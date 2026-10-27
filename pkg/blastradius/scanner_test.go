package blastradius

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/eval"
	"github.com/23skdu/terraform_ast/pkg/recreation"
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
	// The consumer passes `cluster_name`, but this audit carries no TargetModuleName, so the
	// provider cannot be identified and no provider-specific static hazard can be asserted. The
	// risk therefore stays HIGH, driven by the removed input. See TestMatchModuleCategory.
	assert.Equal(t, "HIGH", report.HighestRisk)
	assert.Equal(t, "prod", report.ImpactedRootModules[0].ModuleName)
	assert.Equal(t, []string{"removed_input"}, report.ImpactedRootModules[0].RemovedInputsPassed)
	assert.Empty(t, report.ImpactedRootModules[0].RecreationHazards)
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
	assert.False(t, isMatchingSource(caller, "../modules/vpc", "other"))
	assert.True(t, isMatchingSource(caller, "./submod", "/repos/app/submod"))
	assert.False(t, isMatchingSource(caller, "hashicorp/consul", "my-vpc"))

	// filepath.IsAbs(cleanTarget) with relative resolved
	relCaller := "app/main.tf"
	absTarget, _ := filepath.Abs("app/submod")
	assert.True(t, isMatchingSource(relCaller, "./submod", absTarget))

	// filepath.IsAbs(callerDir) && !filepath.IsAbs(cleanTarget) where absTarget matches resolved
	cwd, _ := os.Getwd()
	nestedCaller := filepath.Join(cwd, "nested", "main.tf")
	assert.True(t, isMatchingSource(nestedCaller, "./submod", "nested/submod"))

	// sourceHasSlash && strings.HasSuffix(resolved, cleanTarget)
	assert.True(t, isMatchingSource("/a/b/main.tf", "c/d", "b/c/d"))
	assert.False(t, isMatchingSource("/a/b/main.tf", "c/d", "x/y"))
}

func TestEvaluateCallSiteImpact_WrapperModOutputsAndRegistry(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		Outputs: map[string]ast.OutputDef{
			"existing_out": {Name: "existing_out"},
		},
	}
	cs := CallSite{
		OutputsReferenced: []string{"existing_out", "deleted_out"},
		PassedInputs:      map[string]string{"custom_force_new": "val"},
	}
	ps := &recreation.ProviderSchemas{
		ProviderSchemas: map[string]recreation.ProviderSchema{
			"aws": {
				ResourceSchemas: map[string]recreation.ResourceSchema{
					"aws_instance": {
						Block: recreation.BlockSchema{
							Attributes: map[string]recreation.AttributeSchema{
								"custom_force_new": {RequiresReplace: true},
							},
						},
					},
				},
			},
		},
	}
	reg := recreation.NewDynamicHazardRegistry(ps)
	impact := evaluateCallSiteImpact(cs, wrapperMod, &wrapper.WrapperCompatibilityReport{}, reg)
	assert.False(t, impact.CanSafelyUpdate)
	assert.Contains(t, impact.RemovedOutputsReferenced, "deleted_out")
	require.Len(t, impact.RecreationHazards, 1)
	assert.Contains(t, impact.RecreationHazards[0], "custom_force_new")
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

	// Unreadable subdirectory is skipped in WalkDir without aborting scan
	unreadable := filepath.Join(dir, "unreadable")
	require.NoError(t, os.MkdirAll(unreadable, 0750))
	require.NoError(t, os.Chmod(unreadable, 0000))
	defer func() { _ = os.Chmod(unreadable, 0750) }()
	rep, err = ScanDirectory(dir, "target", nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, 0, rep.TotalCallSitesScanned)

	// Scanning unreadable root directly returns an error
	_, err = ScanDirectory(unreadable, "target", nil, nil)
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
	impactHigh := evaluateCallSiteImpact(cs, nil, auditHigh, nil)
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
	impactMed := evaluateCallSiteImpact(cs, nil, auditMed, nil)
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

