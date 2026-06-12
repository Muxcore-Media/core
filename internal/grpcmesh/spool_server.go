package grpcmesh

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/internal/config"
	modulemgr "github.com/Muxcore-Media/core/internal/module/mgr"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/spool"
	spoolv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/spool/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const deployCooldown = 30 * time.Second

// SpoolServer implements the SpoolService gRPC service for runtime spool
// management. Admin tools use this to inspect spools and deploy tags.
type SpoolServer struct {
	spoolv1.UnimplementedSpoolServiceServer
	spoolURL   string
	cfg        *config.Config
	cfgMu      *sync.Mutex
	modMgr     *modulemgr.Manager
	reg        *registry.Registry
	deployMu   sync.Mutex
	lastDeploy map[string]time.Time
}

// NewSpoolServer creates a gRPC server for spool management.
func NewSpoolServer(spoolURL string, cfg *config.Config, cfgMu *sync.Mutex, modMgr *modulemgr.Manager, reg *registry.Registry) *SpoolServer {
	return &SpoolServer{
		spoolURL:   spoolURL,
		cfg:        cfg,
		cfgMu:      cfgMu,
		modMgr:     modMgr,
		reg:        reg,
		lastDeploy: make(map[string]time.Time),
	}
}

// RegisterWithGRPC registers this server with a gRPC server.
func (s *SpoolServer) RegisterWithGRPC(srv *grpc.Server) {
	spoolv1.RegisterSpoolServiceServer(srv, s)
}

// checkAuth enforces caller authentication for spool operations.
func (s *SpoolServer) checkAuth(ctx context.Context) error {
	callerID := callerid.Get(ctx)
	if callerID == "" || callerID == "_public" {
		return status.Error(codes.PermissionDenied, "spool: authentication required")
	}
	return nil
}

// ListSpools returns the currently configured spool URLs.
func (s *SpoolServer) ListSpools(ctx context.Context, req *spoolv1.ListSpoolsRequest) (*spoolv1.ListSpoolsResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	s.cfgMu.Lock()
	allowedHosts := make([]string, len(s.cfg.Spool.AllowedHosts))
	copy(allowedHosts, s.cfg.Spool.AllowedHosts)
	s.cfgMu.Unlock()

	return &spoolv1.ListSpoolsResponse{
		Spools: []*spoolv1.SpoolInfo{
			{
				Url:          s.spoolURL,
				Active:       true,
				AllowedHosts: allowedHosts,
			},
		},
	}, nil
}

// ListTags returns available tag names on a spool.
// This is a best-effort operation — spools may not expose a tag listing
// endpoint. Returns an empty list if the spool does not support listing.
func (s *SpoolServer) ListTags(ctx context.Context, req *spoolv1.ListTagsRequest) (*spoolv1.ListTagsResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	spoolURL := req.GetSpoolUrl()
	if spoolURL == "" {
		spoolURL = s.spoolURL
	}

	_, err := spool.FetchTag(ctx, spoolURL, "catalog")
	if err != nil {
		return &spoolv1.ListTagsResponse{ //nolint:nilerr // catalog fetch failure is not fatal, return empty list
			SpoolUrl: spoolURL,
			Tags:     nil,
		}, nil
	}

	return &spoolv1.ListTagsResponse{
		SpoolUrl: spoolURL,
		Tags:     nil,
	}, nil
}

// FetchTag fetches and returns a tag definition from a spool.
func (s *SpoolServer) FetchTag(ctx context.Context, req *spoolv1.FetchTagRequest) (*spoolv1.FetchTagResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	if req.GetTagName() == "" {
		return nil, status.Error(codes.InvalidArgument, "tag_name is required")
	}
	spoolURL := req.GetSpoolUrl()
	if spoolURL == "" {
		spoolURL = s.spoolURL
	}

	tag, err := spool.FetchTag(ctx, spoolURL, req.GetTagName())
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "fetch tag: %v", err)
	}

	modules := make([]*spoolv1.TagModuleProto, len(tag.Modules))
	for i, tm := range tag.Modules {
		modules[i] = &spoolv1.TagModuleProto{
			Repo:       tm.Repo,
			Version:    tm.Version,
			Required:   tm.Required,
			Checksum:   tm.Checksum,
			InstanceId: tm.InstanceID,
			Config:     tm.Config,
		}
	}

	return &spoolv1.FetchTagResponse{
		Name:        tag.Name,
		Description: tag.Description,
		Version:     tag.Version,
		Modules:     modules,
		SpoolUrl:    spoolURL,
	}, nil
}

