package ast

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// BenchmarkGateCalibration is a deterministic CPU loop used by the benchmark gate to
// normalize absolute ns/op values across runners with different CPU speeds. Do not change
// the inner work without regenerating the committed baseline.
func BenchmarkGateCalibration(b *testing.B) {
	var sink uint64
	for b.Loop() {
		x := uint64(1469598103934665603)
		for j := 0; j < 1000; j++ {
			x = (x ^ uint64(j)) * 1099511628211
		}
		sink = x
	}
	_ = sink
}

func BenchmarkInspectModule(b *testing.B) {
	benchmarkInspectModule(b, 30)
}

func BenchmarkInspectModuleSmall(b *testing.B) {
	benchmarkInspectModule(b, 5)
}

func BenchmarkInspectModuleMedium(b *testing.B) {
	benchmarkInspectModule(b, 50)
}

func BenchmarkInspectModuleLarge(b *testing.B) {
	benchmarkInspectModule(b, 200)
}

func benchmarkInspectModule(b *testing.B, n int) {
	dir := b.TempDir()
	for i := 0; i < n; i++ {
		_ = os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)+".tf"), []byte(`variable "x" { type = string }`), 0644)
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
