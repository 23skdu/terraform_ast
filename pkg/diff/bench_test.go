package diff

import (
	"strconv"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
)

func BenchmarkCompare(b *testing.B) {
	oldMod := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{"x": {Name: "x", Type: "string"}}}
	newMod := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{"x": {Name: "x", Type: "string"}, "y": {Name: "y", Type: "string", HasDefault: true}}}
	for b.Loop() {
		Compare(oldMod, newMod, "1.0.0", "1.1.0")
	}
}

func BenchmarkCompareLarge(b *testing.B) {
	oldVars := make(map[string]ast.VariableDef, 1000)
	newVars := make(map[string]ast.VariableDef, 1001)
	for i := 0; i < 1000; i++ {
		name := "var" + strconv.Itoa(i)
		oldVars[name] = ast.VariableDef{Name: name, Type: "string"}
		newVars[name] = ast.VariableDef{Name: name, Type: "string"}
	}
	newVars["new_var"] = ast.VariableDef{Name: "new_var", Type: "string", HasDefault: true}
	oldMod := &ast.ModuleInfo{Variables: oldVars}
	newMod := &ast.ModuleInfo{Variables: newVars}
	for b.Loop() {
		Compare(oldMod, newMod, "1.0.0", "1.1.0")
	}
}
