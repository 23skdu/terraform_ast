package changelog

import (
	"context"
	"testing"
	"strings"
	"github.com/23skdu/terraform_ast/pkg/planner"
	"github.com/23skdu/terraform_ast/pkg/registry"
)

func TestAggregate(t *testing.T) {
	rc := registry.NewClient(nil)
	p := planner.NewUpgradePlanner(rc)
	a := NewAggregator(p)

	out, err := a.Aggregate(context.Background(), "hashicorp/consul/aws", "0.1.0", "0.2.0")
	if err != nil {
		t.Fatalf("Aggregate failed: %v", err)
	}
	if !strings.Contains(out, "Changelog for") {
		t.Fatalf("missing changelog string")
	}

	out2, err := a.Aggregate(context.Background(), "local/module", "0.1.0", "0.1.0")
	if err != nil {
		t.Fatalf("Aggregate failed: %v", err)
	}
	if !strings.Contains(out2, "No version changes detected") {
		t.Fatalf("missing expected string")
	}
	
	_, err = a.Aggregate(context.Background(), "hashicorp/consul/aws", "invalid", "0.2.0")
	if err == nil {
		t.Fatalf("expected plan failure error")
	}
}

func TestNewAggregator(t *testing.T) {
	a := NewAggregator(nil)
	if a.Planner == nil {
		t.Fatalf("planner should not be nil")
	}
}