func TestParseTerragruntCallSiteResolvesDynamicEvaluatedInputs(t *testing.T) {
	src := `
terraform {
  source = "./wrapper"
}
inputs = merge({ region = "us-west1" }, { cluster_name = "prod-eks" })
`
	site, ok := parseTerragruntCallSite([]byte(src), "envs/dev/terragrunt.hcl", "wrapper")
	require.True(t, ok)
	assert.False(t, site.InputAnalysisIncomplete)
	assert.Equal(t, `"us-west1"`, site.PassedInputs["region"])
	assert.Equal(t, `"prod-eks"`, site.PassedInputs["cluster_name"])
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
			if tc.name == "bare identifier key" || tc.name == "quoted key" {
				assert.True(t, resolved, "expected a resolvable key")
				assert.Equal(t, "key", key)
				return
			}
			assert.False(t, resolved)
			assert.Empty(t, key)
		})
	}
}

func TestIsMatchingSource_SuffixCollision(t *testing.T) {
	// Subpath suffix should NOT collide across different directory names
	callerFile := "/repo/live/prod/terragrunt.hcl"
	target := "modules/vpc"

	// Colliding suffix (submodules/vpc ends with modules/vpc string-wise, but not path-wise)
	assert.False(t, isMatchingSource(callerFile, "../../submodules/vpc", target))

	// Exact directory match
	assert.True(t, isMatchingSource(callerFile, "../../modules/vpc", target))
}

func TestTerragruntInputKey_Branches(t *testing.T) {
	// LiteralValueExpr with string
	k, ok := terragruntInputKey(&hclsyntax.LiteralValueExpr{Val: cty.StringVal("lit")})
	assert.True(t, ok)
	assert.Equal(t, "lit", k)

	// LiteralValueExpr with non-string
	k, ok = terragruntInputKey(&hclsyntax.LiteralValueExpr{Val: cty.NumberIntVal(123)})
	assert.False(t, ok)
	assert.Empty(t, k)

	// Template with interpolation
	file, diags := hclsyntax.ParseConfig([]byte(`inputs = { "${var.dyn}" = "val" }`), "t.hcl", hcl.Pos{Line: 1, Column: 1})
	require.False(t, diags.HasErrors())
	inputs := file.Body.(*hclsyntax.Body).Attributes["inputs"].Expr.(*hclsyntax.ObjectConsExpr)
	k, ok = terragruntInputKey(inputs.Items[0].KeyExpr)
	assert.False(t, ok)
	assert.Empty(t, k)
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

	// Unreadable subdirectory should be skipped without error
	sites, err := discoverCallSites(root, "wrapper")
	require.NoError(t, err)
	assert.Empty(t, sites)

	// Unreadable root searchDir should return an error
	_, err = discoverCallSites(blocked, "wrapper")
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
	}, nil)
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
	}, nil, nil, nil)
	assert.False(t, impact.CanSafelyUpdate)
	assert.Equal(t, "MEDIUM", impact.RiskScore)
}

func TestEvaluateCallSiteImpact_IncompleteAnalysisIsAlwaysMedium(t *testing.T) {
	impact := evaluateCallSiteImpact(CallSite{
		Dir:                     "/envs/dev",
		ModuleName:              "dev_cluster",
		InputAnalysisIncomplete: true,
	}, nil, &wrapper.WrapperCompatibilityReport{}, nil)
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

// TestEvaluateCallSiteImpact_HazardSurvivesWithoutMatchingWrapperIssue is the regression test
// for the false negative where a ForceNew hazard was only recorded when a wrapper audit issue
// happened to target the same input. A consumer freely passes its most dangerous inputs, and
// those are exactly the ones that never raise a wrapper issue.
func TestEvaluateCallSiteImpact_HazardSurvivesWithoutMatchingWrapperIssue(t *testing.T) {
	callSite := CallSite{
		Dir:        "/consumers/prod",
		File:       "/consumers/prod/main.tf",
		ModuleName: "primary_cluster",
		PassedInputs: map[string]string{
			"project_id": "acme-prod",
			"name":       "prod-us-central1",
		},
	}

	registry := recreation.NewDynamicHazardRegistry(nil)
	registry.Register("name", recreation.HazardDescriptor{Impact: "Resource names are immutable and force replacement"})
	registry.Register("project_id", recreation.HazardDescriptor{Impact: "Project ID is immutable and force replacement"})

	impact := evaluateCallSiteImpact(callSite, nil, &wrapper.WrapperCompatibilityReport{
		TargetModuleName: "gke",
		// A clean audit: the upstream still accepts every input the consumer passes.
		Issues:        []wrapper.WrapperIssue{},
		TotalBreaking: 0,
		Verdict:       "SAFE_TO_PIN",
	}, registry)

	assert.Len(t, impact.RecreationHazards, 2,
		"project_id and name are immutable GKE attributes and must be reported")
	assert.Contains(t, impact.RecreationHazards[0], "name")
	assert.Contains(t, impact.RecreationHazards[1], "project_id")
	assert.False(t, impact.CanSafelyUpdate)
	assert.Equal(t, "CRITICAL", impact.RiskScore)
}

// TestEvaluateCallSiteImpact_HazardReportedOncePerInput confirms a single hazard entry per
// input, which the removed issue-matching loop could duplicate.
func TestEvaluateCallSiteImpact_MissingRequiredNotAttributedWhenUnexposed(t *testing.T) {
	callSite := CallSite{
		Dir:          "/consumers/prod",
		File:         "/consumers/prod/main.tf",
		ModuleName:   "primary_cluster",
		PassedInputs: map[string]string{},
	}
	audit := &wrapper.WrapperCompatibilityReport{
		TargetModuleName: "gke",
		Issues: []wrapper.WrapperIssue{
			{Category: "MissingRequiredInput", Target: "secret_input", Severity: diff.SeverityBreaking},
		},
	}
	wrapperMod := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{}}
	impact := evaluateCallSiteImpact(callSite, wrapperMod, audit, nil)
	assert.NotContains(t, impact.MissingRequiredInputs, "secret_input")
}

