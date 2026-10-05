package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func getJSON(t *testing.T, srv *Server, path string) map[string]any {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: decode %q: %v", path, rec.Body.String(), err)
	}
	return body
}

func TestHealthAndVersion_ReportProfile(t *testing.T) {
	srv := NewServer(":0", "", "")

	// Not set: no profile fields (backward compatible).
	if _, ok := getJSON(t, srv, "/health")["profile"]; ok {
		t.Fatal("profile must be absent until SetProfile")
	}

	srv.SetProfile("dev", true)
	for _, path := range []string{"/health", "/version"} {
		b := getJSON(t, srv, path)
		if b["profile"] != "dev" || b["insecure"] != true {
			t.Fatalf("%s: dev profile not reported: %v", path, b)
		}
	}

	srv.SetProfile("household", false)
	srv.SetHealthChecker(func() map[string]error { return map[string]error{"m": nil} })
	b := getJSON(t, srv, "/health")
	if b["profile"] != "household" || b["insecure"] != false || b["status"] != "ok" {
		t.Fatalf("household profile not reported: %v", b)
	}
}
