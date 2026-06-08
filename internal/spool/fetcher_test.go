package spool

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func TestFetchTag_HTTPS(t *testing.T) {
	tag := contracts.TagDefinition{
		Name:    "default",
		Version: "1.0.0",
		Modules: []contracts.TagModule{
			{Repo: "https://github.com/Muxcore-Media/admin-ui", Version: "v1.0.0", Required: true},
		},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tags/default.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(tag)
	}))
	defer srv.Close()

	// Use a client that trusts the test server's self-signed cert.
	saved := client
	t.Cleanup(func() { client = saved })
	client = srv.Client()

	got, err := FetchTag(srv.URL, "default")
	if err != nil {
		t.Fatalf("FetchTag failed: %v", err)
	}
	if got.Name != "default" {
		t.Errorf("expected name 'default', got %q", got.Name)
	}
	if len(got.Modules) != 1 {
		t.Errorf("expected 1 module, got %d", len(got.Modules))
	}
}

func TestFetchTag_HTTPRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := FetchTag(srv.URL, "test")
	if err == nil {
		t.Fatal("expected error for HTTP URL (non-TLS)")
	}
}

func TestFetchTag_NotFound(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FetchTag(srv.URL, "nonexistent")
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestFetchTag_EmptyInput(t *testing.T) {
	_, err := FetchTag("", "tag")
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	_, err = FetchTag("https://example.com", "")
	if err == nil {
		t.Fatal("expected error for empty tag name")
	}
}

func TestFetchTag_InvalidJSON(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	_, err := FetchTag(srv.URL, "bad")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
