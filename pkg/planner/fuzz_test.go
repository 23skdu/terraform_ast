package planner

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
)

func FuzzAnalyzeUpgradePath(f *testing.F) {
	f.Add("v1.0.0", "v1.1.0", "v2.0.0", "var_a", "string", "string")
	f.Add("v1.0.0", "v2.0.0", "v3.0.0", "cluster_name", "string", "list(string)")
	f.Add("v2.0.0", "v1.0.0", "v3.0.0", "invalid_order", "string", "string")
	f.Add("v1.0.0", "v1.0.0", "v1.0.0", "same_version", "string", "string")
	f.Add("invalid", "v1.0.0", "v2.0.0", "bad_semver", "string", "string")

	f.Fuzz(func(t *testing.T, v1, v2, v3, varName, type1, type2 string) {
		m1 := &ast.ModuleInfo{
			Path:      "mod/v1",
			Variables: make(map[string]ast.VariableDef),
		}
		m2 := &ast.ModuleInfo{
			Path:      "mod/v2",
			Variables: make(map[string]ast.VariableDef),
		}
		m3 := &ast.ModuleInfo{
			Path:      "mod/v3",
			Variables: make(map[string]ast.VariableDef),
		}

		if varName != "" {
			m1.Variables[varName] = ast.VariableDef{Name: varName, Type: type1}
			m2.Variables[varName] = ast.VariableDef{Name: varName, Type: type2}
			m3.Variables[varName] = ast.VariableDef{Name: varName, Type: type2}
		}

		snapshots := []VersionSnapshot{
			{Version: v1, Module: m1},
			{Version: v2, Module: m2},
			{Version: v3, Module: m3},
		}

		report, err := AnalyzeUpgradePath(snapshots)
		if err == nil {
			if report == nil {
				t.Fatal("expected non-nil report on nil error")
			}
			if len(report.Steps) != 2 {
				t.Fatalf("expected 2 steps, got %d", len(report.Steps))
			}
			if report.FromVersion != v1 || report.TargetVersion != v3 {
				t.Fatalf("boundary version mismatch: %s -> %s", report.FromVersion, report.TargetVersion)
			}
		}
	})
}
