// Package client provides a Go client for connecting to a running muxcored instance.
//
// The Client wraps the five core gRPC services — Discovery, Events, Storage,
// Health, and Mesh — behind a single ergonomic struct. Modules and external
// tools use this to interact with a running core without dealing with gRPC
// boilerplate.
//
// Basic usage:
//
//	c, err := client.Dial("localhost:9090", client.WithInsecure())
//	defer c.Close()
//
//	modules, err := c.Discovery.FindByCapability(ctx, "storage")
//	rc, err := c.Storage.Get(ctx, "media/movie.mkv")
//
// For failover across multiple core instances, use DialWithAddrs or
// WithFallbackAddrs:
//
//	c, err := client.Dial("core1:9090", client.WithFallbackAddrs("core2:9090", "core3:9090"))
//
// When the primary connection enters TransientFailure, the client
// automatically tries fallback addresses in order with exponential backoff
// and replaces the underlying gRPC connection transparently.
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	auditv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/audit/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	healthv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/health/v1"
	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"
	storagev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/storage/v1"
)

// Default reconnect parameters.
const (
	defaultReconnectBackoff  = 1 * time.Second
	defaultMaxReconnectDelay = 30 * time.Second
	// defaultMaxMsgBytes is the default client-side gRPC message size limit (32MB).
	// Matches the server-side default. Prevents unbounded memory allocation.
	defaultMaxMsgBytes = 32 * 1024 * 1024
)

// ReconnectOptions configures the automatic reconnection behaviour.
type ReconnectOptions struct {
	// InitialBackoff is the delay before the first reconnect attempt.
	// Default 1s.
	InitialBackoff time.Duration
	// MaxDelay caps the exponential backoff. Default 30s.
	MaxDelay time.Duration
	// OnReconnect is called after a successful reconnection to a new address.
	OnReconnect func(addr string)
}

// Client is the top-level handle for a connection to muxcored.
// All service clients are pre-initialized on Dial; use them directly.
// When configured with multiple addresses, a background reconnect loop
// monitors the connection and transparently replaces it on failure.
type Client struct {
	mu            sync.Mutex
	conn          *grpc.ClientConn
	currentAddr   string
	addrs         []string
	grpcOpts      []grpc.DialOption
	reconnectOpts ReconnectOptions
	ctx           context.Context
	cancel        context.CancelFunc

	Audit     *AuditClient
	Discovery *DiscoveryClient
	Events    *EventsClient
	Storage   *StorageClient
	Health    *HealthClient
	Mesh      *MeshClient
}

// Option configures a Client before dialing.
type Option func(*dialOptions)

type dialOptions struct {
	grpcOpts      []grpc.DialOption
	addrs         []string
	reconnectOpts ReconnectOptions
}

