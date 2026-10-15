package report

import (
	"encoding/json"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/blastradius"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/policy"
	"github.com/23skdu/terraform_ast/pkg/recreation"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderSARIF(t *testing.T) {
	// Nil handling
	nilData := RenderSARIF(nil, Findings{})
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

	sarifBytes := RenderSARIF(report, Findings{})

	var sarifLog SARIFLog
	err := json.Unmarshal(sarifBytes, &sarifLog)
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
	nilData := RenderWrapperSARIF(nil, Findings{})
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

	sarifBytes := RenderWrapperSARIF(res, Findings{})

	var sarifLog SARIFLog
	err := json.Unmarshal(sarifBytes, &sarifLog)
	require.NoError(t, err)

	require.Len(t, sarifLog.Runs, 1)
	assert.Len(t, sarifLog.Runs[0].Results, 2)
	assert.Equal(t, "error", sarifLog.Runs[0].Results[0].Level)
	assert.Equal(t, "warning", sarifLog.Runs[0].Results[1].Level)
	assert.Equal(t, "modules/gke_wrapper", sarifLog.Runs[0].Results[0].Locations[0].PhysicalLocation.ArtifactLocation.URI)

	// Test default filename when WrapperPath is empty
	res.WrapperPath = ""
	sarifBytes2 := RenderWrapperSARIF(res, Findings{})
	assert.Contains(t, string(sarifBytes2), "main.tf")
}

func TestRenderSARIF_WithFindings(t *testing.T) {
	report := &diff.DiffReport{
		ModuleName: "",
		Warnings: []diff.DifferenceItem{
			{Category: "VariableType", Name: "zones", Description: "Widened", Remediation: "Check values"},
		},
	}

	findings := Findings{
		RecreationHazards: []recreation.RecreationHazard{
			{Variable: "location", Resource: "google_container_cluster", HazardType: "VariableType", Severity: "WARNING_HAZARD", Description: "Location is immutable"},
		},
		BlastRadius: &blastradius.BlastRadiusReport{
			ImpactedRootModules: []blastradius.RootModuleImpact{
				{
					Dir:                 "/envs/dev",
					ModuleName:          "primary_cluster",
					RiskScore:           "CRITICAL",
					CanSafelyUpdate:     false,
					RemovedInputsPassed: []string{"ip_range_services"},
					RecreationHazards:   []string{"location"},
				},
				{Dir: "/envs/qa", ModuleName: "safe_cluster", RiskScore: "LOW", CanSafelyUpdate: true},
			},
		},
	}

	sarifBytes := RenderSARIF(report, findings)

	var log SARIFLog
	require.NoError(t, json.Unmarshal(sarifBytes, &log))
	require.Len(t, log.Runs, 1)
	assert.Len(t, log.Runs[0].Tool.Driver.Rules, 3, "only rules with results are advertised")

	ruleIDs := make(map[string]bool)
	for _, rule := range log.Runs[0].Tool.Driver.Rules {
		ruleIDs[rule.ID] = true
	}
	assert.True(t, ruleIDs[ruleRecreationHazard])
	assert.True(t, ruleIDs[ruleBlastRadius])
	assert.False(t, ruleIDs[ruleBreaking])

	results := log.Runs[0].Results
	require.Len(t, results, 3, "1 warning + 1 hazard + 1 impacted consumer")

	assert.Equal(t, ruleWarning, results[0].RuleID)
	assert.Equal(t, "main.tf", results[0].Locations[0].PhysicalLocation.ArtifactLocation.URI)

	assert.Equal(t, ruleRecreationHazard, results[1].RuleID)
	assert.Contains(t, results[1].Message.Text, "location")

	assert.Equal(t, ruleBlastRadius, results[2].RuleID)
	assert.Equal(t, "/envs/dev", results[2].Locations[0].PhysicalLocation.ArtifactLocation.URI)
	assert.Contains(t, results[2].Message.Text, "ip_range_services")

	// Every emitted result must have a documented rule.
	for _, result := range results {
		assert.True(t, ruleIDs[result.RuleID], "undocumented rule id %s", result.RuleID)
	}
}

func TestRenderWrapperSARIF_WithFindings(t *testing.T) {
	res := &wrapper.WrapperCompatibilityReport{
		WrapperPath:      "modules/gke_wrapper",
		TargetModuleName: "gke",
		Issues: []wrapper.WrapperIssue{
			{Severity: diff.SeverityBreaking, Category: "PassedRemovedInput", Target: "ip_range_services", Description: "Removed"},
		},
		PolicyViolations: []policy.Violation{{Rule: "block_breaking", Message: "blocked"}},
	}

	findings := Findings{
		RecreationHazards: []recreation.RecreationHazard{
			{Variable: "project_id", HazardType: "PassedRemovedInput", Description: "Project ID is immutable"},
		},
		BlastRadius: &blastradius.BlastRadiusReport{
			ImpactedRootModules: []blastradius.RootModuleImpact{
				{Dir: "/envs/prod", ModuleName: "prod_cluster", RiskScore: "HIGH", MissingRequiredInputs: []string{"grant_registry_access"}},
			},
		},
	}

	sarifBytes := RenderWrapperSARIF(res, findings)

	var log SARIFLog
	require.NoError(t, json.Unmarshal(sarifBytes, &log))
	require.Len(t, log.Runs, 1)

	results := log.Runs[0].Results
	require.Len(t, results, 4)
	assert.Equal(t, ruleBreaking, results[0].RuleID)
	assert.Equal(t, ruleRecreationHazard, results[1].RuleID)
	assert.Equal(t, ruleBlastRadius, results[2].RuleID)
	assert.Equal(t, rulePolicy, results[3].RuleID)
	assert.Equal(t, "modules/gke_wrapper", results[3].Locations[0].PhysicalLocation.ArtifactLocation.URI)
	assert.Len(t, log.Runs[0].Tool.Driver.Rules, 4)
}
