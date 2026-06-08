package spool

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchTag_Success(t *testing.T) {
	tag := TagDefinition{
		Name:    "default",
		Version: "1.0.0",
		Modules: []TagModule{
			{Repo: "https://github.com/Muxcore-Media/admin-ui", Version: "v1.0.0", Required: true},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tags/default.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(tag)
	}))
	defer srv.Close()

	result, err := FetchTag(srv.URL, "default")
	if err != nil {
		t.Fatalf("FetchTag failed: %v", err)
	}
	if result.Name != "default" {
		t.Errorf("expected name 'default', got %q", result.Name)
	}
	if len(result.Modules) != 1 {
		t.Errorf("expected 1 module, got %d", len(result.Modules))
	}
}

func TestFetchTag_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FetchTag(srv.URL, "nonexistent")
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestFetchTag_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := FetchTag(srv.URL, "bad")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestBuildFetchURL_GitHub(t *testing.T) {
	u, err := buildFetchURL("https://github.com/Muxcore-Media/spool", "default")
	if err != nil {
		t.Fatalf("buildFetchURL failed: %v", err)
	}
	expected := "https://raw.githubusercontent.com/Muxcore-Media/spool/master/tags/default.json"
	if u != expected {
		t.Errorf("expected %q, got %q", expected, u)
	}
}

func TestBuildFetchURL_NonGitHub(t *testing.T) {
	u, err := buildFetchURL("https://my-spool.example.com", "custom")
	if err != nil {
		t.Fatalf("buildFetchURL failed: %v", err)
	}
	expected := "https://my-spool.example.com/tags/custom.json"
	if u != expected {
		t.Errorf("expected %q, got %q", expected, u)
	}
}

func TestBuildFetchURL_InvalidURL(t *testing.T) {
	_, err := buildFetchURL("://bad-url", "tag")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}