func TestEvaluateCallSiteImpact_AttributeChangedInAudit(t *testing.T) {
	audit := &wrapper.WrapperCompatibilityReport{
		TargetModuleName: "gke",
		Issues:           []wrapper.WrapperIssue{{Target: "foo"}},
		UpstreamDiff: &diff.DiffReport{
			BreakingChanges: []diff.DifferenceItem{{Name: "bar"}},
			Warnings:        []diff.DifferenceItem{{Name: "baz"}},
			NewFeatures:     []diff.DifferenceItem{{Name: "qux"}},
		},
	}
	assert.True(t, attributeChangedInAudit(audit, "foo"))
	assert.True(t, attributeChangedInAudit(audit, "bar"))
	assert.True(t, attributeChangedInAudit(audit, "baz"))
	assert.True(t, attributeChangedInAudit(audit, "qux"))
	assert.False(t, attributeChangedInAudit(audit, "nope"))
	assert.False(t, attributeChangedInAudit(nil, "foo"))
}

func TestEvaluateCallSiteImpact_HazardReportedOncePerInput(t *testing.T) {
	impact := evaluateCallSiteImpact(CallSite{
		PassedInputs: map[string]string{"name": "prod"},
	}, nil, &wrapper.WrapperCompatibilityReport{
		TargetModuleName: "gke",
		Issues: []wrapper.WrapperIssue{
			{Category: "MissingRequiredInput", Target: "name"},
			{Category: "TypeMismatch", Target: "name"},
		},
	}, nil)
	assert.Len(t, impact.RecreationHazards, 1)
}

// parseTerragrunt reads a config from dir and returns the resolved call site, which is what the
// tests below assert against. Going through the parser directly rather than ScanDirectory keeps
// the assertions focused on input evaluation rather than on the directory walk.
func parseTerragrunt(t *testing.T, path, target string) CallSite {
	t.Helper()
	src, err := os.ReadFile(path)
	require.NoError(t, err)
	site, ok := parseTerragruntCallSite(src, path, target)
	require.True(t, ok, "the config must be recognised as a call site")
	return site
}

// TestTerragruntDynamicInputsAreEvaluated is the regression test for roadmap #14. Dynamic inputs
// were evaluated against an empty context with no Terragrunt built-ins, so the canonical
// `merge(local.defaults, {...})` idiom always failed and every realistic Terragrunt consumer was
// reported as fully unanalysed.
func TestTerragruntDynamicInputsAreEvaluated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terragrunt.hcl")
	config := `
locals {
  region = "us-central1"
  defaults = {
    region        = local.region
    removed_input = "legacy"
    cluster_name  = "prod"
  }
}

terraform {
  source = "../modules/gke_wrapper"
}

inputs = merge(local.defaults, {
  cluster_name = "prod-override"
})
`
	require.NoError(t, os.WriteFile(path, []byte(config), 0600))

	site := parseTerragrunt(t, path, "gke_wrapper")
	assert.False(t, site.InputAnalysisIncomplete,
		"a config whose locals resolve must not be reported as unanalysed")
	assert.Equal(t, `"prod-override"`, site.PassedInputs["cluster_name"],
		"merge must let the explicit object override the locals default")
	assert.Equal(t, `"us-central1"`, site.PassedInputs["region"],
		"a local referencing another local must resolve")
	assert.Equal(t, `"legacy"`, site.PassedInputs["removed_input"])
}

