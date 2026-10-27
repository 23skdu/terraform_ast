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
	for b.Loop() {
		_, _ = Inspect(dir)
	}
}

func BenchmarkInspectSource(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 30; i++ {
		_ = os.WriteFile(filepath.Join(dir, "f"+string(rune('a'+i))+".tf"), []byte(`variable "x" { type = string }`), 0644)
	}
	file := filepath.Join(dir, "fa.tf")
	content := `variable "x" { type = number }`
	for b.Loop() {
		_, _ = InspectSource(dir, file, content)
	}
}
