package report

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleDiffReport() *diff.DiffReport {
	return &diff.DiffReport{
		ModuleName:      "terraform-google-modules/kubernetes-engine/google",
		OldVersion:      "v29.0.0",
		NewVersion:      "v30.0.0",
		BumpType:        "MAJOR",
		Verdict:         "BREAKING_CHANGES",
		Summary:         "Detected 1 breaking change(s)",
		IsDowngrade:     false,
		CanSafelyUpdate: false,
		BreakingChanges: []diff.DifferenceItem{
			{
				Category:    "Variable",
				Name:        "ip_range_pods",
				Severity:    diff.SeverityBreaking,
				Description: "Input was removed",
				Remediation: "Remove from wrapper",
			},
		},
		NewFeatures: []diff.DifferenceItem{
			{
				Category:    "Variable",
				Name:        "enable_cost_allocation",
				Severity:    diff.SeverityNewFeature,
				Description: "New optional input",
			},
		},
		Warnings: []diff.DifferenceItem{
			{
				Category:    "Variable",
				Name:        "zones",
				Severity:    diff.SeverityWarning,
				Description: "Widened to set(string)",
				Remediation: "Ensure set validity",
			},
		},
	}
}

func sampleWrapperReport() *wrapper.WrapperCompatibilityReport {
	return &wrapper.WrapperCompatibilityReport{
		WrapperPath:      "/repos/gke-wrapper",
		TargetModuleName: "gke",
		TargetVersion:    "v30.0.0",
		RecommendedPin:   `version = "30.0.0"`,
		Verdict:          "BREAKING_CHANGES_DETECTED",
		Summary:          "Wrapper has 1 breaking issue(s)",
		TotalBreaking:    1,
		Issues: []wrapper.WrapperIssue{
			{
				Category:    "PassedRemovedInput",
				Target:      "ip_range_pods",
				Severity:    diff.SeverityBreaking,
				Description: "Wrapper passes removed argument",
				Remediation: "Remove argument from module block",
			},
		},
		NewFeatureAdoptions: []wrapper.WrapperIssue{
			{
				Category:    "OptionalVariableAvailable",
				Target:      "enable_cost_allocation",
				Severity:    diff.SeverityNewFeature,
				Description: "New optional feature available",
			},
		},
	}
}

func TestRenderText(t *testing.T) {
	assert.Equal(t, "", RenderText(nil))

	r := sampleDiffReport()
	out := RenderText(r)
	assert.Contains(t, out, "TERRAFORM AST COMPATIBILITY DIFF REPORT")
	assert.Contains(t, out, "ip_range_pods")
	assert.Contains(t, out, "enable_cost_allocation")
	assert.Contains(t, out, "zones")
}

func TestRenderMarkdown(t *testing.T) {
	assert.Equal(t, "", RenderMarkdown(nil))

	// Caution alert (breaking)
	r := sampleDiffReport()
	out := RenderMarkdown(r)
	assert.Contains(t, out, "# Terraform AST Compatibility Diff Report")
	assert.Contains(t, out, "[!CAUTION]")
	assert.Contains(t, out, "ip_range_pods")
	assert.Contains(t, out, "enable_cost_allocation")
	assert.Contains(t, out, "zones")

	// Warning alert
	rWarning := sampleDiffReport()
	rWarning.BreakingChanges = nil
	outWarning := RenderMarkdown(rWarning)
	assert.Contains(t, outWarning, "[!WARNING]")

	// Note alert
	rSafe := sampleDiffReport()
	rSafe.BreakingChanges = nil
	rSafe.Warnings = nil
	outSafe := RenderMarkdown(rSafe)
	assert.Contains(t, outSafe, "[!NOTE]")
}

func TestConfidenceScoresAreRendered(t *testing.T) {
	report := sampleDiffReport()
	report.BreakingChanges[0].ConfidenceScore = 9
	report.NewFeatures[0].ConfidenceScore = 9
	report.Warnings[0].ConfidenceScore = 7

	text := RenderText(report)
	assert.Contains(t, text, "confidence: 9/10")
	assert.Contains(t, text, "confidence: 7/10")
	markdown := RenderMarkdown(report)
	assert.Contains(t, markdown, "Confidence")
	assert.Contains(t, markdown, "9/10")

	jsonBytes, err := RenderJSON(report)
	require.NoError(t, err)
	assert.Contains(t, string(jsonBytes), `"confidence_score": 9`)
	sarifBytes, err := RenderSARIF(report)
	require.NoError(t, err)
	assert.Contains(t, string(sarifBytes), "Confidence: 9/10")
}

func TestRenderJSON(t *testing.T) {
	r := sampleDiffReport()
	data, err := RenderJSON(r)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"ip_range_pods"`)
}

func TestRenderWrapperText(t *testing.T) {
	assert.Equal(t, "", RenderWrapperText(nil))

	w := sampleWrapperReport()
	out := RenderWrapperText(w)
	assert.Contains(t, out, "TERRAFORM WRAPPER COMPATIBILITY & PIN AUDIT")
	assert.Contains(t, out, "ip_range_pods")
	assert.Contains(t, out, "enable_cost_allocation")
}

func TestRenderWrapperMarkdown(t *testing.T) {
	assert.Equal(t, "", RenderWrapperMarkdown(nil))

	// Caution alert
	w := sampleWrapperReport()
	out := RenderWrapperMarkdown(w)
	assert.Contains(t, out, "# Terraform Wrapper Compatibility Audit")
	assert.Contains(t, out, "[!CAUTION]")
	assert.Contains(t, out, "ip_range_pods")
	assert.Contains(t, out, "enable_cost_allocation")

	// Warning alert
	wWarning := sampleWrapperReport()
	wWarning.TotalBreaking = 0
	wWarning.TotalWarnings = 1
	outWarning := RenderWrapperMarkdown(wWarning)
	assert.Contains(t, outWarning, "[!WARNING]")

	// Note alert
	wSafe := sampleWrapperReport()
	wSafe.TotalBreaking = 0
	wSafe.TotalWarnings = 0
	outSafe := RenderWrapperMarkdown(wSafe)
	assert.Contains(t, outSafe, "[!NOTE]")
}

func TestRenderWrapperJSON(t *testing.T) {
	w := sampleWrapperReport()
	data, err := RenderWrapperJSON(w)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"ip_range_pods"`)
}

func TestAssertGoldenMatch(t *testing.T) {
	tmpDir := t.TempDir()
	goldenFile := filepath.Join(tmpDir, "sub", "test.golden")
	content := []byte("canonical golden content")

	// 1. Initial creation with updateGolden = true
	AssertGoldenMatch(t, goldenFile, content, true)

	// 2. Verification against saved content
	AssertGoldenMatch(t, goldenFile, content, false)

	// 3. Verification with UPDATE_GOLDEN env var
	t.Setenv("UPDATE_GOLDEN", "true")
	AssertGoldenMatch(t, goldenFile, []byte("updated content"), false)

	// Read file to verify it updated
	saved, err := os.ReadFile(goldenFile)
	require.NoError(t, err)
	assert.Equal(t, "updated content", string(saved))
}
