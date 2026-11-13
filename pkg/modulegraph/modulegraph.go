// Package modulegraph builds and renders a dependency graph of the modules and
// resources declared in a Terraform module.
package modulegraph

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/ast"
)

// Node represents a node in the module dependency graph.
type Node struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Source string `json:"source,omitempty"`
}

// Edge represents a directed dependency between two nodes.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Graph is a module dependency graph.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

var moduleRefRe = regexp.MustCompile(`module\.([A-Za-z0-9_]+)`)

// Build constructs a dependency graph from a ModuleInfo.
func Build(info *ast.ModuleInfo) *Graph {
	g := &Graph{}
	if info == nil {
		return g
	}

	// Module call nodes.
	moduleNames := make([]string, 0, len(info.ModuleCalls))
	for name := range info.ModuleCalls {
		moduleNames = append(moduleNames, name)
	}
	sort.Strings(moduleNames)
	for _, name := range moduleNames {
		mc := info.ModuleCalls[name]
		g.Nodes = append(g.Nodes, Node{ID: "module." + name, Kind: "module", Source: mc.Source})
	}

	// Resource nodes (managed) are leaves/sources.
	resourceNames := make([]string, 0, len(info.ManagedResources))
	for name := range info.ManagedResources {
		resourceNames = append(resourceNames, name)
	}
	sort.Strings(resourceNames)
	for _, name := range resourceNames {
		g.Nodes = append(g.Nodes, Node{ID: name, Kind: "resource"})
	}

	// Edges: module inputs referencing other modules.
	for _, name := range moduleNames {
		mc := info.ModuleCalls[name]
		for _, expr := range mc.InputExpressions {
			for _, m := range moduleRefRe.FindAllStringSubmatch(expr, -1) {
				dep := m[1]
				if dep == name {
					continue
				}
				g.Edges = append(g.Edges, Edge{From: "module." + name, To: "module." + dep})
			}
		}
		// Also consider passed inputs list (names only) - no edges derivable.
		_ = mc.PassedInputs
	}

	// Edges: resources referencing modules via expressions are not available here;
	// instead, resources are standalone nodes.

	// Deduplicate edges.
	seen := make(map[string]struct{})
	uniq := g.Edges[:0]
	for _, e := range g.Edges {
		key := e.From + "->" + e.To
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		uniq = append(uniq, e)
	}
	g.Edges = uniq

	return g
}

// RenderDOT renders the graph in Graphviz DOT format.
func RenderDOT(g *Graph) string {
	if g == nil {
		return "digraph modulegraph {\n}\n"
	}
	var b strings.Builder
	b.WriteString("digraph modulegraph {\n")
	for _, n := range g.Nodes {
		fmt.Fprintf(&b, "  %q [label=%q];\n", n.ID, n.ID)
	}
	for _, e := range g.Edges {
		fmt.Fprintf(&b, "  %q -> %q;\n", e.From, e.To)
	}
	b.WriteString("}\n")
	return b.String()
}
