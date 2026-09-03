package registry

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// GraphNode is a module in the dependency graph.
type GraphNode struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	State        string `json:"state"`
	Healthy      bool   `json:"healthy"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// GraphEdge represents a dependency: From must be running before To can start.
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// DependencyGraph is a snapshot of module dependencies suitable for
// visualization and impact analysis.
type DependencyGraph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// BuildDependencyGraph constructs a full dependency graph from the registry.
func (r *Registry) BuildDependencyGraph() *DependencyGraph {
	entries := r.List()
	nodes := make([]GraphNode, 0, len(entries))
	edgeSet := make(map[string]struct{})

	for _, e := range entries {
		nodes = append(nodes, GraphNode{
			ID:           e.Info.ID,
			Name:         e.Info.Name,
			Version:      e.Info.Version,
			State:        string(e.State),
			Healthy:      e.Health == nil,
			Capabilities: append([]string(nil), e.Info.Capabilities...),
		})
		for _, dep := range e.Deps {
			key := dep + "->" + e.Info.ID
			edgeSet[key] = struct{}{}
		}
	}

	edges := make([]GraphEdge, 0, len(edgeSet))
	for key := range edgeSet {
		parts := strings.Split(key, "->")
		if len(parts) == 2 {
			edges = append(edges, GraphEdge{From: parts[0], To: parts[1]})
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return &DependencyGraph{Nodes: nodes, Edges: edges}
}

// DOT renders the graph in Graphviz DOT format.
func (g *DependencyGraph) DOT() string {
	if g == nil {
		return "digraph modules {}\n"
	}
	var b strings.Builder
	b.WriteString("digraph modules {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=rounded];\n")
	for _, n := range g.Nodes {
		label := n.ID
		if n.Version != "" {
			label += "\\n" + n.Version
		}
		color := "black"
		if !n.Healthy || n.State == string(contracts.ModuleStateDegraded) {
			color = "red"
		} else if n.State == string(contracts.ModuleStateRunning) {
			color = "green"
		}
		fmt.Fprintf(&b, "  %q [label=%q, color=%s];\n", n.ID, label, color)
	}
	for _, e := range g.Edges {
		fmt.Fprintf(&b, "  %q -> %q;\n", e.From, e.To)
	}
	b.WriteString("}\n")
	return b.String()
}

// Mermaid renders the graph as a Mermaid flowchart.
func (g *DependencyGraph) Mermaid() string {
	if g == nil {
		return "flowchart LR\n"
	}
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	for _, e := range g.Edges {
		fmt.Fprintf(&b, "  %s --> %s\n", sanitizeMermaidID(e.From), sanitizeMermaidID(e.To))
	}
	for _, n := range g.Nodes {
		style := ""
		if !n.Healthy || n.State == string(contracts.ModuleStateDegraded) {
			style = ":::unhealthy"
		}
		fmt.Fprintf(&b, "  %s[%q]%s\n", sanitizeMermaidID(n.ID), n.ID, style)
	}
	b.WriteString("  classDef unhealthy fill:#fdd,stroke:#c00\n")
	return b.String()
}

func sanitizeMermaidID(id string) string {
	return strings.NewReplacer("-", "_", ".", "_", ":", "_").Replace(id)
}
