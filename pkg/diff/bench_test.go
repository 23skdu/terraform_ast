package diff

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
)

func BenchmarkCompare(b *testing.B) {
	oldMod := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{"x": {Name: "x", Type: "string"}}}
	newMod := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{"x": {Name: "x", Type: "string"}, "y": {Name: "y", Type: "string", HasDefault: true}}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Compare(oldMod, newMod, "1.0.0", "1.1.0")
	}
}
