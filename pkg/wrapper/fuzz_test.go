package wrapper

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/diff"
)

func FuzzAnalyzeWrapper(f *testing.F) {
	f.Add("v2.0.0", "v1.0.0", "upstream_mod", "cluster_name", "string", "string", true, false)
	f.Add("v1.1.0", "v1.0.0", "my_module", "zones", "list(string)", "set(string)", false, false)
	f.Add("", "", "", "", "", "", false, false)

	f.Fuzz(func(t *testing.T, targetVer, pinnedVer, callName, varName, varType1, varType2 string, req1, req2 bool) {
		wrapMod := &ast.ModuleInfo{
			Path:                    "wrapper",
			Variables:               make(map[string]ast.VariableDef),
			Outputs:                 make(map[string]ast.OutputDef),
			ModuleCalls:             make(map[string]ast.ModuleCallDef),
			WrapperOutputReferences: make(map[string][]ast.OutputReference),
		}

		targetUpstream := &ast.ModuleInfo{
			Path:      "upstream",
			Variables: make(map[string]ast.VariableDef),
			Outputs:   make(map[string]ast.OutputDef),
		}

		pinnedUpstream := &ast.ModuleInfo{
			Path:      "upstream",
			Variables: make(map[string]ast.VariableDef),
			Outputs:   make(map[string]ast.OutputDef),
		}

		if callName != "" {
			var passed []string
			if varName != "" {
				passed = append(passed, varName)
			}
			wrapMod.ModuleCalls[callName] = ast.ModuleCallDef{
				Name:         callName,
				Source:       "../upstream",
				PassedInputs: passed,
			}
		}

		if varName != "" {
			pinnedUpstream.Variables[varName] = ast.VariableDef{
				Name:     varName,
				Type:     varType1,
				Required: req1,
			}
			targetUpstream.Variables[varName] = ast.VariableDef{
				Name:     varName,
				Type:     varType2,
				Required: req2,
			}
		}

		report := AnalyzeWrapper(wrapMod, targetUpstream, pinnedUpstream, targetVer, pinnedVer)
		if report == nil {
			t.Fatal("expected non-nil report")
		}

		wrapperBreaking := 0
		for _, iss := range report.Issues {
			if iss.Severity == diff.SeverityBreaking {
				wrapperBreaking++
			}
		}
		if report.TotalBreaking != wrapperBreaking+report.UpstreamBreaking {
			t.Fatalf("breaking count mismatch: %d vs %d", report.TotalBreaking, wrapperBreaking+report.UpstreamBreaking)
		}
	})
}
