package report

import (
	"testing"
)

func BenchmarkRenderText(b *testing.B) {
	r := sampleDiffReport()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RenderText(r, Findings{})
	}
}

func BenchmarkRenderJSON(b *testing.B) {
	r := sampleDiffReport()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RenderJSON(r, Findings{})
	}
}