// TestTerragruntInputsFromIncludeChainParents covers a root config supplying the locals a child
// merges, which is how a real Terragrunt monorepo is laid out.
func TestTerragruntInputsFromIncludeChainParents(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root.hcl")
	require.NoError(t, os.WriteFile(root, []byte(`
locals {
  defaults = {
    region       = "us-east1"
    cluster_name = "from-root"
  }
}
`), 0600))

	childDir := filepath.Join(dir, "live")
	require.NoError(t, os.MkdirAll(childDir, 0750))
	child := filepath.Join(childDir, "terragrunt.hcl")
	require.NoError(t, os.WriteFile(child, []byte(`
include {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "../modules/gke_wrapper"
}

inputs = merge(local.defaults, { cluster_name = "child" })
`), 0600))

	site := parseTerragrunt(t, child, "gke_wrapper")
	assert.False(t, site.InputAnalysisIncomplete)
	assert.Equal(t, `"child"`, site.PassedInputs["cluster_name"])
	assert.Equal(t, `"us-east1"`, site.PassedInputs["region"],
		"a local declared in the include parent must be visible to the child")
}

// TestTerragruntChildLocalsOverrideParentLocals pins Terragrunt's precedence: a child's own local
// wins over one it would otherwise inherit.
func TestTerragruntChildLocalsOverrideParentLocals(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "root.hcl"), []byte(`
locals {
  region = "us-east1"
}
`), 0600))

	path := filepath.Join(dir, "terragrunt.hcl")
	require.NoError(t, os.WriteFile(path, []byte(`
locals {
  region = "europe-west1"
}

include {
  path = "root.hcl"
}

terraform {
  source = "../modules/gke_wrapper"
}

inputs = merge({}, { region = local.region })
`), 0600))

	site := parseTerragrunt(t, path, "gke_wrapper")
	assert.False(t, site.InputAnalysisIncomplete)
	assert.Equal(t, `"europe-west1"`, site.PassedInputs["region"],
		"the child's own local must win over the inherited one")
}

// TestTerragruntInputsUsingGetEnv covers get_env, including its default argument.
func TestTerragruntInputsUsingGetEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terragrunt.hcl")
	require.NoError(t, os.WriteFile(path, []byte(`
locals {
  cluster_name = get_env("TFASTDIFF_CLUSTER_NAME", "fallback")
  region       = get_env("TFASTDIFF_UNSET_VAR", "default-region")
}

terraform {
  source = "../modules/gke_wrapper"
}

inputs = merge({}, {
  cluster_name = local.cluster_name
  region       = local.region
})
`), 0600))

	t.Setenv("TFASTDIFF_CLUSTER_NAME", "from-env")

	site := parseTerragrunt(t, path, "gke_wrapper")
	assert.False(t, site.InputAnalysisIncomplete)
	assert.Equal(t, `"from-env"`, site.PassedInputs["cluster_name"],
		"get_env must read the environment when the variable is set")
	assert.Equal(t, `"default-region"`, site.PassedInputs["region"],
		"get_env must fall back to its default when the variable is unset")
}

// TestTerragruntStaticInputsKeepSourceText documents the other branch: a literal inputs object is
// recorded as written, without evaluation. Blast radius only compares input names against the
// wrapper, so the raw text is both sufficient and more faithful to what the file says.
func TestTerragruntStaticInputsKeepSourceText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terragrunt.hcl")
	require.NoError(t, os.WriteFile(path, []byte(`
terraform {
  source = "../modules/gke_wrapper"
}

inputs = {
  cluster_name = get_env("TFASTDIFF_CLUSTER_NAME", "fallback")
}
`), 0600))

	site := parseTerragrunt(t, path, "gke_wrapper")
	assert.False(t, site.InputAnalysisIncomplete)
	assert.Equal(t, `get_env("TFASTDIFF_CLUSTER_NAME", "fallback")`, site.PassedInputs["cluster_name"],
		"a literal inputs object records the expression as written")
}

