package report

import (
	"strconv"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/diff"
)

func benchmarkSizedReport(n int) *diff.DiffReport {
	r := sampleDiffReport()
	for i := 0; i < n; i++ {
		name := "finding" + strconv.Itoa(i)
		r.BreakingChanges = append(r.BreakingChanges, diff.DifferenceItem{
			Category: "Variable", Name: name, Severity: diff.SeverityBreaking, Description: name,
		})
		r.Warnings = append(r.Warnings, diff.DifferenceItem{
			Category: "VariableType", Name: name, Severity: diff.SeverityWarning, Description: name,
		})
		r.NewFeatures = append(r.NewFeatures, diff.DifferenceItem{
			Category: "Variable", Name: name, Severity: diff.SeverityNewFeature, Description: name,
		})
	}
	r.TotalBreaking = len(r.BreakingChanges)
	r.TotalWarnings = len(r.Warnings)
	r.TotalNewFeatures = len(r.NewFeatures)
	return r
}

// Baseline-compatible names retained so the committed gate does not see a vanished benchmark.
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

func BenchmarkRenderTextSmall(b *testing.B) {
	r := benchmarkSizedReport(5)
	for b.Loop() {
		RenderText(r, Findings{})
	}
}

func BenchmarkRenderTextMedium(b *testing.B) {
	r := benchmarkSizedReport(100)
	for b.Loop() {
		RenderText(r, Findings{})
	}
}

func BenchmarkRenderTextLarge(b *testing.B) {
	r := benchmarkSizedReport(1000)
	for b.Loop() {
		RenderText(r, Findings{})
	}
}

func BenchmarkRenderJSONSmall(b *testing.B) {
	r := benchmarkSizedReport(5)
	for b.Loop() {
		RenderJSON(r, Findings{})
	}
}

func BenchmarkRenderJSONMedium(b *testing.B) {
	r := benchmarkSizedReport(100)
	for b.Loop() {
		RenderJSON(r, Findings{})
	}
}

func BenchmarkRenderJSONLarge(b *testing.B) {
	r := benchmarkSizedReport(1000)
	for b.Loop() {
		RenderJSON(r, Findings{})
	}
}
