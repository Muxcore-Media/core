package grpcmesh

import "testing"

func TestTenantNodeFinderNil(t *testing.T) {
	if TenantNodeFinder(nil) != nil {
		t.Fatal("expected nil")
	}
}