// TestTerragruntPathFunctions covers the directory helpers that depend on the include relationship.
func TestTerragruntPathFunctions(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "envs", "prod")
	require.NoError(t, os.MkdirAll(nested, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "root.hcl"), []byte("locals { x = 1 }\n"), 0600))

	path := filepath.Join(nested, "terragrunt.hcl")
	require.NoError(t, os.WriteFile(path, []byte(`
include {
  path = find_in_parent_folders("root.hcl")
}

locals {
  here  = get_terragrunt_dir()
  there = get_parent_terragrunt_dir()
  rel   = path_relative_to_include()
}

terraform {
  source = "../modules/gke_wrapper"
}

inputs = merge({}, {
  dir    = local.here
  parent = local.there
  rel    = local.rel
})
`), 0600))

	site := parseTerragrunt(t, path, "gke_wrapper")
	assert.False(t, site.InputAnalysisIncomplete)
	assert.Equal(t, strconv.Quote(nested), site.PassedInputs["dir"])
	assert.Equal(t, strconv.Quote(dir), site.PassedInputs["parent"])
	assert.Equal(t, `"envs/prod"`, site.PassedInputs["rel"])
}

// TestTerragruntReadTerragruntConfig covers reading another config's locals by path.
func TestTerragruntReadTerragruntConfig(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "shared.hcl"), []byte(`
locals {
  region = "ap-south1"
}
`), 0600))

	path := filepath.Join(dir, "terragrunt.hcl")
	require.NoError(t, os.WriteFile(path, []byte(`
terraform {
  source = "../modules/gke_wrapper"
}

inputs = merge({}, { region = read_terragrunt_config("shared.hcl").region })
`), 0600))

	site := parseTerragrunt(t, path, "gke_wrapper")
	assert.False(t, site.InputAnalysisIncomplete)
	assert.Equal(t, `"ap-south1"`, site.PassedInputs["region"])
}

// TestTerragruntUnresolvableInputsStayIncomplete confirms the honest failure mode survives: an
// input depending on something unknowable offline is reported as unanalysed rather than guessed.
func TestTerragruntUnresolvableInputsStayIncomplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terragrunt.hcl")
	require.NoError(t, os.WriteFile(path, []byte(`
terraform {
  source = "../modules/gke_wrapper"
}

inputs = merge(local.defaults, { cluster_name = local.missing })
`), 0600))

	site := parseTerragrunt(t, path, "gke_wrapper")
	assert.True(t, site.InputAnalysisIncomplete,
		"an unresolvable reference must be reported, not silently treated as resolved")
}

// TestTerragruntUnreachableIncludeStaysIncomplete covers an include whose parent cannot be found.
func TestTerragruntUnreachableIncludeStaysIncomplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terragrunt.hcl")
	require.NoError(t, os.WriteFile(path, []byte(`
include {
  path = find_in_parent_folders("absent.hcl")
}

terraform {
  source = "../modules/gke_wrapper"
}

inputs = merge(local.defaults, { cluster_name = "x" })
`), 0600))

	site := parseTerragrunt(t, path, "gke_wrapper")
	assert.True(t, site.InputAnalysisIncomplete)
}

