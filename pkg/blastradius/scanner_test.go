package blastradius

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
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
	jsonBytes := report.RenderJSON()
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

func TestScanDirectory_TerragruntConsumer(t *testing.T) {
	searchDir := t.TempDir()
	envDir := filepath.Join(searchDir, "prod")
	require.NoError(t, os.MkdirAll(envDir, 0750))
	terragruntConfig := `
terraform {
  source = "../modules/gke_wrapper"
}

inputs = {
  removed_input = "value"
  cluster_name      = "prod"
}
`
	require.NoError(t, os.WriteFile(filepath.Join(envDir, "terragrunt.hcl"), []byte(terragruntConfig), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(envDir, "other.hcl"), []byte(terragruntConfig), 0600))

	audit := &wrapper.WrapperCompatibilityReport{
		Issues: []wrapper.WrapperIssue{
			{
				Category:    "PassedRemovedInput",
				Target:      "removed_input",
				Severity:    diff.SeverityBreaking,
				Description: "Input was removed",
			},
		},
	}
	report, err := ScanDirectory(searchDir, "gke_wrapper", nil, audit)
	require.NoError(t, err)
	require.Equal(t, 1, report.TotalCallSitesScanned)
	assert.False(t, report.CanSafelyUpdateAll)
	assert.Equal(t, "HIGH", report.HighestRisk)
	assert.Equal(t, "prod", report.ImpactedRootModules[0].ModuleName)
	assert.Equal(t, []string{"removed_input"}, report.ImpactedRootModules[0].RemovedInputsPassed)
}

func TestScanDirectory_TerragruntDynamicInputsAreNotReportedSafe(t *testing.T) {
	searchDir := t.TempDir()
	envDir := filepath.Join(searchDir, "prod")
	require.NoError(t, os.MkdirAll(envDir, 0750))
	terragruntConfig := `
terraform {
  source = "../modules/gke_wrapper"
}

inputs = merge(local.defaults, { removed_input = "value" })
`
	require.NoError(t, os.WriteFile(filepath.Join(envDir, "terragrunt.hcl"), []byte(terragruntConfig), 0600))

	report, err := ScanDirectory(searchDir, "gke_wrapper", nil, &wrapper.WrapperCompatibilityReport{})
	require.NoError(t, err)
	require.Equal(t, 1, report.TotalCallSitesScanned)
	assert.False(t, report.CanSafelyUpdateAll)
	assert.True(t, report.ImpactedRootModules[0].AnalysisIncomplete)
	assert.Equal(t, "MEDIUM", report.HighestRisk)
}

