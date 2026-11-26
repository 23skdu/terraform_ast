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

func BenchmarkCompareStream(b *testing.B) {
	oldMod := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{"x": {Name: "x", Type: "string"}}}
	newMod := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{"x": {Name: "x", Type: "number"}}}
	for b.Loop() {
		CollectDiff(CompareStream(oldMod, newMod, "1.0.0", "2.0.0"))
	}
}

func BenchmarkCompareSmall(b *testing.B) {
	benchmarkCompareVars(b, 5)
}

func BenchmarkCompareMedium(b *testing.B) {
	benchmarkCompareVars(b, 100)
}

func BenchmarkCompareLarge(b *testing.B) {
	benchmarkCompareVars(b, 1000)
}

func benchmarkCompareVars(b *testing.B, n int) {
	oldVars := make(map[string]ast.VariableDef, n)
	newVars := make(map[string]ast.VariableDef, n+1)
	for i := 0; i < n; i++ {
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
