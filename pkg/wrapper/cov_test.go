package wrapper

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
)

func TestCoverage_AnalyzeWrapperLocals(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		Variables:   map[string]ast.VariableDef{"custom_region": {Name: "custom_region", Type: "string"}},
		Locals:      map[string]string{"my_local": "var.custom_region", "nested": "local.my_local", "self": "local.self", "bad": "!!!", "bare": "var", "modref": "module.m.out", "missingref": "local.missing"},
		ModuleCalls: map[string]ast.ModuleCallDef{"m": {Name: "m", PassedInputs: []string{"region"}, InputExpressions: map[string]string{"region": "var + local.missingref + local.nested + local.missing + local.self + local.bad + local.bare + local.modref + module.m.x"}}},
	}
	targetUpstream := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{"region": {Name: "region", Type: "string"}}}
	_ = AnalyzeWrapper(wrapperMod, targetUpstream, nil, "1.0.0", "0.9.0", nil)
	// parse error fallback
	wrapperMod2 := &ast.ModuleInfo{
		Variables:   map[string]ast.VariableDef{"x": {Name: "x", Type: "string"}},
		ModuleCalls: map[string]ast.ModuleCallDef{"m": {Name: "m", PassedInputs: []string{"region"}, InputExpressions: map[string]string{"region": "var.x + !!!"}}},
	}
	targetUpstream2 := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{"region": {Name: "region", Type: "string"}}}
	_ = AnalyzeWrapper(wrapperMod2, targetUpstream2, nil, "1.0.0", "0.9.0", nil)
}
