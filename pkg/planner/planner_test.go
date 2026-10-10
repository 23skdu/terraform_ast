package planner

import (
	"context"
	"testing"
)

func TestPlanUpgrade(t *testing.T) {
	p := NewUpgradePlanner(nil)

	plan, err := p.PlanUpgrade(context.Background(), "hashicorp/consul/aws", "0.1.0", "0.2.0")
	if err != nil {
		t.Fatalf("PlanUpgrade failed: %v", err)
	}
	if len(plan) == 0 {
		t.Fatalf("expected plan")
	}

	plan2, err := p.PlanUpgrade(context.Background(), "local/module", "0.1.0", "0.2.0")
	if err != nil {
		t.Fatalf("PlanUpgrade failed: %v", err)
	}
	if len(plan2) != 2 {
		t.Fatalf("expected plan of length 2")
	}

	_, err = p.PlanUpgrade(context.Background(), "local/module", "invalid", "0.2.0")
	if err == nil {
		t.Fatalf("expected error")
	}

	_, err = p.PlanUpgrade(context.Background(), "local/module", "0.1.0", "invalid")
	if err == nil {
		t.Fatalf("expected error")
	}

	plan3, err := p.PlanUpgrade(context.Background(), "local/module", "0.3.0", "0.2.0")
	if err != nil {
		t.Fatalf("PlanUpgrade failed: %v", err)
	}
	if plan3[0] != "0.3.0" {
		t.Fatalf("expected fromVersion")
	}
	
	// Trigger ListVersions failure
	_, err = p.PlanUpgrade(context.Background(), "hashicorp/invalid/aws", "0.1.0", "0.2.0")
	if err == nil {
		t.Fatalf("expected ListVersions to fail")
	}
}
