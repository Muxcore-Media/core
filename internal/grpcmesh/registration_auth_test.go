package grpcmesh

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/events"
	modlifecycle "github.com/Muxcore-Media/core/internal/module"
	modulemgr "github.com/Muxcore-Media/core/internal/module/mgr"
	"github.com/Muxcore-Media/core/internal/registry"
	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// startRegistrationServer runs the ModuleRegistration service behind the
// auth interceptor on a core-like mTLS listener (VerifyClientCertIfGiven
// against the core CA) with the given ADR-0018 policy. setup runs on the
// manager before serving.
func startRegistrationServer(t *testing.T, ca *CertAuthority, pol modulemgr.RegistrationPolicy, setup ...func(*modulemgr.Manager)) (string, *registry.Registry) {
	t.Helper()
	reg := registry.New()
	life := modlifecycle.NewManager(reg, events.NewMemoryBus())
	mgr := modulemgr.NewManager("127.0.0.1:9090", reg, life)
	mgr.SetRegistrationPolicy(pol)
	for _, f := range setup {
		f(mgr)
	}

	auth := NewAuthInterceptor()
	t.Cleanup(auth.StopCleanup)
	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(serverTLSConfig(t, ca, tls.VerifyClientCertIfGiven))),
		grpc.UnaryInterceptor(auth.UnaryInterceptor()),
	)
	mgr.RegisterModuleService(srv)
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), reg
}

func registrationClient(t *testing.T, addr string, creds credentials.TransportCredentials) modulev1.ModuleRegistrationClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return modulev1.NewModuleRegistrationClient(conn)
}

func registerOver(t *testing.T, c modulev1.ModuleRegistrationClient, id string, caps ...string) codes.Code {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := c.Register(ctx, &modulev1.RegisterRequest{
		ModuleId:   id,
		ModuleInfo: &modulev1.ModuleInfo{Id: id, Name: id, Version: "1.0.0", Capabilities: caps},
	})
	if err != nil {
		return status.Code(err)
	}
	if !resp.Accepted {
		t.Fatalf("register %s: not accepted: %s", id, resp.Error)
	}
	return codes.OK
}

// ADR-0018 over a real mTLS handshake in the household profile.
func TestRegistration_HouseholdOverMTLS(t *testing.T) {
	ca := testCA(t)
	addr, reg := startRegistrationServer(t, ca, modulemgr.RegistrationPolicy{Household: true})

	authCert := moduleTLSCert(t, ca, "auth-local")
	moviesCert := moduleTLSCert(t, ca, "media-movies")
	owner := registrationClient(t, addr, clientTLSCreds(t, ca, &authCert))
	movies := registrationClient(t, addr, clientTLSCreds(t, ca, &moviesCert))
	anon := registrationClient(t, addr, clientTLSCreds(t, ca, nil))

	if code := registerOver(t, anon, "auth-local", "auth", "identity"); code != codes.PermissionDenied {
		t.Fatalf("uncertified security provider: %v", code)
	}
	if code := registerOver(t, movies, "auth-local", "auth"); code != codes.PermissionDenied {
		t.Fatalf("CN mismatch: %v", code)
	}
	if code := registerOver(t, owner, "auth-local", "auth", "identity"); code != codes.OK {
		t.Fatalf("owner register: %v", code)
	}
	if code := registerOver(t, owner, "auth-local", "auth", "identity"); code != codes.OK {
		t.Fatalf("owner re-register (replace): %v", code)
	}
	if code := registerOver(t, movies, "media-movies", "identity"); code != codes.FailedPrecondition {
		t.Fatalf("second identity provider: %v", code)
	}
	if code := registerOver(t, anon, "media-tv", "media.tv"); code != codes.OK {
		t.Fatalf("stage 1 uncertified non-security module: %v", code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for name, c := range map[string]modulev1.ModuleRegistrationClient{"anonymous": anon, "other module": movies} {
		if _, err := c.Unregister(ctx, &modulev1.UnregisterRequest{ModuleId: "auth-local"}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s unregister: %v", name, err)
		}
	}
	if _, err := reg.Get("auth-local"); err != nil {
		t.Fatal("auth-local was unregistered by a non-owner")
	}
	resp, err := owner.Unregister(ctx, &modulev1.UnregisterRequest{ModuleId: "auth-local"})
	if err != nil || !resp.Acknowledged {
		t.Fatalf("owner unregister: %v %v", resp, err)
	}
}

// In dev the CN mismatch rule still applies; other rows are permissive.
func TestRegistration_DevOverMTLS(t *testing.T) {
	ca := testCA(t)
	addr, reg := startRegistrationServer(t, ca, modulemgr.RegistrationPolicy{})
	moviesCert := moduleTLSCert(t, ca, "media-movies")
	movies := registrationClient(t, addr, clientTLSCreds(t, ca, &moviesCert))
	anon := registrationClient(t, addr, clientTLSCreds(t, ca, nil))

	if code := registerOver(t, movies, "auth-local", "auth"); code != codes.PermissionDenied {
		t.Fatalf("dev CN mismatch: %v", code)
	}
	if code := registerOver(t, anon, "auth-local", "auth"); code != codes.OK {
		t.Fatalf("dev uncertified security provider: %v", code)
	}
	if code := registerOver(t, anon, "auth-two", "auth"); code != codes.OK {
		t.Fatalf("dev second provider: %v", code)
	}
	if p, _ := reg.Provider("auth"); p.Info.ID != "auth-local" {
		t.Fatalf("dev provider of record = %s", p.Info.ID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if resp, err := anon.Unregister(ctx, &modulev1.UnregisterRequest{ModuleId: "auth-local"}); err != nil || !resp.Acknowledged {
		t.Fatalf("dev unregister: %v %v", resp, err)
	}
}