// WithInsecure disables TLS. Use only for local development.
func WithInsecure() Option {
	return func(o *dialOptions) {
		o.grpcOpts = append(o.grpcOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

// WithGRPCOption passes a raw gRPC dial option.
func WithGRPCOption(opt grpc.DialOption) Option {
	return func(o *dialOptions) {
		o.grpcOpts = append(o.grpcOpts, opt)
	}
}

// WithFallbackAddrs sets additional core gRPC addresses to try when the
// primary connection fails. The primary address is the one passed to Dial().
// When connected to a fallback, the client continues to monitor the
// connection and will try earlier addresses again on the next failure.
func WithFallbackAddrs(addrs ...string) Option {
	return func(o *dialOptions) {
		o.addrs = append(o.addrs, addrs...)
	}
}

// WithReconnect configures reconnection parameters.
// If not called, defaults are used (1s initial backoff, 30s max delay).
func WithReconnect(initialBackoff, maxDelay time.Duration) Option {
	return func(o *dialOptions) {
		o.reconnectOpts.InitialBackoff = initialBackoff
		o.reconnectOpts.MaxDelay = maxDelay
	}
}

// WithOnReconnect sets a callback invoked after each successful
// reconnection. The callback receives the address of the new core.
func WithOnReconnect(fn func(addr string)) Option {
	return func(o *dialOptions) {
		o.reconnectOpts.OnReconnect = fn
	}
}

// applyDefaults fills zero-valued reconnect options.
func applyDefaults(opts *ReconnectOptions) {
	if opts.InitialBackoff <= 0 {
		opts.InitialBackoff = defaultReconnectBackoff
	}
	if opts.MaxDelay <= 0 {
		opts.MaxDelay = defaultMaxReconnectDelay
	}
}

// initServiceClients creates all gRPC service clients from a connection.
func initServiceClients(conn *grpc.ClientConn) (*AuditClient, *DiscoveryClient, *EventsClient, *StorageClient, *HealthClient, *MeshClient) {
	return &AuditClient{auditv1.NewAuditServiceClient(conn)},
		&DiscoveryClient{discoveryv1.NewDiscoveryServiceClient(conn)},
		&EventsClient{eventsv1.NewEventServiceClient(conn)},
		&StorageClient{storagev1.NewStorageServiceClient(conn)},
		&HealthClient{healthv1.NewHealthServiceClient(conn)},
		&MeshClient{meshv1.NewModuleMeshClient(conn)}
}

// Dial connects to a muxcored gRPC endpoint and returns a Client.
// The caller must call Close() when done.
// The address is the primary; use WithFallbackAddrs for failover addresses.
func Dial(addr string, opts ...Option) (*Client, error) {
	return DialWithAddrs([]string{addr}, opts...)
}

// DialWithAddrs connects to a muxcored gRPC endpoint with full failover
// support. The first address is the primary; subsequent addresses are
// fallbacks tried in order on connection failure.
//
// If only one address is supplied, the client still runs a reconnect loop
// that re-dials the same address on transient failure (useful for short
// core restarts).
//
// The caller must call Close() when done.
func DialWithAddrs(addrs []string, opts ...Option) (*Client, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("client: at least one address is required")
	}

	do := &dialOptions{}
	for _, o := range opts {
		o(do)
	}

	// Merge addresses: the first DialWithAddrs arg is primary, WithFallbackAddrs
	// args are additional fallbacks.
	allAddrs := make([]string, 0, len(addrs)+len(do.addrs))
	allAddrs = append(allAddrs, addrs...)
	allAddrs = append(allAddrs, do.addrs...)

	applyDefaults(&do.reconnectOpts)

	// Prepend default message size limits so callers can override if needed.
	msgSizeOpt := grpc.WithDefaultCallOptions(
		grpc.MaxCallRecvMsgSize(defaultMaxMsgBytes),
		grpc.MaxCallSendMsgSize(defaultMaxMsgBytes),
	)
	do.grpcOpts = append([]grpc.DialOption{msgSizeOpt}, do.grpcOpts...)

	conn, err := grpc.NewClient(allAddrs[0], do.grpcOpts...)
	if err != nil {
		return nil, fmt.Errorf("client: dial %s: %w", allAddrs[0], err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		conn:          conn,
		currentAddr:   allAddrs[0],
		addrs:         allAddrs,
		grpcOpts:      do.grpcOpts,
		reconnectOpts: do.reconnectOpts,
		ctx:           ctx,
		cancel:        cancel,
	}
	c.Audit, c.Discovery, c.Events, c.Storage, c.Health, c.Mesh = initServiceClients(conn)

	// Start the reconnect loop if there are fallback addresses or more than one.
	if len(allAddrs) > 1 {
		go c.reconnectLoop()
	}

	return c, nil
}

// Close closes the underlying gRPC connection and stops the reconnect loop.
// Safe to call multiple times.
func (c *Client) Close() error {
	c.cancel()
	return c.conn.Close()
}

// replaceConn atomically swaps the gRPC connection and all service clients.
// The old connection is closed asynchronously.
func (c *Client) replaceConn(newConn *grpc.ClientConn, addr string) {
	aud, disc, ev, stor, hlth, mesh := initServiceClients(newConn)

	c.mu.Lock()
	oldConn := c.conn
	c.conn = newConn
	c.currentAddr = addr
	c.Audit = aud
	c.Discovery = disc
	c.Events = ev
	c.Storage = stor
	c.Health = hlth
	c.Mesh = mesh
	c.mu.Unlock()

	slog.Info("client: reconnected to core", "addr", addr)

	// Close the old connection in the background so existing in-flight
	// RPCs drain gracefully before the underlying transport is torn down.
	if oldConn != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("client close old conn panic recovered", "panic", r)
				}
			}()
			oldConn.Close()
		}()
	}
}

