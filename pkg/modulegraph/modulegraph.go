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

// TopologicalOrder returns the node IDs of g in dependency order, with
// dependencies emitted before the nodes that depend on them. It returns an
// error when the graph contains a cycle.
func TopologicalOrder(g *Graph) ([]string, error) {
	if g == nil {
		return nil, nil
	}
	nodes := make(map[string]struct{}, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes[n.ID] = struct{}{}
	}
	// Edges run From -> To where From depends on To, so To must come first.
	inDegree := make(map[string]int, len(nodes))
	dependents := make(map[string][]string, len(nodes))
	for node := range nodes {
		inDegree[node] = 0
	}
	for _, e := range g.Edges {
		if _, ok := nodes[e.From]; !ok {
			continue
		}
		if _, ok := nodes[e.To]; !ok {
			continue
		}
		// Avoid double counting duplicate edges.
		if !contains(dependents[e.To], e.From) {
			dependents[e.To] = append(dependents[e.To], e.From)
			inDegree[e.From]++
		}
	}

	// Kahn's algorithm with a sorted ready set for determinism.
	var ready []string
	for node := range nodes {
		if inDegree[node] == 0 {
			ready = append(ready, node)
		}
	}
	sort.Strings(ready)

	var order []string
	for len(ready) > 0 {
		current := ready[0]
		ready = ready[1:]
		order = append(order, current)
		next := append([]string(nil), dependents[current]...)
		sort.Strings(next)
		for _, dep := range next {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				ready = append(ready, dep)
				sort.Strings(ready)
			}
		}
	}

	if len(order) != len(nodes) {
		return nil, fmt.Errorf("module graph contains a cycle")
	}
	return order, nil
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
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
