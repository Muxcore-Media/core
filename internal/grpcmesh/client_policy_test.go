package grpcmesh

import (
	"context"
	"strings"
	"testing"

	"github.com/Muxcore-Media/core/internal/callerid"
)

// stubHandler is a MeshHandler that echoes its payload back as the result.
type stubHandler struct{}

func (stubHandler) HandleCall(_ context.Context, _ string, payload []byte) ([]byte, error) {
	return payload, nil
}

// stubCallPolicy is a configurable CallPolicyProvider for testing.
type stubCallPolicy struct{ allow bool }

func (p stubCallPolicy) AllowCall(_ context.Context, _, _, _ string) (bool, error) {
	return p.allow, nil
}

// TestClient_Call_NilPolicy_Denied verifies that when no call policy is
// configured, Client.Call() returns a denied error rather than allowing the
// call through. This is the deny-by-default invariant.
func TestClient_Call_NilPolicy_Denied(t *testing.T) {
	srv := NewServer()
	srv.RegisterHandler("target-module", stubHandler{})

	client := NewClient(srv)
	// No call policy set — must deny.

	_, err := client.Call(context.Background(), "target-module", "DoSomething", []byte("payload"))
	if err == nil {
		t.Fatal("expected error when no call policy is configured, got nil")
	}
	if !strings.Contains(err.Error(), "no call policy configured") {
		t.Errorf("expected 'no call policy configured' in error, got: %v", err)
	}
}

// TestClient_Call_WithAllowPolicy_Succeeds verifies that a registered allow
// policy permits the call.
func TestClient_Call_WithAllowPolicy_Succeeds(t *testing.T) {
	srv := NewServer()
	srv.RegisterHandler("target-module", stubHandler{})

	client := NewClient(srv)
	client.SetCallPolicy(stubCallPolicy{allow: true})

	result, err := client.Call(context.Background(), "target-module", "Echo", []byte("hello"))
	if err != nil {
		t.Fatalf("expected success with allow policy, got: %v", err)
	}
	if string(result) != "hello" {
		t.Errorf("expected echo 'hello', got %q", result)
	}
}

// TestClient_Call_WithDenyPolicy_Denied verifies that a registered deny policy
// blocks the call.
func TestClient_Call_WithDenyPolicy_Denied(t *testing.T) {
	srv := NewServer()
	srv.RegisterHandler("target-module", stubHandler{})

	client := NewClient(srv)
	client.SetCallPolicy(stubCallPolicy{allow: false})

	_, err := client.Call(
		callerid.Set(context.Background(), "caller-module"),
		"target-module", "Restricted", nil,
	)
	if err == nil {
		t.Fatal("expected error when call denied by policy, got nil")
	}
	if !strings.Contains(err.Error(), "call denied by policy") {
		t.Errorf("expected 'call denied by policy' in error, got: %v", err)
	}
}
