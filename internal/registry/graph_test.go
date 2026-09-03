package registry

import (
	"context"
	"strings"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

type graphModule struct {
	info contracts.ModuleInfo
}

func (m *graphModule) Info() contracts.ModuleInfo       { return m.info }
func (m *graphModule) Init(context.Context) error       { return nil }
func (m *graphModule) Start(context.Context) error      { return nil }
func (m *graphModule) Stop(context.Context) error       { return nil }
func (m *graphModule) Health(context.Context) error     { return nil }

func TestBuildDependencyGraph(t *testing.T) {
	reg := New()
	_ = reg.Register(&graphModule{info: contracts.ModuleInfo{ID: "base", Name: "base"}}, nil)
	_ = reg.Register(&graphModule{info: contracts.ModuleInfo{ID: "app", Name: "app"}}, []string{"base"})

	graph := reg.BuildDependencyGraph()
	if len(graph.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(graph.Nodes))
	}
	if len(graph.Edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(graph.Edges))
	}
	if graph.Edges[0].From != "base" || graph.Edges[0].To != "app" {
		t.Fatalf("edge = %+v", graph.Edges[0])
	}

	dot := graph.DOT()
	if !strings.Contains(dot, `"base" -> "app"`) {
		t.Fatalf("dot missing edge: %s", dot)
	}
	mermaid := graph.Mermaid()
	if !strings.Contains(mermaid, "base --> app") {
		t.Fatalf("mermaid missing edge: %s", mermaid)
	}
}