// TestScanDirectory_TerragruntFixtureEndToEnd runs the committed terragrunt.hcl fixtures through the
// real scanner. Roadmap #14 asked for one: the package had no Terragrunt fixture at all, so the
// ecosystem the blast-radius feature exists to serve was entirely untested.
func TestScanDirectory_TerragruntFixtureEndToEnd(t *testing.T) {
	fixtures := filepath.Join("..", "..", "test", "fixtures", "terragrunt_env")

	// The fixtures call the gke wrapper and pass ip_range_services, which v30 removed.
	audit := &wrapper.WrapperCompatibilityReport{
		TargetModuleName: "gke",
		Issues: []wrapper.WrapperIssue{
			{Category: "PassedRemovedInput", Target: "ip_range_services", Severity: diff.SeverityBreaking},
			{Category: "MissingRequiredInput", Target: "grant_registry_access", Severity: diff.SeverityBreaking},
		},
	}

	report, err := ScanDirectory(fixtures, "gke_wrapper", nil, audit)
	require.NoError(t, err)
	assert.Equal(t, 2, report.TotalCallSitesScanned, "both environments must be discovered")

	for _, impact := range report.ImpactedRootModules {
		assert.False(t, impact.AnalysisIncomplete,
			"%s must resolve its inputs through the include chain and locals", impact.Dir)
		assert.False(t, impact.CanSafelyUpdate,
			"%s passes a removed input", impact.Dir)
		assert.Contains(t, impact.RemovedInputsPassed, "ip_range_services",
			"the dynamic expression must resolve far enough to see ip_range_services")
	}

	// Only the prod fixture omits the newly required input; dev sets it explicitly.
	var prod, dev *RootModuleImpact
	for i := range report.ImpactedRootModules {
		switch filepath.Base(report.ImpactedRootModules[i].Dir) {
		case "prod":
			prod = &report.ImpactedRootModules[i]
		case "dev":
			dev = &report.ImpactedRootModules[i]
		}
	}
	require.NotNil(t, prod, "the prod environment must be discovered")
	require.NotNil(t, dev, "the dev environment must be discovered")
	assert.Contains(t, prod.MissingRequiredInputs, "grant_registry_access")
	assert.NotContains(t, dev.MissingRequiredInputs, "grant_registry_access",
		"dev supplies it, so it must not be reported as missing")

	// ip_range_services is a GKE immutable attribute, so both consumers are also hazard-flagged.
	assert.Equal(t, "CRITICAL", report.HighestRisk)
}

// TestParseTerragruntFixtureOverridesLocal pins that a child's own locals win, using the committed
// dev fixture which declares name locally.
func TestParseTerragruntFixtureOverridesLocal(t *testing.T) {
	path := filepath.Join("..", "..", "test", "fixtures", "terragrunt_env", "dev", "terragrunt.hcl")

	site := parseTerragrunt(t, path, "gke_wrapper")
	assert.False(t, site.InputAnalysisIncomplete)
	assert.Equal(t, `"dev-cluster"`, site.PassedInputs["name"],
		"the dev fixture declares its own name local, which must win over the inherited one")
	assert.Equal(t, `"us-central1"`, site.PassedInputs["region"],
		"region still comes from the root config's defaults")
	assert.Equal(t, "false", site.PassedInputs["grant_registry_access"])
}

// TestParseTerragruntFixtureNestedObjectInput confirms a structured input value survives evaluation,
// since a real wrapper passes objects for things like autoscaling profiles.
func TestParseTerragruntFixtureNestedObjectInput(t *testing.T) {
	path := filepath.Join("..", "..", "test", "fixtures", "terragrunt_env", "prod", "terragrunt.hcl")

	site := parseTerragrunt(t, path, "gke_wrapper")
	assert.False(t, site.InputAnalysisIncomplete)

	profile := site.PassedInputs["cluster_autoscaling_profile"]
	assert.Contains(t, profile, "enable_cpu_autoscaling")
	assert.Contains(t, profile, "prod")
	assert.Equal(t, `"10.2.0.0/20"`, site.PassedInputs["ip_range_services"])
	assert.NotContains(t, site.PassedInputs, "grant_registry_access",
		"the prod fixture deliberately omits it, so it must read as a missing required input")
}

func TestFilterTaintedCallSites(t *testing.T) {
	// nil report returns call sites as-is
	sites := []CallSite{
		{ModuleName: "site1"},
	}
	assert.Equal(t, sites, FilterTaintedCallSites(sites, nil))

	report := &eval.TaintReport{
		TargetVariable: "network_name",
		TracePath:      []string{"var.network_name", "local.net", "output.network_id"},
		AffectedResourceAttributes: []string{
			"google_compute_network.this.name",
			"output.network_self_link",
		},
	}

	callSites := []CallSite{
		// Site 1: passes tainted input directly
		{
			ModuleName:   "site1",
			PassedInputs: map[string]string{"network_name": `"custom-vpc"`},
		},
		// Site 2: references tainted output directly
		{
			ModuleName:        "site2",
			OutputsReferenced: []string{"network_id"},
		},
		// Site 3: passes input expression referencing tainted output
		{
			ModuleName:   "site3",
			PassedInputs: map[string]string{"vpc_id": "module.vpc.network_self_link"},
		},
		// Site 4: unrelated consumer
		{
			ModuleName:        "site4",
			PassedInputs:      map[string]string{"cluster_name": `"prod"`},
			OutputsReferenced: []string{"cluster_ca_certificate"},
		},
	}

	filtered := FilterTaintedCallSites(callSites, report)
	require.Len(t, filtered, 3)
	assert.Equal(t, "site1", filtered[0].ModuleName)
	assert.Equal(t, "site2", filtered[1].ModuleName)
	assert.Equal(t, "site3", filtered[2].ModuleName)
}

