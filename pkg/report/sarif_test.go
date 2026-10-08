package report

import (
	"encoding/json"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderSARIF(t *testing.T) {
	// Nil handling
	nilData, err := RenderSARIF(nil)
	require.NoError(t, err)
	assert.Contains(t, string(nilData), "sarif-schema-2.1.0.json")

	report := &diff.DiffReport{
		ModuleName: "terraform-google-modules/kubernetes-engine/google",
		BreakingChanges: []diff.DifferenceItem{
			{
				Category:    "Variable",
				Name:        "ip_range_services",
				Description: "Removed",
				Remediation: "Remove it",
			},
		},
		Warnings: []diff.DifferenceItem{
			{
				Category:    "VariableType",
				Name:        "zones",
				Description: "Widened",
				Remediation: "Check values",
			},
		},
		NewFeatures: []diff.DifferenceItem{
			{
				Category:    "Variable",
				Name:        "enable_cost",
				Description: "New variable",
			},
		},
	}

	sarifBytes, err := RenderSARIF(report)
	require.NoError(t, err)

	var sarifLog SARIFLog
	err = json.Unmarshal(sarifBytes, &sarifLog)
	require.NoError(t, err)

	assert.Equal(t, "2.1.0", sarifLog.Version)
	require.Len(t, sarifLog.Runs, 1)
	assert.Equal(t, "terraform-ast-diff", sarifLog.Runs[0].Tool.Driver.Name)
	assert.Len(t, sarifLog.Runs[0].Results, 3)

	// Check error level for breaking
	assert.Equal(t, "error", sarifLog.Runs[0].Results[0].Level)
	assert.Equal(t, "warning", sarifLog.Runs[0].Results[1].Level)
	assert.Equal(t, "note", sarifLog.Runs[0].Results[2].Level)
}

func TestRenderWrapperSARIF(t *testing.T) {
	// Nil handling
	nilData, err := RenderWrapperSARIF(nil)
	require.NoError(t, err)
	assert.Contains(t, string(nilData), "sarif-schema-2.1.0.json")

	res := &wrapper.WrapperCompatibilityReport{
		WrapperPath:      "modules/gke_wrapper",
		TargetModuleName: "terraform-google-modules/kubernetes-engine/google",
		Issues: []wrapper.WrapperIssue{
			{
				Severity:    diff.SeverityBreaking,
				Category:    "Variable",
				Target:      "ip_range_services",
				Description: "Passed removed input",
				Remediation: "Remove reference",
			},
			{
				Severity:    diff.SeverityWarning,
				Category:    "Warning",
				Target:      "zones",
				Description: "Widened type",
			},
		},
	}

	sarifBytes, err := RenderWrapperSARIF(res)
	require.NoError(t, err)

	var sarifLog SARIFLog
	err = json.Unmarshal(sarifBytes, &sarifLog)
	require.NoError(t, err)

	require.Len(t, sarifLog.Runs, 1)
	assert.Len(t, sarifLog.Runs[0].Results, 2)
	assert.Equal(t, "error", sarifLog.Runs[0].Results[0].Level)
	assert.Equal(t, "warning", sarifLog.Runs[0].Results[1].Level)
	assert.Equal(t, "modules/gke_wrapper", sarifLog.Runs[0].Results[0].Locations[0].PhysicalLocation.ArtifactLocation.URI)

	// Test default filename when WrapperPath is empty
	res.WrapperPath = ""
	sarifBytes2, err := RenderWrapperSARIF(res)
	require.NoError(t, err)
	assert.Contains(t, string(sarifBytes2), "main.tf")
}