// reconnectLoop monitors gRPC connection state and attempts to connect to
// fallback addresses when the current connection enters TransientFailure.
// Runs until the client context is cancelled (Close is called).
func (c *Client) reconnectLoop() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("client reconnect loop panic recovered", "panic", r)
		}
	}()
	backoff := c.reconnectOpts.InitialBackoff
	addrIdx := 0 // index into c.addrs for the next address to try

	for {
		// Wait for the current connection to fail.
		if !c.waitForFailure(c.ctx) {
			return // context cancelled
		}

		// Move to the next address (round-robin with fallback).
		addrIdx = (addrIdx + 1) % len(c.addrs)
		addr := c.addrs[addrIdx]

		slog.Info("client: attempting reconnect", "addr", addr, "backoff", backoff)

		select {
		case <-c.ctx.Done():
			return
		case <-time.After(backoff):
		}

		newConn, err := grpc.NewClient(addr, c.grpcOpts...)
		if err != nil {
			slog.Warn("client: reconnect failed", "addr", addr, "error", err)
			backoff *= 2
			if backoff > c.reconnectOpts.MaxDelay {
				backoff = c.reconnectOpts.MaxDelay
			}
			continue
		}

		c.replaceConn(newConn, addr)
		backoff = c.reconnectOpts.InitialBackoff

		if c.reconnectOpts.OnReconnect != nil {
			c.reconnectOpts.OnReconnect(addr)
		}
	}
}

// waitForFailure blocks until the current connection enters
// TransientFailure or Shutdown state. Returns false if the context
// is cancelled (client is closing).
func (c *Client) waitForFailure(ctx context.Context) bool {
	state := c.conn.GetState()
	for state != connectivity.TransientFailure && state != connectivity.Shutdown {
		if !c.conn.WaitForStateChange(ctx, state) {
			return false
		}
		state = c.conn.GetState()
	}
	return true
}

// CurrentAddr returns the address of the core this client is currently
// connected to. Useful after reconnection for logging.
func (c *Client) CurrentAddr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentAddr
}

// --- Discovery ---

// DiscoveryClient wraps the DiscoveryService gRPC client with ergonomic methods.
type DiscoveryClient struct {
	raw discoveryv1.DiscoveryServiceClient
}

// FindByCapability returns all modules advertising the given capability string.
func (d *DiscoveryClient) FindByCapability(ctx context.Context, capability string) ([]*discoveryv1.ModuleInfoProto, error) {
	resp, err := d.raw.FindByCapability(ctx, &discoveryv1.FindByCapabilityRequest{Capability: capability})
	if err != nil {
		return nil, err
	}
	return resp.GetModules(), nil
}

// FindByRole returns all modules with the given role string.
func (d *DiscoveryClient) FindByRole(ctx context.Context, role string) ([]*discoveryv1.ModuleInfoProto, error) {
	resp, err := d.raw.FindByRole(ctx, &discoveryv1.FindByRoleRequest{Role: role})
	if err != nil {
		return nil, err
	}
	return resp.GetModules(), nil
}

// Resolve looks up a single module by ID. Returns (nil, nil) if not found.
func (d *DiscoveryClient) Resolve(ctx context.Context, moduleID string) (*discoveryv1.ModuleInfoProto, error) {
	resp, err := d.raw.Resolve(ctx, &discoveryv1.ResolveRequest{ModuleId: moduleID})
	if err != nil {
		return nil, err
	}
	if !resp.GetFound() {
		return nil, nil
	}
	return resp.GetModule(), nil
}