func TestScanDirectoryWithTaint(t *testing.T) {
	dir := t.TempDir()
	c1Dir := filepath.Join(dir, "consumer1")
	c2Dir := filepath.Join(dir, "consumer2")
	require.NoError(t, os.Mkdir(c1Dir, 0o750))
	require.NoError(t, os.Mkdir(c2Dir, 0o750))

	// c1 passes tainted input
	c1TF := `
module "gke" {
  source = "./modules/gke"
  network_name = "custom"
}
`
	// c2 passes unrelated input
	c2TF := `
module "gke" {
  source = "./modules/gke"
  cluster_name = "test"
}
`
	require.NoError(t, os.WriteFile(filepath.Join(c1Dir, "main.tf"), []byte(c1TF), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(c2Dir, "main.tf"), []byte(c2TF), 0o600))

	reportTainted := &eval.TaintReport{
		TargetVariable: "network_name",
		TracePath:      []string{"var.network_name"},
	}

	// Scan with taint filter
	res, err := ScanDirectoryWithTaint(dir, "./modules/gke", nil, nil, nil, reportTainted)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 1, res.TotalCallSitesScanned)
	assert.Contains(t, res.Summary, "filtered to 1 tainted by var.network_name")

	// Scan with taint report matching 0 consumers
	reportUnmatched := &eval.TaintReport{
		TargetVariable: "non_existent",
	}
	res0, err := ScanDirectoryWithTaint(dir, "./modules/gke", nil, nil, nil, reportUnmatched)
	require.NoError(t, err)
	assert.Equal(t, 0, res0.TotalCallSitesScanned)
	assert.Contains(t, res0.Summary, "0 consumers are reachable from tainted variable var.non_existent")

	// Scan with breaking audit and taint
	audit := &wrapper.WrapperCompatibilityReport{
		TargetModuleName: "gke",
		Issues: []wrapper.WrapperIssue{
			{
				Category: "PassedRemovedInput",
				Target:   "network_name",
			},
		},
	}
	resBreaking, err := ScanDirectoryWithTaint(dir, "./modules/gke", nil, audit, nil, reportTainted)
	require.NoError(t, err)
	assert.False(t, resBreaking.CanSafelyUpdateAll)
	assert.Contains(t, resBreaking.Summary, "detected breaking impacts")
}

func TestScanDirectory_Determinism(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 16; i++ {
		sub := filepath.Join(root, "env-"+strconv.Itoa(i))
		require.NoError(t, os.MkdirAll(sub, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(sub, "main.tf"), []byte(`
module "gke_a" {
  source = "../modules/gke_wrapper"
  name   = "a"
}
module "gke_b" {
  source = "../modules/gke_wrapper"
  name   = "b"
}
`), 0o600))
	}

	first, err := ScanDirectory(root, "gke_wrapper", nil, &wrapper.WrapperCompatibilityReport{})
	require.NoError(t, err)
	firstJSON := string(first.RenderJSON())

	for run := 0; run < 20; run++ {
		rep, err := ScanDirectory(root, "gke_wrapper", nil, &wrapper.WrapperCompatibilityReport{})
		require.NoError(t, err)
		assert.Equal(t, firstJSON, string(rep.RenderJSON()))
	}
}

func BenchmarkScanDirectory(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 30; i++ {
		sub := filepath.Join(root, "env-"+strconv.Itoa(i))
		_ = os.MkdirAll(sub, 0o750)
		_ = os.WriteFile(filepath.Join(sub, "main.tf"), []byte(`
module "gke" {
  source = "../modules/gke_wrapper"
  name   = "cluster"
}
`), 0o600)
		_ = os.WriteFile(filepath.Join(sub, "resources.tf"), []byte(`
resource "null_resource" "noop" {}
`), 0o600)
	}
	audit := &wrapper.WrapperCompatibilityReport{
		TargetModuleName: "gke",
		Issues: []wrapper.WrapperIssue{
			{Category: "PassedRemovedInput", Target: "obsolete", Severity: diff.SeverityBreaking},
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ScanDirectory(root, "gke_wrapper", nil, audit)
	}
}
