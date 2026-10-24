package goldentest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultiCloudCatalog(t *testing.T) {
	catalog := MultiCloudCatalog()
	require.NotEmpty(t, catalog)

	var hasGCP, hasAWS, hasAzure bool
	for _, m := range catalog {
		switch m.Provider {
		case ProviderGCP:
			hasGCP = true
		case ProviderAWS:
			hasAWS = true
		case ProviderAzure:
			hasAzure = true
		}
	}
	assert.True(t, hasGCP, "matrix should include GCP modules")
	assert.True(t, hasAWS, "matrix should include AWS modules")
	assert.True(t, hasAzure, "matrix should include Azure modules")
}

func TestDetectDrift(t *testing.T) {
	tmpDir := t.TempDir()
	baselinePath := filepath.Join(tmpDir, "baseline.json")

	spec := ModuleSpec{
		ID:                 "azure-aks",
		Provider:           ProviderAzure,
		Name:               "Azure Kubernetes Service (AKS)",
		Source:             "Azure/aks/azurerm",
		BaselineVersion:    "v7.0.0",
		TargetVersion:      "v8.0.0",
		GoldenBaselinePath: baselinePath,
	}

	baselineReport := ReportSummary{
		ModuleName:    spec.Source,
		Verdict:       "BREAKING_CHANGES",
		TotalBreaking: 1,
		BreakingChanges: []BreakingChangeSummary{
			{Name: "agents_type", Type: "removed_input", Description: "Input was removed"},
		},
	}
	baselineBytes, err := json.Marshal(baselineReport)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(baselinePath, baselineBytes, 0o600))

	// 1. Error: invalid actual JSON
	_, err = DetectDrift(spec, []byte("invalid-json"), baselinePath)
	assert.Error(t, err)

	// 2. Error: missing baseline file
	_, err = DetectDrift(spec, baselineBytes, filepath.Join(tmpDir, "missing.json"))
	assert.Error(t, err)

	// 3. Error: invalid baseline file JSON
	corruptPath := filepath.Join(tmpDir, "corrupt.json")
	require.NoError(t, os.WriteFile(corruptPath, []byte("{bad"), 0o600))
	_, err = DetectDrift(spec, baselineBytes, corruptPath)
	assert.Error(t, err)

	// 4. Exact match: No drift
	report, err := DetectDrift(spec, baselineBytes, baselinePath)
	require.NoError(t, err)
	assert.False(t, report.HasDrift)
	assert.Equal(t, 1, report.BaselineBreakingCount)
	assert.Equal(t, 1, report.ActualBreakingCount)
	assert.Empty(t, report.UndocumentedChanges)
	assert.Contains(t, report.Summary, "MATCHES BASELINE")

	// 5. Drift: Undocumented breaking change introduced
	driftActual := ReportSummary{
		ModuleName:    spec.Source,
		Verdict:       "BREAKING_CHANGES",
		TotalBreaking: 2,
		BreakingChanges: []BreakingChangeSummary{
			{Name: "agents_type", Type: "removed_input", Description: "Input was removed"},
			{Name: "workload_identity_enabled", Type: "new_required_input", Description: "Input is newly required"},
		},
	}
	driftActualBytes, err := json.Marshal(driftActual)
	require.NoError(t, err)

	report, err = DetectDrift(spec, driftActualBytes, baselinePath)
	require.NoError(t, err)
	assert.True(t, report.HasDrift)
	assert.Equal(t, 1, report.BaselineBreakingCount)
	assert.Equal(t, 2, report.ActualBreakingCount)
	require.Len(t, report.UndocumentedChanges, 1)
	assert.Contains(t, report.UndocumentedChanges[0], "workload_identity_enabled")
	assert.Contains(t, report.Summary, "DRIFT DETECTED")

	// 6. Drift: Count mismatch without undocumented changes
	mismatchActual := ReportSummary{
		ModuleName:    spec.Source,
		Verdict:       "BREAKING_CHANGES",
		TotalBreaking: 0,
		BreakingChanges: []BreakingChangeSummary{
			{Name: "agents_type", Type: "removed_input", Description: "Input was removed"},
		},
	}
	mismatchBytes, err := json.Marshal(mismatchActual)
	require.NoError(t, err)

	report, err = DetectDrift(spec, mismatchBytes, baselinePath)
	require.NoError(t, err)
	assert.True(t, report.HasDrift)
	assert.Empty(t, report.UndocumentedChanges)
}

func TestGenerateDriftIssuePayload(t *testing.T) {
	spec := ModuleSpec{
		ID:              "azure-aks",
		Provider:        ProviderAzure,
		Name:            "Azure Kubernetes Service (AKS)",
		Source:          "Azure/aks/azurerm",
		BaselineVersion: "v7.0.0",
		TargetVersion:   "v8.0.0",
	}

	// 1. All clean
	cleanReports := []*DriftReport{
		{
			Spec:                  spec,
			HasDrift:              false,
			BaselineBreakingCount: 1,
			ActualBreakingCount:   1,
		},
	}
	cleanPayload, err := GenerateDriftIssuePayload(cleanReports)
	require.NoError(t, err)
	assert.Contains(t, cleanPayload, "All Baselines Intact")

	// 2. Drifted reports: one with undocumented changes, one with count mismatch
	driftReports := []*DriftReport{
		{
			Spec:                  spec,
			HasDrift:              true,
			BaselineBreakingCount: 1,
			ActualBreakingCount:   2,
			UndocumentedChanges:   []string{"dns_prefix: type changed from string to list(string)"},
		},
		{
			Spec: ModuleSpec{
				ID:              "aws-eks",
				Provider:        ProviderAWS,
				Name:            "Elastic Kubernetes Service (EKS)",
				Source:          "terraform-aws-modules/eks/aws",
				BaselineVersion: "v19.0.0",
				TargetVersion:   "v20.0.0",
			},
			HasDrift:              true,
			BaselineBreakingCount: 4,
			ActualBreakingCount:   3,
			UndocumentedChanges:   nil,
		},
	}

	driftPayload, err := GenerateDriftIssuePayload(driftReports)
	require.NoError(t, err)
	assert.Contains(t, driftPayload, "Upstream Multi-Cloud Module Drift Detected")
	assert.Contains(t, driftPayload, "Azure Kubernetes Service (AKS)")
	assert.Contains(t, driftPayload, "dns_prefix: type changed")
	assert.Contains(t, driftPayload, "Breaking count mismatch: baseline had 4, actual has 3")
}

func TestMultiCloudCatalog_DetectDriftAgainstBaselines(t *testing.T) {
	catalog := MultiCloudCatalog()
	require.NotEmpty(t, catalog)

	var reports []*DriftReport
	for _, spec := range catalog {
		goldenFile := filepath.Join("../../", spec.GoldenBaselinePath)
		require.FileExists(t, goldenFile, "Golden baseline file must exist: %s", goldenFile)

		goldenBytes, err := os.ReadFile(goldenFile)
		require.NoError(t, err)

		report, err := DetectDrift(spec, goldenBytes, goldenFile)
		require.NoError(t, err)
		assert.False(t, report.HasDrift, "Canonical golden baseline must match itself with no drift: %s", spec.ID)
		assert.Contains(t, report.Summary, "MATCHES BASELINE")
		reports = append(reports, report)
	}

	payload, err := GenerateDriftIssuePayload(reports)
	require.NoError(t, err)
	assert.Contains(t, payload, "All Baselines Intact")
}
