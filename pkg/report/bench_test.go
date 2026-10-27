package report

import (
	"testing"
)

func BenchmarkRenderText(b *testing.B) {
	r := sampleDiffReport()
	for b.Loop() {
		RenderText(r, Findings{})
	}
}

func BenchmarkRenderJSON(b *testing.B) {
	r := sampleDiffReport()
	for b.Loop() {
		RenderJSON(r, Findings{})
	}
}
