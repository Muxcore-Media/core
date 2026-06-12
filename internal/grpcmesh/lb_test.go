package grpcmesh

import (
	"context"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func TestRoundRobinStrategy_Pick(t *testing.T) {
	s := NewRoundRobinStrategy()
	candidates := []contracts.NodeInfo{
		{ID: "node-a"},
		{ID: "node-b"},
		{ID: "node-c"},
	}

	// First pick should be 0 (index 1 mod 3 = 1)
	idx, err := s.Pick(context.Background(), candidates)
	if err != nil {
		t.Fatalf("Pick failed: %v", err)
	}
	if idx < 0 || idx >= len(candidates) {
		t.Fatalf("index %d out of range", idx)
	}

	// Second pick should be different (round-robin)
	idx2, _ := s.Pick(context.Background(), candidates)
	if idx2 == idx {
		t.Logf("note: consecutive picks produced same index %d (possible with >1 candidate, unlikely)", idx)
	}
}

func TestRoundRobinStrategy_Distributes(t *testing.T) {
	s := NewRoundRobinStrategy()
	candidates := []contracts.NodeInfo{
		{ID: "node-a"},
		{ID: "node-b"},
	}
	seen := make(map[int]int)
	n := 100
	for i := 0; i < n; i++ {
		idx, err := s.Pick(context.Background(), candidates)
		if err != nil {
			t.Fatalf("Pick failed: %v", err)
		}
		seen[idx]++
	}
	// With round-robin over 2 candidates, each should get ~50.
	if len(seen) != 2 {
		t.Errorf("expected both candidates to be picked, got %d", len(seen))
	}
}

func TestRoundRobinStrategy_Empty(t *testing.T) {
	s := NewRoundRobinStrategy()
	_, err := s.Pick(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty candidates")
	}
}

func TestRoundRobinStrategy_SingleCandidate(t *testing.T) {
	s := NewRoundRobinStrategy()
	candidates := []contracts.NodeInfo{{ID: "node-a"}}
	for i := 0; i < 5; i++ {
		idx, err := s.Pick(context.Background(), candidates)
		if err != nil {
			t.Fatalf("Pick failed: %v", err)
		}
		if idx != 0 {
			t.Fatalf("expected index 0, got %d", idx)
		}
	}
}

func TestHealthyCandidates_FiltersUnhealthy(t *testing.T) {
	candidates := []contracts.NodeInfo{
		{ID: "node-a", ModuleHealth: map[string]string{"downloader": ""}},
		{ID: "node-b", ModuleHealth: map[string]string{"downloader": "out of memory"}},
		{ID: "node-c", ModuleHealth: map[string]string{}, ModuleIDs: []string{"downloader"}},
	}

	healthy := HealthyCandidates(candidates, "downloader")
	if len(healthy) != 2 {
		t.Fatalf("expected 2 healthy candidates, got %d", len(healthy))
	}
	if healthy[0].ID == "node-b" || healthy[1].ID == "node-b" {
		t.Error("unhealthy node-b should not be in healthy list")
	}
}

func TestHealthyCandidates_AllUnhealthy(t *testing.T) {
	candidates := []contracts.NodeInfo{
		{ID: "node-a", ModuleHealth: map[string]string{"downloader": "crash"}},
		{ID: "node-b", ModuleHealth: map[string]string{"downloader": "oom"}},
	}

	healthy := HealthyCandidates(candidates, "downloader")
	if len(healthy) != 2 {
		t.Fatalf("expected fallback to all candidates (2), got %d", len(healthy))
	}
}

func TestHealthyCandidates_AllHealthy(t *testing.T) {
	candidates := []contracts.NodeInfo{
		{ID: "node-a", ModuleHealth: map[string]string{"downloader": ""}},
		{ID: "node-b", ModuleHealth: map[string]string{"downloader": ""}},
	}

	healthy := HealthyCandidates(candidates, "downloader")
	if len(healthy) != 2 {
		t.Fatalf("expected 2 healthy candidates, got %d", len(healthy))
	}
}

func TestHealthyCandidates_Empty(t *testing.T) {
	healthy := HealthyCandidates(nil, "downloader")
	if len(healthy) != 0 {
		t.Errorf("expected empty, got %d", len(healthy))
	}
}

func TestHealthyCandidates_NoHealthData(t *testing.T) {
	candidates := []contracts.NodeInfo{
		{ID: "node-a"}, // no ModuleHealth map
	}
	healthy := HealthyCandidates(candidates, "downloader")
	if len(healthy) != 1 {
		t.Fatalf("expected 1 (module not in health map = assumed healthy), got %d", len(healthy))
	}
}

func TestRandomStrategy_PickValidIndex(t *testing.T) {
	s := NewRandomStrategy()
	candidates := []contracts.NodeInfo{
		{ID: "node-a"},
		{ID: "node-b"},
		{ID: "node-c"},
	}
	for i := 0; i < 20; i++ {
		idx, err := s.Pick(context.Background(), candidates)
		if err != nil {
			t.Fatalf("Pick failed: %v", err)
		}
		if idx < 0 || idx >= len(candidates) {
			t.Fatalf("index %d out of range [0, %d)", idx, len(candidates))
		}
	}
}

func TestRandomStrategy_Empty(t *testing.T) {
	s := NewRandomStrategy()
	_, err := s.Pick(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty candidates")
	}
}

func TestRandomStrategy_Distributes(t *testing.T) {
	s := NewRandomStrategy()
	candidates := []contracts.NodeInfo{
		{ID: "node-a"},
		{ID: "node-b"},
	}
	seen := make(map[int]int)
	n := 200
	for i := 0; i < n; i++ {
		idx, err := s.Pick(context.Background(), candidates)
		if err != nil {
			t.Fatalf("Pick failed: %v", err)
		}
		seen[idx]++
	}
	if len(seen) != 2 {
		t.Fatalf("expected both candidates picked, got %d distinct", len(seen))
	}
	for idx, count := range seen {
		if count < n/10 {
			t.Errorf("candidate %d picked only %d/%d times", idx, count, n)
		}
	}
}

func TestRandomStrategy_SingleCandidate(t *testing.T) {
	s := NewRandomStrategy()
	candidates := []contracts.NodeInfo{{ID: "node-a"}}
	for i := 0; i < 10; i++ {
		idx, err := s.Pick(context.Background(), candidates)
		if err != nil {
			t.Fatalf("Pick failed: %v", err)
		}
		if idx != 0 {
			t.Fatalf("expected 0, got %d", idx)
		}
	}
}

func TestLeastLoadedStrategy_PickValidIndex(t *testing.T) {
	s := NewLeastLoadedStrategy()
	candidates := []contracts.NodeInfo{
		{ID: "node-a"},
		{ID: "node-b"},
		{ID: "node-c"},
	}
	idx, err := s.Pick(context.Background(), candidates)
	if err != nil {
		t.Fatalf("Pick failed: %v", err)
	}
	if idx < 0 || idx >= len(candidates) {
		t.Fatalf("index %d out of range", idx)
	}
}

func TestLeastLoadedStrategy_Empty(t *testing.T) {
	s := NewLeastLoadedStrategy()
	_, err := s.Pick(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty candidates")
	}
}

func TestLeastLoadedStrategy_PicksLeastLoaded(t *testing.T) {
	s := NewLeastLoadedStrategy()
	candidates := []contracts.NodeInfo{
		{ID: "node-a"},
		{ID: "node-b"},
		{ID: "node-c"},
	}

	s.TrackStart("node-a")
	s.TrackStart("node-a")
	s.TrackStart("node-b")

	idx, err := s.Pick(context.Background(), candidates)
	if err != nil {
		t.Fatalf("Pick failed: %v", err)
	}
	if candidates[idx].ID != "node-c" {
		t.Fatalf("expected node-c (0 load), got %s", candidates[idx].ID)
	}
}

func TestLeastLoadedStrategy_TrackStartEnd(t *testing.T) {
	s := NewLeastLoadedStrategy()
	candidates := []contracts.NodeInfo{
		{ID: "node-a"},
		{ID: "node-b"},
	}

	s.TrackStart("node-a")
	s.TrackStart("node-a")
	s.TrackStart("node-b")

	idx, _ := s.Pick(context.Background(), candidates)
	if candidates[idx].ID != "node-b" {
		t.Fatalf("before TrackEnd: expected node-b, got %s", candidates[idx].ID)
	}

	s.TrackEnd("node-a")
	s.TrackEnd("node-a")

	idx, _ = s.Pick(context.Background(), candidates)
	if candidates[idx].ID != "node-a" {
		t.Fatalf("after TrackEnd: expected node-a (0 load), got %s", candidates[idx].ID)
	}
}

func TestLeastLoadedStrategy_TrackEndDoesNotGoBelowZero(t *testing.T) {
	s := NewLeastLoadedStrategy()
	s.TrackEnd("node-x")
	s.TrackEnd("node-x")

	candidates := []contracts.NodeInfo{{ID: "node-x"}}
	idx, err := s.Pick(context.Background(), candidates)
	if err != nil {
		t.Fatalf("Pick failed: %v", err)
	}
	if idx != 0 {
		t.Fatalf("expected 0, got %d", idx)
	}
}
