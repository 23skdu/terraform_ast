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

func TestRiskRank(t *testing.T) {
	assert.Equal(t, 4, riskRank("CRITICAL"))
	assert.Equal(t, 3, riskRank("HIGH"))
	assert.Equal(t, 2, riskRank("MEDIUM"))
	assert.Equal(t, 1, riskRank("LOW"))
	assert.Equal(t, 0, riskRank("UNKNOWN"))
}

func TestIsMatchingSource(t *testing.T) {
	caller := "/repos/app/main.tf"
	assert.True(t, isMatchingSource(caller, "../modules/vpc", "../modules/vpc"))
	assert.True(t, isMatchingSource(caller, "../modules/vpc", "vpc"))
	assert.True(t, isMatchingSource(caller, "./submod", "/repos/app/submod"))
	assert.False(t, isMatchingSource(caller, "hashicorp/consul", "my-vpc"))
}

func TestScanDirectory_FilteringAndErrors(t *testing.T) {
	dir := t.TempDir()

	// 1. vendor and .terraform directories should be skipped
	vendorDir := filepath.Join(dir, "vendor")
	require.NoError(t, os.MkdirAll(vendorDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(vendorDir, "main.tf"), []byte(`module "m" { source = "target" }`), 0600))

	tfDir := filepath.Join(dir, ".terraform")
	require.NoError(t, os.MkdirAll(tfDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(tfDir, "main.tf"), []byte(`module "m" { source = "target" }`), 0600))

	// 2. Non-tf file
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Docs"), 0600))

	// Unreadable .tf file
	noReadTF := filepath.Join(dir, "unread.tf")
	require.NoError(t, os.WriteFile(noReadTF, []byte(`module "m" {}`), 0600))
	require.NoError(t, os.Chmod(noReadTF, 0000))
	defer func() { _ = os.Chmod(noReadTF, 0600) }()

	// 3. Invalid HCL file
	require.NoError(t, os.WriteFile(filepath.Join(dir, "invalid.tf"), []byte("{{{ syntax error"), 0600))

	// 4. Module block without source
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nosrc.tf"), []byte(`module "m" { name = "foo" }`), 0600))

	// 5. Module block with non-matching source
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.tf"), []byte(`module "other" { source = "unrelated/repo" }`), 0600))

	// Scan should succeed with 0 call sites found
	rep, err := ScanDirectory(dir, "target", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, rep.TotalCallSitesScanned)

	// Unreadable dir triggers error in WalkDir
	unreadable := filepath.Join(dir, "unreadable")
	require.NoError(t, os.MkdirAll(unreadable, 0750))
	require.NoError(t, os.Chmod(unreadable, 0000))
	defer func() { _ = os.Chmod(unreadable, 0750) }()
	_, err = ScanDirectory(dir, "target", nil, nil)
	assert.Error(t, err)
}

func TestEvaluateCallSiteImpact_RiskLevels(t *testing.T) {
	cs := CallSite{
		Dir:        "/app",
		ModuleName: "app_mod",
		PassedInputs: map[string]string{
			"env": "prod",
		},
	}

	// 1. High risk: missing required inputs without recreation hazard or removed output
	auditHigh := &wrapper.WrapperCompatibilityReport{
		Issues: []wrapper.WrapperIssue{
			{
				Category:    "Variable",
				Target:      "cluster_id",
				Severity:    diff.SeverityBreaking,
				Description: "New required variable 'cluster_id' added",
			},
		},
	}
	impactHigh := evaluateCallSiteImpact(cs, nil, auditHigh)
	assert.Equal(t, "HIGH", impactHigh.RiskScore)
	assert.False(t, impactHigh.CanSafelyUpdate)

	// 2. Medium risk: breaking type change on passed input
	auditMed := &wrapper.WrapperCompatibilityReport{
		Issues: []wrapper.WrapperIssue{
			{
				Category:    "Variable",
				Target:      "env",
				Severity:    diff.SeverityBreaking,
				Description: "Variable 'env' type changed incompatibly",
			},
		},
	}
	impactMed := evaluateCallSiteImpact(cs, nil, auditMed)
	assert.Equal(t, "MEDIUM", impactMed.RiskScore)
	assert.False(t, impactMed.CanSafelyUpdate)
}

func TestRenderMarkdown_WithSafeConsumer(t *testing.T) {
	rep := &BlastRadiusReport{
		SearchDir:             "/app",
		WrapperIdentifier:     "mod",
		TotalCallSitesScanned: 1,
		CanSafelyUpdateAll:    true,
		HighestRisk:           "LOW",
		Summary:               "all safe",
		ImpactedRootModules: []RootModuleImpact{
			{
				Dir:             "/app/env",
				ModuleName:      "mod",
				RiskScore:       "LOW",
				CanSafelyUpdate: true,
			},
		},
	}
	md := rep.RenderMarkdown()
	assert.Contains(t, md, "None")
}


