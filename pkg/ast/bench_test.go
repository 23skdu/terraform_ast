package ast

import (
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkInspectModule(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 30; i++ {
		_ = os.WriteFile(filepath.Join(dir, "f"+string(rune('a'+i))+".tf"), []byte(`variable "x" { type = string }`), 0644)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Inspect(dir)
	}
}