func TestScanDirectory_TerraformJSONConsumer(t *testing.T) {
	searchDir := t.TempDir()
	consumerDir := filepath.Join(searchDir, "consumer")
	targetDir := filepath.Join(searchDir, "modules", "gke_wrapper")
	require.NoError(t, os.MkdirAll(consumerDir, 0750))
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	consumerConfig := `{
  "module": {
    "my_gke": {
      "source": "../modules/gke_wrapper",
      "count": 1,
      "removed_input": "value"
    }
  },
  "output": {
    "network_tag": {
      "value": "${module.my_gke[0].legacy_network_tag}"
    },
    "literal": {
      "value": "module.my_gke.not_a_reference"
    }
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "main.tf.json"), []byte(consumerConfig), 0600))
	audit := &wrapper.WrapperCompatibilityReport{
		Issues: []wrapper.WrapperIssue{
			{Category: "PassedRemovedInput", Target: "removed_input", Severity: diff.SeverityBreaking, Description: "removed input"},
			{Category: "Output", Target: "legacy_network_tag", Severity: diff.SeverityBreaking, Description: "output was removed"},
		},
	}
	report, err := ScanDirectory(searchDir, targetDir, nil, audit)
	require.NoError(t, err)
	require.Equal(t, 1, report.TotalCallSitesScanned)
	assert.Equal(t, []string{"removed_input"}, report.ImpactedRootModules[0].RemovedInputsPassed)
	assert.Equal(t, []string{"legacy_network_tag"}, report.ImpactedRootModules[0].RemovedOutputsReferenced)
	assert.Equal(t, "CRITICAL", report.HighestRisk)
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

func TestExtractModuleReferencesUsesExpressions(t *testing.T) {
	src := []byte(`
# module.consumer.fake_comment
locals {
  note = "module.consumer.fake_string"
}
output "actual" {
  value = module.consumer.real_output
}
`)
	assert.Equal(t, map[string][]string{"consumer": {"real_output"}}, extractModuleReferences(src))
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

func TestRenderText(t *testing.T) {
	var nilReport *BlastRadiusReport
	assert.Equal(t, "", nilReport.RenderText())

	rep := &BlastRadiusReport{
		SearchDir:             "/app",
		WrapperIdentifier:     "mod",
		TotalCallSitesScanned: 2,
		CanSafelyUpdateAll:    false,
		HighestRisk:           "CRITICAL",
		Summary:               "breaking impacts detected",
		ImpactedRootModules: []RootModuleImpact{
			{
				Dir:                      "/app/prod",
				ModuleName:               "prod_cluster",
				RiskScore:                "CRITICAL",
				CanSafelyUpdate:          false,
				RemovedInputsPassed:      []string{"ip_range_services"},
				MissingRequiredInputs:    []string{"grant_registry_access"},
				RemovedOutputsReferenced: []string{"legacy_network_tag"},
				RecreationHazards:        []string{"location"},
			},
			{
				Dir:             "/app/qa",
				ModuleName:      "qa_cluster",
				RiskScore:       "LOW",
				CanSafelyUpdate: true,
			},
			{
				Dir:                "/app/dynamic",
				ModuleName:         "dynamic_cluster",
				RiskScore:          "MEDIUM",
				CanSafelyUpdate:    false,
				AnalysisIncomplete: true,
			},
		},
	}

	text := rep.RenderText()
	assert.Contains(t, text, "BLAST-RADIUS DOWNSTREAM CONSUMER ANALYSIS")
	assert.Contains(t, text, "Call Sites Scanned: 2")
	assert.Contains(t, text, "Highest Risk:      CRITICAL")
	assert.Contains(t, text, "prod_cluster")
	assert.Contains(t, text, "removed inputs passed: [ip_range_services]")
	assert.Contains(t, text, "missing required inputs: [grant_registry_access]")
	assert.Contains(t, text, "removed outputs used: [legacy_network_tag]")
	assert.Contains(t, text, "recreation hazards: [location]")
	assert.Contains(t, text, "some dynamic inputs could not be analyzed")
	assert.Contains(t, text, "qa_cluster (/app/qa) safe_to_update=true")
}

func TestDiscoverCallSitesSkipsUnreadableAndHiddenPaths(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "vendor"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".terraform", "modules"), 0750))

	consumer := `
module "wrapper" {
  source = "../wrapper"
}
`
	// Hidden, vendor and .terraform trees must never be traversed.
	for _, dir := range []string{".git", "vendor", ".terraform/modules"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, dir, "main.tf"), []byte(consumer), 0600))
	}
	// A non-Terraform file and an unreadable Terraform file are skipped.
	require.NoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("module \"wrapper\" {}"), 0600))
	unreadable := filepath.Join(root, "locked.tf")
	require.NoError(t, os.WriteFile(unreadable, []byte(consumer), 0000))
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0644) })

	if _, err := os.ReadFile(unreadable); err != nil {
		t.Skip("filesystem allows reading mode-000 files; skipping the unreadable-file branch")
	}

	sites, err := discoverCallSites(root, "wrapper")
	require.NoError(t, err)
	assert.Empty(t, sites)
}

func TestDiscoverJSONCallSitesSkipsUnparsableFiles(t *testing.T) {
	// Malformed JSON: ParseJSON fails.
	sites := discoverJSONCallSites([]byte("{not json"), "main.tf.json", "wrapper")
	assert.Nil(t, sites)

	// Valid JSON that does not contain the module block schema.
	sites = discoverJSONCallSites([]byte(`{"provider":{"aws":{}}}`), "main.tf.json", "wrapper")
	assert.Nil(t, sites)
}

func TestDiscoverJSONCallSitesIgnoresUnusableModuleBlocks(t *testing.T) {
	// module without a source, a non-matching source, and a valid call.
	src := `{
  "module": {
    "sourceless": {"region": "us"},
    "other": {"source": "./somewhere-else"},
    "match": {"source": "../wrapper", "region": "us"}
  }
}`
	sites := discoverJSONCallSites([]byte(src), "main.tf.json", "wrapper")
	require.Len(t, sites, 1)
	assert.Equal(t, "match", sites[0].ModuleName)
}

func TestParseTerragruntCallSiteRejectsUnusableFiles(t *testing.T) {
	target := "wrapper"

	// Unparsable HCL.
	_, ok := parseTerragruntCallSite([]byte("terragrunt {"), "terragrunt.hcl", target)
	assert.False(t, ok)

	// No terraform block carrying a source.
	_, ok = parseTerragruntCallSite([]byte(`terraform {}`), "terragrunt.hcl", target)
	assert.False(t, ok)

	// Source that does not point at the wrapper.
	_, ok = parseTerragruntCallSite([]byte(`terraform { source = "./elsewhere" }`), "terragrunt.hcl", target)
	assert.False(t, ok)
}

func TestParseTerragruntCallSiteFlagsDynamicInputs(t *testing.T) {
	src := `
terraform {
  source = "./wrapper"
}
inputs = {
  static_key = "value"
  (local.dynamic_key) = "value"
  1 = "numeric key"
  true = "boolean key"
}
`
	site, ok := parseTerragruntCallSite([]byte(src), "envs/dev/terragrunt.hcl", "wrapper")
	require.True(t, ok)
	assert.True(t, site.InputAnalysisIncomplete)
	assert.Contains(t, site.PassedInputs, "static_key")
}

func TestParseTerragruntCallSiteFlagsNonLiteralInputs(t *testing.T) {
	src := `
terraform {
  source = "./wrapper"
}
inputs = local.inputs
`
	site, ok := parseTerragruntCallSite([]byte(src), "envs/dev/terragrunt.hcl", "wrapper")
	require.True(t, ok)
	assert.True(t, site.InputAnalysisIncomplete)
	assert.Empty(t, site.PassedInputs)
}

func TestTerragruntInputKey(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"quoted key", `inputs = { "key" = "value" }`},
		{"bare identifier key", `inputs = { key = "value" }`},
		{"parenthesised key", `inputs = { (var.dynamic_key) = "value" }`},
		{"computed parenthesised key", `inputs = { (1 + 2) = "value" }`},
		{"numeric key", `inputs = { 1 = "value" }`},
		{"boolean key", `inputs = { true = "value" }`},
		{"null key", `inputs = { null = "value" }`},
		{"multi step traversal key", `inputs = { a.b = "value" }`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, diags := hclsyntax.ParseConfig([]byte(tc.src), "terragrunt.hcl", hcl.Pos{Line: 1, Column: 1})
			require.False(t, diags.HasErrors())
			inputs, ok := file.Body.(*hclsyntax.Body).Attributes["inputs"].Expr.(*hclsyntax.ObjectConsExpr)
			require.True(t, ok)

			key, resolved := terragruntInputKey(inputs.Items[0].KeyExpr)
			if tc.name == "bare identifier key" {
				assert.True(t, resolved, "expected a resolvable key")
				assert.Equal(t, "key", key)
				return
			}
			assert.False(t, resolved)
			assert.Empty(t, key)
		})
	}
}

func TestExtractModuleReferencesHandlesInvalidSource(t *testing.T) {
	refs := extractModuleReferences([]byte("this is not { valid hcl"))
	assert.Empty(t, refs)
}

func TestAddModuleReferencesWithSeenIgnoresUnusableTraversals(t *testing.T) {
	refs := map[string][]string{}
	seen := map[string]map[string]struct{}{}

	addModuleReferencesWithSeen(refs, seen, []hcl.Traversal{
		// Shorter than a module.<call>.<output> reference.
		{hcl.TraverseRoot{Name: "module"}},
		{hcl.TraverseRoot{Name: "var"}, hcl.TraverseAttr{Name: "x"}},
		{hcl.TraverseRoot{Name: "module"}, hcl.TraverseAttr{Name: "gke"}},
		// Root is not "module".
		{hcl.TraverseRoot{Name: "data"}, hcl.TraverseAttr{Name: "aws"}, hcl.TraverseAttr{Name: "id"}},
		// Second step is an index rather than an attribute.
		{hcl.TraverseRoot{Name: "module"}, hcl.TraverseIndex{Key: cty.NumberIntVal(0)}, hcl.TraverseAttr{Name: "out"}},
	})
	assert.Empty(t, refs)
}

func TestDiscoverCallSitesPropagatesWalkErrors(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	require.NoError(t, os.MkdirAll(blocked, 0750))
	require.NoError(t, os.Chmod(blocked, 0000))
	t.Cleanup(func() { _ = os.Chmod(blocked, 0750) })

	if _, err := os.ReadDir(blocked); err == nil {
		t.Skip("filesystem allows reading mode-000 directories; skipping the walk error branch")
	}

	_, err := discoverCallSites(root, "wrapper")
	require.Error(t, err)
}

func TestDiscoverJSONCallSitesRejectsSchemaMismatchedJSON(t *testing.T) {
	// A block body that is not a JSON object produces a schema diagnostic.
	sites := discoverJSONCallSites([]byte(`{"module": {"gke": "not-an-object"}}`), "main.tf.json", "wrapper")
	assert.Nil(t, sites)

	sites = discoverJSONCallSites([]byte(`{"module": "not-an-object"}`), "main.tf.json", "wrapper")
	assert.Nil(t, sites)
}

func TestParseTerragruntCallSiteIgnoresNonTerraformBlocks(t *testing.T) {
	src := `
locals {
  region = "us-central1"
}
terraform {
  source = "./wrapper"
}
`
	site, ok := parseTerragruntCallSite([]byte(src), "envs/dev/terragrunt.hcl", "wrapper")
	require.True(t, ok)
	assert.Equal(t, "./wrapper", site.Source)
}

func TestEvaluateCallSiteImpact_BlockingTypeMismatchIsMediumRisk(t *testing.T) {
	impact := evaluateCallSiteImpact(CallSite{
		Dir:        "/envs/dev",
		ModuleName: "dev_cluster",
		PassedInputs: map[string]string{
			"zones": "var.zones",
		},
	}, nil, &wrapper.WrapperCompatibilityReport{
		TargetModuleName: "gke",
		Issues: []wrapper.WrapperIssue{
			{
				Severity:    diff.SeverityBreaking,
				Category:    "TypeMismatch",
				Target:      "zones",
				Description: "Wrapper variable 'zones' type changed incompatibly",
			},
		},
	})
	assert.False(t, impact.CanSafelyUpdate)
	assert.Equal(t, "MEDIUM", impact.RiskScore)
}

func TestAddModuleReferencesDedupesExistingEntries(t *testing.T) {
	refs := map[string][]string{"gke": {"endpoint"}}
	traversals := []hcl.Traversal{
		{hcl.TraverseRoot{Name: "module"}, hcl.TraverseAttr{Name: "gke"}, hcl.TraverseAttr{Name: "endpoint"}},
		{hcl.TraverseRoot{Name: "module"}, hcl.TraverseAttr{Name: "gke"}, hcl.TraverseAttr{Name: "name"}},
		{hcl.TraverseRoot{Name: "module"}, hcl.TraverseAttr{Name: "gke"}, hcl.TraverseAttr{Name: "name"}},
	}

	addModuleReferences(refs, traversals)
	assert.ElementsMatch(t, []string{"endpoint", "name"}, refs["gke"])
}

func TestEvaluateCallSiteImpact_IncompleteAnalysisOnly(t *testing.T) {
	impact := evaluateCallSiteImpact(CallSite{
		Dir:                     "/envs/dev",
		ModuleName:              "dev_cluster",
		InputAnalysisIncomplete: true,
	}, nil, nil)
	assert.False(t, impact.CanSafelyUpdate)
	assert.Equal(t, "MEDIUM", impact.RiskScore)
}

func TestEvaluateCallSiteImpact_IncompleteAnalysisIsAlwaysMedium(t *testing.T) {
	impact := evaluateCallSiteImpact(CallSite{
		Dir:                     "/envs/dev",
		ModuleName:              "dev_cluster",
		InputAnalysisIncomplete: true,
	}, nil, &wrapper.WrapperCompatibilityReport{})
	assert.False(t, impact.CanSafelyUpdate)
	assert.Equal(t, "MEDIUM", impact.RiskScore)
}

func TestRenderMarkdownRendersIncompleteAnalysis(t *testing.T) {
	rep := &BlastRadiusReport{
		SearchDir:             "/app",
		WrapperIdentifier:     "mod",
		TotalCallSitesScanned: 1,
		HighestRisk:           "MEDIUM",
		ImpactedRootModules: []RootModuleImpact{
			{
				Dir:                "/app/dev",
				ModuleName:         "dev_cluster",
				RiskScore:          "MEDIUM",
				CanSafelyUpdate:    false,
				AnalysisIncomplete: true,
			},
		},
	}
	md := rep.RenderMarkdown()
	assert.Contains(t, md, "Some dynamic inputs could not be analyzed")
}

// TestDiscoverCallSites_EmptyModuleLabel pins the behaviour of `module "" {}`, which HCL accepts
// and which must not be mistaken for an unparsable file.
func TestDiscoverCallSites_EmptyModuleLabel(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.tf"), []byte(`module "" { source = "../wrapper" }`), 0600))

	sites, err := discoverCallSites(root, "wrapper")
	require.NoError(t, err)
	require.Len(t, sites, 1)
	assert.Empty(t, sites[0].ModuleName)
	assert.NotEmpty(t, sites[0].File)
	assert.NotNil(t, sites[0].PassedInputs)
}
