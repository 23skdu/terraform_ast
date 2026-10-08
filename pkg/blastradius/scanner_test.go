package blastradius

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanDirectory_ErrorsAndEmpty(t *testing.T) {
	// 1. Non-existent path
	_, err := ScanDirectory("/non/existent/dir", "my-wrapper", nil, nil)
	assert.Error(t, err)

	// 2. File instead of directory
	tmpFile := filepath.Join(t.TempDir(), "somefile.txt")
	require.NoError(t, os.WriteFile(tmpFile, []byte("hello"), 0600))
	_, err = ScanDirectory(tmpFile, "my-wrapper", nil, nil)
	assert.Error(t, err)

	// 3. Empty directory
	emptyDir := t.TempDir()
	report, err := ScanDirectory(emptyDir, "my-wrapper", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, report.TotalCallSitesScanned)
	assert.True(t, report.CanSafelyUpdateAll)
	assert.Contains(t, report.Summary, "No downstream consumer modules found")
}

func TestScanDirectory_ImpactAnalysis(t *testing.T) {
	searchDir := t.TempDir()

	// Create root module 1: safe consumer
	safeDir := filepath.Join(searchDir, "env-safe")
	require.NoError(t, os.MkdirAll(safeDir, 0750))
	safeTF := `
module "gke_cluster" {
  source       = "../modules/gke_wrapper"
  cluster_name = "prod-cluster"
}
`
	require.NoError(t, os.WriteFile(filepath.Join(safeDir, "main.tf"), []byte(safeTF), 0600))

	// Create root module 2: impacted consumer (passes removed input + references removed output)
	impactedDir := filepath.Join(searchDir, "env-impacted")
	require.NoError(t, os.MkdirAll(impactedDir, 0750))
	impactedTF := `
module "my_gke" {
  source            = "../modules/gke_wrapper"
  ip_range_services = "10.0.0.0/20"
}

output "net_tag" {
  value = module.my_gke.legacy_network_tag
}
`
	require.NoError(t, os.WriteFile(filepath.Join(impactedDir, "main.tf"), []byte(impactedTF), 0600))

	// Mock Wrapper Audit Report
	audit := &wrapper.WrapperCompatibilityReport{
		TargetModuleName: "terraform-google-modules/kubernetes-engine/google",
		Issues: []wrapper.WrapperIssue{
			{
				Category:    "Variable",
				Target:      "ip_range_services",
				Severity:    diff.SeverityBreaking,
				Description: "Input variable 'ip_range_services' removed in upstream version",
			},
			{
				Category:    "Variable",
				Target:      "cluster_autoscaling_profile",
				Severity:    diff.SeverityBreaking,
				Description: "New required variable 'cluster_autoscaling_profile' added without default",
			},
			{
				Category:    "Output",
				Target:      "legacy_network_tag",
				Severity:    diff.SeverityBreaking,
				Description: "Output 'legacy_network_tag' was removed",
			},
		},
	}

	report, err := ScanDirectory(searchDir, "gke_wrapper", nil, audit)
	require.NoError(t, err)
	assert.Equal(t, 2, report.TotalCallSitesScanned)
	assert.False(t, report.CanSafelyUpdateAll)
	assert.Equal(t, "CRITICAL", report.HighestRisk)

	// Check JSON and Markdown rendering
	jsonBytes, err := report.RenderJSON()
	require.NoError(t, err)
	assert.Contains(t, string(jsonBytes), "env-impacted")

	md := report.RenderMarkdown()
	assert.Contains(t, md, "Blast-Radius Downstream Consumer Analysis")
	assert.Contains(t, md, "CRITICAL")
	assert.Contains(t, md, "legacy_network_tag")

	// Test safe consumers only
	safeOnlyReport, err := ScanDirectory(safeDir, "gke_wrapper", nil, &wrapper.WrapperCompatibilityReport{})
	require.NoError(t, err)
	assert.Equal(t, 1, safeOnlyReport.TotalCallSitesScanned)
	assert.True(t, safeOnlyReport.CanSafelyUpdateAll)
	assert.Contains(t, safeOnlyReport.Summary, "can safely update")

	// Nil audit
	nilAuditReport, err := ScanDirectory(safeDir, "gke_wrapper", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, nilAuditReport.TotalCallSitesScanned)
	assert.True(t, nilAuditReport.CanSafelyUpdateAll)
}

