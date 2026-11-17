package modulegraph

import (
	"strings"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
)

func TestBuildNil(t *testing.T) {
	g := Build(nil)
	if g == nil || len(g.Nodes) != 0 || len(g.Edges) != 0 {
		t.Fatalf("expected empty graph")
	}
}

func TestBuildAndRender(t *testing.T) {
	info := &ast.ModuleInfo{
		ModuleCalls: map[string]ast.ModuleCallDef{
			"gke": {Name: "gke", Source: "terraform-google-modules/kubernetes-engine/google", InputExpressions: map[string]string{"network": "module.vpc.network_name"}},
			"vpc": {Name: "vpc", Source: "terraform-google-modules/network/google"},
		},
		ManagedResources: map[string]ast.ResourceDef{
			"google_compute_instance.vm": {Type: "google_compute_instance", Name: "vm"},
		},
	}
	g := Build(info)
	if len(g.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(g.Nodes))
	}
	if len(g.Edges) != 1 || g.Edges[0].From != "module.gke" || g.Edges[0].To != "module.vpc" {
		t.Fatalf("unexpected edges: %+v", g.Edges)
	}
	dot := RenderDOT(g)
	if !strings.Contains(dot, "digraph modulegraph") || !strings.Contains(dot, "module.gke") {
		t.Fatalf("bad dot: %s", dot)
	}
}

func TestBuildSelfAndDuplicateEdges(t *testing.T) {
	info := &ast.ModuleInfo{
		ModuleCalls: map[string]ast.ModuleCallDef{
			"gke": {Name: "gke", Source: "src", InputExpressions: map[string]string{
				"self": "module.gke.x",
				"vpc":  "module.vpc.network",
				"vpc2": "module.vpc.network",
			}},
			"vpc": {Name: "vpc", Source: "src2"},
		},
	}
	g := Build(info)
	if len(g.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(g.Edges))
	}
}

func TestBuildEmptyModuleInfo(t *testing.T) {
	g := Build(&ast.ModuleInfo{})
	if len(g.Nodes) != 0 || len(g.Edges) != 0 {
		t.Fatalf("expected empty")
	}
}

func TestTopologicalOrderNil(t *testing.T) {
	order, err := TopologicalOrder(nil)
	if err != nil || order != nil {
		t.Fatalf("expected nil, nil; got %v, %v", order, err)
	}
}

func TestTopologicalOrderLinear(t *testing.T) {
	g := &Graph{
		Nodes: []Node{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Edges: []Edge{{From: "a", To: "b"}, {From: "b", To: "c"}},
	}
	order, err := TopologicalOrder(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 || order[0] != "c" || order[1] != "b" || order[2] != "a" {
		t.Fatalf("unexpected order: %v", order)
	}
}

func TestTopologicalOrderCycle(t *testing.T) {
	g := &Graph{
		Nodes: []Node{{ID: "a"}, {ID: "b"}},
		Edges: []Edge{{From: "a", To: "b"}, {From: "b", To: "a"}},
	}
	_, err := TopologicalOrder(g)
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestTopologicalOrderDisconnectedAndDuplicates(t *testing.T) {
	g := &Graph{
		Nodes: []Node{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Edges: []Edge{{From: "a", To: "b"}, {From: "a", To: "b"}, {From: "x", To: "y"}},
	}
	order, err := TopologicalOrder(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3, got %d: %v", len(order), order)
	}
}

func TestTopologicalOrderEmpty(t *testing.T) {
	g := &Graph{}
	order, err := TopologicalOrder(g)
	if err != nil || len(order) != 0 {
		t.Fatalf("expected empty, got %v, %v", order, err)
	}
}

func TestTopologicalOrderUnknownEndpoints(t *testing.T) {
	g := &Graph{
		Nodes: []Node{{ID: "a"}},
		Edges: []Edge{{From: "a", To: "missing"}, {From: "missing", To: "a"}},
	}
	order, err := TopologicalOrder(g)
	if err != nil || len(order) != 1 || order[0] != "a" {
		t.Fatalf("unexpected: %v, %v", order, err)
	}
}

func TestRenderDOTNilCoverage2(t *testing.T) {
	if RenderDOT(nil) == "" {
		t.Fatal("expected non-empty")
	}
}