// Members returns the current cluster member list.
func (d *DiscoveryClient) Members(ctx context.Context) ([]*discoveryv1.NodeInfo, string, error) {
	resp, err := d.raw.Members(ctx, &discoveryv1.MembersRequest{})
	if err != nil {
		return nil, "", err
	}
	return resp.GetMembers(), resp.GetLeaderId(), nil
}

// Join requests cluster membership. Sends joinToken via metadata if non-empty.
func (d *DiscoveryClient) Join(ctx context.Context, node *discoveryv1.NodeInfo) (*discoveryv1.JoinResponse, error) {
	return d.raw.Join(ctx, &discoveryv1.JoinRequest{Node: node})
}

// Leave gracefully removes a node from the cluster.
func (d *DiscoveryClient) Leave(ctx context.Context, nodeID string) error {
	_, err := d.raw.Leave(ctx, &discoveryv1.LeaveRequest{NodeId: nodeID})
	return err
}

// Raw returns the underlying gRPC client for advanced use.
func (d *DiscoveryClient) Raw() discoveryv1.DiscoveryServiceClient { return d.raw }

// --- Events ---

// EventsClient wraps the EventService gRPC client.
type EventsClient struct {
	raw eventsv1.EventServiceClient
}

// Publish publishes an event to the core event bus.
func (e *EventsClient) Publish(ctx context.Context, eventType, source string, payload []byte) error {
	_, err := e.raw.Publish(ctx, &eventsv1.PublishRequest{
		Event: &eventsv1.Event{
			Type:    eventType,
			Source:  source,
			Payload: payload,
		},
	})
	return err
}

// Subscribe opens a streaming subscription for the given event type.
// Returns a channel of events and a cancel func. Close cancel when done.
func (e *EventsClient) Subscribe(ctx context.Context, eventType string) (<-chan *eventsv1.Event, context.CancelFunc, error) {
	subCtx, cancel := context.WithCancel(ctx)
	stream, err := e.raw.Subscribe(subCtx, &eventsv1.SubscribeRequest{EventTypes: []string{eventType}})
	if err != nil {
		cancel()
		return nil, nil, err
	}

	ch := make(chan *eventsv1.Event, 64)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("client subscribe stream panic recovered", "panic", r)
			}
		}()
		defer close(ch)
		for {
			ev, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
					slog.Debug("client subscribe stream ended", "error", err)
				}
				return
			}
			select {
			case ch <- ev:
			case <-subCtx.Done():
				return
			}
		}
	}()

	return ch, cancel, nil
}

// Raw returns the underlying gRPC client.
func (e *EventsClient) Raw() eventsv1.EventServiceClient { return e.raw }

// --- Storage ---

// StorageClient wraps the StorageService gRPC client.
type StorageClient struct {
	raw storagev1.StorageServiceClient
}

// Put uploads an object. Reads all of r before sending.
func (s *StorageClient) Put(ctx context.Context, key string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("storage.Put: read: %w", err)
	}

	stream, err := s.raw.Put(ctx)
	if err != nil {
		return fmt.Errorf("storage.Put: open stream: %w", err)
	}

	// Send header chunk with metadata.
	if err := stream.Send(&storagev1.PutRequest{
		Key:       key,
		Chunk:     data,
		TotalSize: int64(len(data)),
	}); err != nil {
		return fmt.Errorf("storage.Put: send: %w", err)
	}

	_, err = stream.CloseAndRecv()
	return err
}

// Get downloads an object and returns it as a ReadCloser.
// The caller must close the returned reader.
func (s *StorageClient) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	stream, err := s.raw.Get(ctx, &storagev1.GetRequest{Key: key})
	if err != nil {
		return nil, fmt.Errorf("storage.Get: %w", err)
	}

	pr, pw := io.Pipe()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				pw.CloseWithError(fmt.Errorf("client get stream panic: %v", r))
			}
		}()
		for {
			chunk, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				pw.Close()
				return
			}
			if err != nil {
				pw.CloseWithError(err)
				return
			}
			if _, err := pw.Write(chunk.GetChunk()); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
	}()

	return pr, nil
}

// Delete removes an object.
func (s *StorageClient) Delete(ctx context.Context, key string) error {
	_, err := s.raw.Delete(ctx, &storagev1.DeleteRequest{Key: key})
	return err
}

