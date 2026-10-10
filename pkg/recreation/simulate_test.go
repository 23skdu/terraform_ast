package recreation

import (
	"context"
	"testing"
)

func TestSimulator(t *testing.T) {
	s := NewSimulator("/tmp/workspace")
	if s.WorkspaceDir != "/tmp/workspace" {
		t.Fatalf("expected workspace")
	}

	res, err := s.SimulatePlan(context.Background(), "module")
	if err != nil {
		t.Fatalf("expected no error")
	}
	if len(res) != 0 {
		t.Fatalf("expected empty result")
	}
}