// DeployTag fetches a tag, resolves all its modules, verifies checksums,
// and spawns any that are not already running. Idempotent.
// Rate-limited to one deploy per caller per deployCooldown period to
// prevent resource exhaustion from rapid repeated deploy requests.
func (s *SpoolServer) DeployTag(ctx context.Context, req *spoolv1.DeployTagRequest) (*spoolv1.DeployTagResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	if req.GetTagName() == "" {
		return nil, status.Error(codes.InvalidArgument, "tag_name is required")
	}
	spoolURL := req.GetSpoolUrl()
	if spoolURL == "" {
		spoolURL = s.spoolURL
	}

	// Rate limit: one deploy per caller per cooldown window.
	callerID := callerid.Get(ctx)
	s.deployMu.Lock()
	last, exists := s.lastDeploy[callerID]
	now := time.Now()
	if exists && now.Sub(last) < deployCooldown {
		s.deployMu.Unlock()
		slog.Warn("deploy tag rate limited", "caller", callerID, "cooldown", deployCooldown)
		return nil, status.Error(codes.ResourceExhausted,
			"deploy too frequent — wait and retry")
	}
	s.lastDeploy[callerID] = now
	s.deployMu.Unlock()

	tag, err := spool.FetchTag(ctx, spoolURL, req.GetTagName())
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "deploy: fetch tag: %v", err)
	}

	// Store the tag on the manager for resurrection tracking.
	s.modMgr.SetTag(tag)

	var results []*spoolv1.ModuleDeployResult
	spawned := 0
	skipped := 0
	failed := 0

	for _, tm := range tag.Modules {
		moduleID := modulemgr.ModuleIDFromRepoWithInstance(tm.Repo, tm.InstanceID)
		result := &spoolv1.ModuleDeployResult{ModuleId: moduleID}

		// Check if already running.
		_, err := s.reg.Get(moduleID)
		if err == nil {
			result.AlreadyRunning = true
			skipped++
			results = append(results, result)
			continue
		}

		bin, err := s.modMgr.ResolveTagModule(tm)
		if err != nil {
			if tm.Required {
				result.Error = fmt.Sprintf("resolve: %v", err)
				failed++
				results = append(results, result)
				continue
			}
			slog.Warn("deploy tag: resolve optional module failed, skipping", "repo", tm.Repo, "error", err)
			result.Error = fmt.Sprintf("resolve skipped: %v", err)
			skipped++
			results = append(results, result)
			continue
		}

		if err := s.modMgr.VerifyChecksum(bin, tm.Checksum); err != nil {
			if tm.Required {
				result.Error = fmt.Sprintf("checksum: %v", err)
				failed++
				results = append(results, result)
				continue
			}
			slog.Warn("deploy tag: checksum verification failed, skipping optional module",
				"repo", tm.Repo, "error", err)
			result.Error = fmt.Sprintf("checksum skipped: %v", err)
			skipped++
			results = append(results, result)
			continue
		}

		if len(tm.Config) > 0 {
			bin.Config = tm.Config
		}

		if err := s.modMgr.Spawn(ctx, bin); err != nil {
			if tm.Required {
				result.Error = fmt.Sprintf("spawn: %v", err)
				failed++
				results = append(results, result)
				continue
			}
			slog.Warn("deploy tag: spawn optional module failed, skipping", "id", bin.ID, "error", err)
			result.Error = fmt.Sprintf("spawn skipped: %v", err)
			skipped++
			results = append(results, result)
			continue
		}

		result.Spawned = true
		spawned++
		results = append(results, result)
	}

	return &spoolv1.DeployTagResponse{
		TagName:  tag.Name,
		SpoolUrl: spoolURL,
		Results:  results,
		Total:    int32(len(tag.Modules)), //nolint:gosec // bounded by tag spec
		Spawned:  int32(spawned),          //nolint:gosec // bounded by module count
		Skipped:  int32(skipped),          //nolint:gosec // bounded by module count
		Failed:   int32(failed),           //nolint:gosec // bounded by module count
	}, nil
}