// Stat returns metadata for an object. Returns (nil, nil) if the object does not exist.
func (s *StorageClient) Stat(ctx context.Context, key string) (*storagev1.StatResponse, error) {
	resp, err := s.raw.Stat(ctx, &storagev1.StatRequest{Key: key})
	if err != nil {
		return nil, err
	}
	if !resp.GetFound() {
		return nil, nil
	}
	return resp, nil
}

// List returns all objects under the given prefix.
func (s *StorageClient) List(ctx context.Context, prefix string) ([]*storagev1.StatResponse, error) {
	resp, err := s.raw.List(ctx, &storagev1.ListRequest{Prefix: prefix})
	if err != nil {
		return nil, err
	}
	return resp.GetObjects(), nil
}

// Capabilities returns the capability strings advertised by the storage provider.
func (s *StorageClient) Capabilities(ctx context.Context) ([]string, error) {
	resp, err := s.raw.Capabilities(ctx, &storagev1.CapabilitiesRequest{})
	if err != nil {
		return nil, err
	}
	return resp.GetCapabilities(), nil
}

// Raw returns the underlying gRPC client.
func (s *StorageClient) Raw() storagev1.StorageServiceClient { return s.raw }

// --- Health ---

// HealthClient wraps the HealthService gRPC client.
type HealthClient struct {
	raw healthv1.HealthServiceClient
}

// Check returns the health status of the node or a specific module.
// Pass moduleID="" to check overall node health.
func (h *HealthClient) Check(ctx context.Context, moduleID string) (*healthv1.HealthCheckResponse, error) {
	return h.raw.Check(ctx, &healthv1.HealthCheckRequest{ModuleId: moduleID})
}

// Raw returns the underlying gRPC client.
func (h *HealthClient) Raw() healthv1.HealthServiceClient { return h.raw }

// --- Mesh ---

// MeshClient wraps the ModuleMesh gRPC client for inter-module calls.
type MeshClient struct {
	raw meshv1.ModuleMeshClient
}

// Call sends a request to a module's named method and returns the response payload.
func (m *MeshClient) Call(ctx context.Context, targetModule, method string, payload []byte) ([]byte, error) {
	resp, err := m.raw.Call(ctx, &meshv1.CallRequest{
		TargetModule: targetModule,
		Method:       method,
		Payload:      payload,
	})
	if err != nil {
		return nil, err
	}
	if resp.GetError() != "" {
		return nil, fmt.Errorf("mesh call error: %s", resp.GetError())
	}
	return resp.GetPayload(), nil
}

// Raw returns the underlying gRPC client.
func (m *MeshClient) Raw() meshv1.ModuleMeshClient { return m.raw }

// --- Audit ---

// AuditClient wraps the AuditService gRPC client for reading and writing
// audit log entries.
type AuditClient struct {
	raw auditv1.AuditServiceClient
}

// Log writes an entry to the core audit log.
func (a *AuditClient) Log(ctx context.Context, actor, action, resource, resourceID, traceID string, details map[string]string) (string, error) {
	resp, err := a.raw.Log(ctx, &auditv1.LogRequest{
		Actor:      actor,
		Action:     action,
		Resource:   resource,
		ResourceId: resourceID,
		Details:    details,
		TraceId:    traceID,
	})
	if err != nil {
		return "", err
	}
	return resp.GetId(), nil
}

// Query returns audit entries matching the given filter parameters.
func (a *AuditClient) Query(ctx context.Context, actor, action, resource, traceID, fromTime, toTime string, maxResults int32) ([]*auditv1.AuditEntryProto, error) {
	resp, err := a.raw.Query(ctx, &auditv1.AuditQueryRequest{
		Actor:      actor,
		Action:     action,
		Resource:   resource,
		TraceId:    traceID,
		FromTime:   fromTime,
		ToTime:     toTime,
		MaxResults: maxResults,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetEntries(), nil
}

// Raw returns the underlying gRPC client.
func (a *AuditClient) Raw() auditv1.AuditServiceClient { return a.raw }
