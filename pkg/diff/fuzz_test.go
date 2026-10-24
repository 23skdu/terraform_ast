package diff

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
)

func FuzzCompare(f *testing.F) {
	f.Add("v1.0.0", "v1.1.0", "var_a", "string", "string", true, false, "out_a", "string", "string")
	f.Add("v1.0.0", "v2.0.0", "cluster_name", "string", "list(string)", false, true, "endpoint", "string", "")
	f.Add("v2.0.0", "v1.0.0", "tags", "map(string)", "map(string)", false, false, "arn", "", "")
	f.Add("invalid", "v1.0.0", "foo", "number", "string", true, true, "bar", "bool", "bool")
	f.Add("", "", "", "", "", false, false, "", "", "")

	f.Fuzz(func(t *testing.T, oldVer, newVer, varName, oldVarType, newVarType string, oldVarReq, newVarReq bool, outName, oldOutType, newOutType string) {
		oldMod := &ast.ModuleInfo{
			Path:              "old/module",
			Variables:         make(map[string]ast.VariableDef),
			Outputs:           make(map[string]ast.OutputDef),
			RequiredProviders: make(map[string]ast.ProviderRequirement),
		}
		newMod := &ast.ModuleInfo{
			Path:              "new/module",
			Variables:         make(map[string]ast.VariableDef),
			Outputs:           make(map[string]ast.OutputDef),
			RequiredProviders: make(map[string]ast.ProviderRequirement),
		}

		if varName != "" {
			oldMod.Variables[varName] = ast.VariableDef{
				Name:        varName,
				Type:        oldVarType,
				Required:    oldVarReq,
				Description: "old description",
			}
			newMod.Variables[varName] = ast.VariableDef{
				Name:        varName,
				Type:        newVarType,
				Required:    newVarReq,
				Description: "new description",
			}
		}

		if outName != "" {
			oldMod.Outputs[outName] = ast.OutputDef{
				Name:        outName,
				Description: "old output",
			}
			newMod.Outputs[outName] = ast.OutputDef{
				Name:        outName,
				Description: "new output",
			}
		}

		report := Compare(oldMod, newMod, oldVer, newVer)
		if report == nil {
			t.Fatal("expected non-nil report")
		}

		if report.TotalBreaking != len(report.BreakingChanges) {
			t.Fatalf("breaking count mismatch: %d vs %d", report.TotalBreaking, len(report.BreakingChanges))
		}
		if report.TotalWarnings != len(report.Warnings) {
			t.Fatalf("warnings count mismatch: %d vs %d", report.TotalWarnings, len(report.Warnings))
		}
		if report.TotalNewFeatures != len(report.NewFeatures) {
			t.Fatalf("features count mismatch: %d vs %d", report.TotalNewFeatures, len(report.NewFeatures))
		}

		switch report.Verdict {
		case "SAFE", "WARNINGS_ONLY", "BREAKING_CHANGES", "DOWNGRADE":
		default:
			t.Fatalf("unexpected verdict: %s", report.Verdict)
		}
	})
}
