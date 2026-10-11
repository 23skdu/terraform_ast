package terratest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/blastradius"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/gitops"
	"github.com/23skdu/terraform_ast/pkg/planner"
	"github.com/23skdu/terraform_ast/pkg/recreation"
	"github.com/23skdu/terraform_ast/pkg/registry"
	"github.com/23skdu/terraform_ast/pkg/report"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTerratest_ValidateFixtures runs terraform init and validate on all test modules
// using Terratest to guarantee realistic and valid Terraform syntax.
func TestTerratest_ValidateFixtures(t *testing.T) {
	fixtureDirs := []string{
		"../fixtures/upstream_gke_v1",
		"../fixtures/upstream_gke_v2",
		"../fixtures/gke_wrapper",
		"../fixtures/upstream_eks_v19",
		"../fixtures/upstream_eks_v20",
		"../fixtures/eks_wrapper",
		"../fixtures/downstream_consumer",
		"../fixtures/object_schema_v1",
		"../fixtures/object_schema_v2",
		"../fixtures/json_module",
	}

	for _, relDir := range fixtureDirs {
		dir, err := filepath.Abs(relDir)
		require.NoError(t, err)

		t.Run(filepath.Base(dir), func(t *testing.T) {
			opts := &terraform.Options{
				TerraformDir: dir,
				NoColor:      true,
			}
			t.Cleanup(func() {
				_ = os.RemoveAll(filepath.Join(dir, ".terraform"))
				_ = os.Remove(filepath.Join(dir, ".terraform.lock.hcl"))
			})
			terraform.InitAndValidate(t, opts)
		})
	}
}

func TestTerratest_TerraformJSONInspection(t *testing.T) {
	dir, err := filepath.Abs("../fixtures/json_module")
	require.NoError(t, err)
	opts := &terraform.Options{TerraformDir: dir, NoColor: true}
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Join(dir, ".terraform"))
		_ = os.Remove(filepath.Join(dir, ".terraform.lock.hcl"))
	})
	terraform.InitAndValidate(t, opts)

	info, err := ast.Inspect(dir)
	require.NoError(t, err)
	require.Contains(t, info.Variables, "value")
	require.Contains(t, info.ModuleCalls, "child")
	assert.Equal(t, []string{"input"}, info.ModuleCalls["child"].PassedInputs)
	require.Contains(t, info.WrapperOutputReferences, "child_id")
	assert.Equal(t, "id", info.WrapperOutputReferences["child_id"].TargetOutput)

	blastReport, err := blastradius.ScanDirectory(dir, filepath.Join(dir, "child"), nil, &wrapper.WrapperCompatibilityReport{
		Issues: []wrapper.WrapperIssue{
			{
				Category:    "Output",
				Target:      "id",
				Severity:    diff.SeverityBreaking,
				Description: "Output was removed",
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, blastReport.TotalCallSitesScanned)
	assert.Equal(t, "CRITICAL", blastReport.HighestRisk)
	assert.Equal(t, []string{"id"}, blastReport.ImpactedRootModules[0].RemovedOutputsReferenced)
}

// TestGKE_ASTDiffAndGolden verifies GKE upstream diff against golden images.
func TestGKE_ASTDiffAndGolden(t *testing.T) {
	gkeV1Dir, err := filepath.Abs("../fixtures/upstream_gke_v1")
	require.NoError(t, err)
	gkeV2Dir, err := filepath.Abs("../fixtures/upstream_gke_v2")
	require.NoError(t, err)

	modV1, err := ast.Inspect(gkeV1Dir)
	require.NoError(t, err)
	modV2, err := ast.Inspect(gkeV2Dir)
	require.NoError(t, err)

	diffReport := diff.Compare(modV1, modV2, "v29.0.0", "v30.0.0")
	diffReport.ModuleName = "terraform-google-modules/kubernetes-engine/google"

	// Verify breaking changes detected
	assert.False(t, diffReport.CanSafelyUpdate)
	assert.Equal(t, "BREAKING_CHANGES", diffReport.Verdict)

	// Check specific breaking requirements
	// 1. Removed input: ip_range_services
	// 2. Newly required input: cluster_autoscaling_profile
	// 3. Newly required (was optional): grant_registry_access
	// 4. Changed type constraint: ip_range_pods (string -> list(string))
	// 5. Removed output: legacy_network_tag
	assert.Equal(t, 5, diffReport.TotalBreaking)

	// Check new features: enable_cost_allocation (var), cost_allocation_status (output)
	assert.Equal(t, 2, diffReport.TotalNewFeatures)

	// Check warnings: zones widened to set(string), core requirement updated
	assert.Equal(t, 2, diffReport.TotalWarnings)

	// Golden file testing: JSON
	goldenJSONPath, err := filepath.Abs("../golden/gke_upstream_diff.golden.json")
	require.NoError(t, err)
	jsonData, err := report.RenderJSON(diffReport)
	require.NoError(t, err)
	report.AssertGoldenMatch(t, goldenJSONPath, jsonData, false)

	// Golden file testing: Markdown
	goldenMDPath, err := filepath.Abs("../golden/gke_upstream_diff.golden.md")
	require.NoError(t, err)
	mdData := []byte(report.RenderMarkdown(diffReport))
	report.AssertGoldenMatch(t, goldenMDPath, mdData, false)
}

// TestGKE_WrapperAuditAndGolden verifies GKE wrapper compatibility against golden images.
func TestGKE_WrapperAuditAndGolden(t *testing.T) {
	wrapperDir, err := filepath.Abs("../fixtures/gke_wrapper")
	require.NoError(t, err)
	gkeV1Dir, err := filepath.Abs("../fixtures/upstream_gke_v1")
	require.NoError(t, err)
	gkeV2Dir, err := filepath.Abs("../fixtures/upstream_gke_v2")
	require.NoError(t, err)

	wrapperMod, err := ast.Inspect(wrapperDir)
	require.NoError(t, err)
	gkeV1, err := ast.Inspect(gkeV1Dir)
	require.NoError(t, err)
	gkeV2, err := ast.Inspect(gkeV2Dir)
	require.NoError(t, err)

	wrapperAudit := wrapper.AnalyzeWrapper(wrapperMod, gkeV2, gkeV1, "v30.0.0", "v29.0.0")
	wrapperAudit.WrapperPath = "test/fixtures/gke_wrapper"

	// Verify wrapper cannot safely pin yet
	assert.False(t, wrapperAudit.CanSafelyUpdate)
	assert.Equal(t, "BREAKING_CHANGES_DETECTED", wrapperAudit.Verdict)

	// Breaking issues in wrapper:
	// 1. Wrapper passes removed input: ip_range_services
	// 2. Upstream requires new input without default: cluster_autoscaling_profile
	// 3. Upstream requires grant_registry_access which wrapper does not pass
	// 4. Wrapper variable ip_range_pods type (string) incompatible with upstream (list(string))
	// 5. Wrapper output legacy_network_tag delegates to removed upstream output
	assert.Equal(t, 5, wrapperAudit.TotalBreaking)
	assert.Equal(t, `version = "30.0.0"`, wrapperAudit.RecommendedPin)

	// Golden file testing: JSON
	goldenJSONPath, err := filepath.Abs("../golden/gke_wrapper_audit.golden.json")
	require.NoError(t, err)
	jsonData, err := report.RenderWrapperJSON(wrapperAudit)
	require.NoError(t, err)
	report.AssertGoldenMatch(t, goldenJSONPath, jsonData, false)

	// Golden file testing: Markdown
	goldenMDPath, err := filepath.Abs("../golden/gke_wrapper_audit.golden.md")
	require.NoError(t, err)
	mdData := []byte(report.RenderWrapperMarkdown(wrapperAudit))
	report.AssertGoldenMatch(t, goldenMDPath, mdData, false)
}

// TestEKS_ASTDiffAndGolden verifies AWS EKS upstream diff against golden images.
func TestEKS_ASTDiffAndGolden(t *testing.T) {
	eksV19Dir, err := filepath.Abs("../fixtures/upstream_eks_v19")
	require.NoError(t, err)
	eksV20Dir, err := filepath.Abs("../fixtures/upstream_eks_v20")
	require.NoError(t, err)

	modV19, err := ast.Inspect(eksV19Dir)
	require.NoError(t, err)
	modV20, err := ast.Inspect(eksV20Dir)
	require.NoError(t, err)

	diffReport := diff.Compare(modV19, modV20, "v19.0.0", "v20.0.0")
	diffReport.ModuleName = "terraform-aws-modules/eks/aws"

	assert.False(t, diffReport.CanSafelyUpdate)
	assert.Equal(t, "BREAKING_CHANGES", diffReport.Verdict)

	// 1. Removed inputs: manage_aws_auth_configmap, legacy_security_group_id
	// 2. Newly required input: authentication_mode
	// 3. Removed output: aws_auth_configmap_yaml
	assert.Equal(t, 4, diffReport.TotalBreaking)

	// New features: enable_cluster_creator_admin_permissions (var), access_entries (output)
	assert.Equal(t, 2, diffReport.TotalNewFeatures)

	// Golden file testing: JSON
	goldenJSONPath, err := filepath.Abs("../golden/eks_upstream_diff.golden.json")
	require.NoError(t, err)
	jsonData, err := report.RenderJSON(diffReport)
	require.NoError(t, err)
	report.AssertGoldenMatch(t, goldenJSONPath, jsonData, false)

	// Golden file testing: Markdown
	goldenMDPath, err := filepath.Abs("../golden/eks_upstream_diff.golden.md")
	require.NoError(t, err)
	mdData := []byte(report.RenderMarkdown(diffReport))
	report.AssertGoldenMatch(t, goldenMDPath, mdData, false)
}

// TestEKS_WrapperAuditAndGolden verifies AWS EKS wrapper compatibility against golden images.
func TestEKS_WrapperAuditAndGolden(t *testing.T) {
	wrapperDir, err := filepath.Abs("../fixtures/eks_wrapper")
	require.NoError(t, err)
	eksV19Dir, err := filepath.Abs("../fixtures/upstream_eks_v19")
	require.NoError(t, err)
	eksV20Dir, err := filepath.Abs("../fixtures/upstream_eks_v20")
	require.NoError(t, err)

	wrapperMod, err := ast.Inspect(wrapperDir)
	require.NoError(t, err)
	eksV19, err := ast.Inspect(eksV19Dir)
	require.NoError(t, err)
	eksV20, err := ast.Inspect(eksV20Dir)
	require.NoError(t, err)

	wrapperAudit := wrapper.AnalyzeWrapper(wrapperMod, eksV20, eksV19, "v20.0.0", "v19.0.0")
	wrapperAudit.WrapperPath = "test/fixtures/eks_wrapper"

	assert.False(t, wrapperAudit.CanSafelyUpdate)
	assert.Equal(t, "BREAKING_CHANGES_DETECTED", wrapperAudit.Verdict)

	// 1. Passed removed input: manage_aws_auth_configmap
	// 2. Passed removed input: legacy_security_group_id
	// 3. Missing required input: authentication_mode
	// 4. Broken output delegation: aws_auth references removed output
	assert.Equal(t, 4, wrapperAudit.TotalBreaking)
	assert.Equal(t, `version = "20.0.0"`, wrapperAudit.RecommendedPin)

	// Golden file testing: JSON
	goldenJSONPath, err := filepath.Abs("../golden/eks_wrapper_audit.golden.json")
	require.NoError(t, err)
	jsonData, err := report.RenderWrapperJSON(wrapperAudit)
	require.NoError(t, err)
	report.AssertGoldenMatch(t, goldenJSONPath, jsonData, false)

	// Golden file testing: Markdown
	goldenMDPath, err := filepath.Abs("../golden/eks_wrapper_audit.golden.md")
	require.NoError(t, err)
	mdData := []byte(report.RenderWrapperMarkdown(wrapperAudit))
	report.AssertGoldenMatch(t, goldenMDPath, mdData, false)
}

// TestTerratest_DeepObjectSchemaDiff verifies deep object schema diffing with nested attribute level checks.
func TestTerratest_DeepObjectSchemaDiff(t *testing.T) {
	v1Dir, err := filepath.Abs("../fixtures/object_schema_v1")
	require.NoError(t, err)
	v2Dir, err := filepath.Abs("../fixtures/object_schema_v2")
	require.NoError(t, err)

	modV1, err := ast.Inspect(v1Dir)
	require.NoError(t, err)
	modV2, err := ast.Inspect(v2Dir)
	require.NoError(t, err)

	diffReport := diff.Compare(modV1, modV2, "v1.0.0", "v2.0.0")
	assert.False(t, diffReport.CanSafelyUpdate)
	assert.Equal(t, "BREAKING_CHANGES", diffReport.Verdict)
	assert.GreaterOrEqual(t, diffReport.TotalBreaking, 1)

	// Verify deep object attribute incompatibility is detected
	var foundObjectBreaking bool
	for _, item := range diffReport.BreakingChanges {
		if item.Name == "node_config" && (strings.Contains(item.Description, "spot") || strings.Contains(item.Description, "tags")) {
			foundObjectBreaking = true
			assert.Contains(t, item.Description, "object schema")
		}

	}
	assert.True(t, foundObjectBreaking, "Expected breaking change detected for object schema variable node_config")
}

func TestTerratest_MultiVersionUpgradePathRecommendation(t *testing.T) {
	v1Dir, err := filepath.Abs("../fixtures/upstream_gke_v1")
	require.NoError(t, err)
	v2Dir, err := filepath.Abs("../fixtures/upstream_gke_v2")
	require.NoError(t, err)
	v1, err := ast.Inspect(v1Dir)
	require.NoError(t, err)
	v2, err := ast.Inspect(v2Dir)
	require.NoError(t, err)

	path, err := planner.AnalyzeUpgradePath([]planner.VersionSnapshot{
		{Version: "v29.0.0", Module: v1},
		{Version: "v30.0.0", Module: v2},
	})
	require.NoError(t, err)
	assert.False(t, path.CanSafelyUpdateAll)
	assert.Equal(t, "v29.0.0", path.RecommendedVersion)
	require.Len(t, path.Steps, 1)
	assert.Equal(t, "BREAKING_CHANGES", path.Steps[0].Diff.Verdict)
}

// TestTerratest_RecreationHazardsAndSARIF verifies detection of ForceNew destruction hazards and SARIF v2.1.0 generation.
func TestTerratest_RecreationHazardsAndSARIF(t *testing.T) {
	gkeV1Dir, err := filepath.Abs("../fixtures/upstream_gke_v1")
	require.NoError(t, err)
	gkeV2Dir, err := filepath.Abs("../fixtures/upstream_gke_v2")
	require.NoError(t, err)

	modV1, err := ast.Inspect(gkeV1Dir)
	require.NoError(t, err)
	modV2, err := ast.Inspect(gkeV2Dir)
	require.NoError(t, err)

	diffReport := diff.Compare(modV1, modV2, "v29.0.0", "v30.0.0")
	diffReport.ModuleName = "terraform-google-modules/kubernetes-engine/google"

	// 1. Recreation Hazards validation
	hazards := recreation.AnalyzeHazards(diffReport)
	assert.NotEmpty(t, hazards)

	var foundServicesHazard, foundPodsHazard bool
	for _, h := range hazards {
		if h.Variable == "ip_range_services" {
			foundServicesHazard = true
			assert.Equal(t, "google_container_cluster", h.Resource)
		}
		if h.Variable == "ip_range_pods" {
			foundPodsHazard = true
		}
	}
	assert.True(t, foundServicesHazard, "ip_range_services must be detected as ForceNew recreation hazard")
	assert.True(t, foundPodsHazard, "ip_range_pods must be detected as ForceNew recreation hazard")

	// 2. SARIF generation validation
	sarifBytes, err := report.RenderSARIF(diffReport)
	require.NoError(t, err)
	assert.Contains(t, string(sarifBytes), "sarif-schema-2.1.0.json")
	assert.Contains(t, string(sarifBytes), "TF-DIFF-BREAKING-CHANGE")

	// Golden file testing: SARIF
	goldenSARIFPath, err := filepath.Abs("../golden/gke_sarif.golden.json")
	require.NoError(t, err)
	report.AssertGoldenMatch(t, goldenSARIFPath, sarifBytes, false)
}

// TestTerratest_BlastRadiusConsumerAnalysis verifies downstream root module blast radius analysis.
func TestTerratest_BlastRadiusConsumerAnalysis(t *testing.T) {
	wrapperDir, err := filepath.Abs("../fixtures/gke_wrapper")
	require.NoError(t, err)
	consumerDir, err := filepath.Abs("../fixtures/downstream_consumer")
	require.NoError(t, err)
	gkeV1Dir, err := filepath.Abs("../fixtures/upstream_gke_v1")
	require.NoError(t, err)
	gkeV2Dir, err := filepath.Abs("../fixtures/upstream_gke_v2")
	require.NoError(t, err)

	wrapperMod, err := ast.Inspect(wrapperDir)
	require.NoError(t, err)
	gkeV1, err := ast.Inspect(gkeV1Dir)
	require.NoError(t, err)
	gkeV2, err := ast.Inspect(gkeV2Dir)
	require.NoError(t, err)

	wrapperAudit := wrapper.AnalyzeWrapper(wrapperMod, gkeV2, gkeV1, "v30.0.0", "v29.0.0")
	wrapperAudit.WrapperPath = "test/fixtures/gke_wrapper"

	blastReport, err := blastradius.ScanDirectory(consumerDir, "gke_wrapper", wrapperMod, wrapperAudit)
	require.NoError(t, err)
	assert.Equal(t, 1, blastReport.TotalCallSitesScanned)
	assert.False(t, blastReport.CanSafelyUpdateAll)
	assert.Equal(t, "CRITICAL", blastReport.HighestRisk)

	impact := blastReport.ImpactedRootModules[0]
	assert.Equal(t, "primary_cluster", impact.ModuleName)
	assert.Contains(t, impact.RemovedOutputsReferenced, "legacy_network_tag")

	// Golden file testing: Blast Radius JSON
	goldenBlastPath, err := filepath.Abs("../golden/blast_radius.golden.json")
	require.NoError(t, err)
	blastJSON, err := blastReport.RenderJSON()
	require.NoError(t, err)
	report.AssertGoldenMatch(t, goldenBlastPath, blastJSON, false)
}

// TestTerratest_GitShallowAndRegistryProtocols verifies shallow extraction and registry protocol detection.
func TestTerratest_GitShallowAndRegistryProtocols(t *testing.T) {
	// Registry source detection
	assert.True(t, registry.IsRegistrySource("terraform-google-modules/kubernetes-engine/google"))
	assert.True(t, registry.IsRegistrySource("terraform-aws-modules/eks/aws"))
	assert.False(t, registry.IsRegistrySource("./local/module"))
	assert.False(t, registry.IsRegistrySource("git@github.com:org/repo.git"))

	host, ns, name, prov, err := registry.ParseRegistrySource("terraform-google-modules/kubernetes-engine/google")
	require.NoError(t, err)
	assert.Equal(t, "registry.terraform.io", host)
	assert.Equal(t, "terraform-google-modules", ns)
	assert.Equal(t, "kubernetes-engine", name)
	assert.Equal(t, "google", prov)

	// Shallow extraction on local git fixture
	gkeV1Dir, err := filepath.Abs("../fixtures/upstream_gke_v1")
	require.NoError(t, err)

	destDir := t.TempDir()
	err = gitops.ExtractVersionToDirShallow(gkeV1Dir, "", destDir)
	require.NoError(t, err)
}

// TestTerratest_PlanFixtures executes terraform init and plan via Terratest on fixtures.
func TestTerratest_PlanFixtures(t *testing.T) {
	planDirs := []string{
		"../fixtures/object_schema_v1",
		"../fixtures/object_schema_v2",
		"../fixtures/downstream_consumer",
	}

	for _, relDir := range planDirs {
		dir, err := filepath.Abs(relDir)
		require.NoError(t, err)

		t.Run(filepath.Base(dir), func(t *testing.T) {
			opts := &terraform.Options{
				TerraformDir: dir,
				NoColor:      true,
			}
			if filepath.Base(dir) == "object_schema_v2" {
				opts.Vars = map[string]interface{}{
					"node_config": map[string]interface{}{
						"machine_type": "e2-medium",
						"disk_size_gb": 50,
						"tags":         []string{"web"},
						"spot":         true,
					},
				}
			}
			t.Cleanup(func() {
				_ = os.RemoveAll(filepath.Join(dir, ".terraform"))
				_ = os.Remove(filepath.Join(dir, ".terraform.lock.hcl"))
			})
			planOutput := terraform.InitAndPlan(t, opts)
			assert.NotEmpty(t, planOutput)
		})
	}
}

// TestTerratest_CLI_EndToEnd runs the compiled tfastdiff CLI binary against real Terratest fixtures.
func TestTerratest_CLI_EndToEnd(t *testing.T) {
	tmpBin := filepath.Join(t.TempDir(), "tfastdiff")
	cmdBuild := exec.Command("go", "build", "-o", tmpBin, "../../cmd/tfastdiff")
	require.NoError(t, cmdBuild.Run(), "Failed to build tfastdiff binary for Terratest E2E")

	gkeV1Dir, _ := filepath.Abs("../fixtures/upstream_gke_v1")
	gkeV2Dir, _ := filepath.Abs("../fixtures/upstream_gke_v2")
	gkeWrapperDir, _ := filepath.Abs("../fixtures/gke_wrapper")
	consumerDir, _ := filepath.Abs("../fixtures/downstream_consumer")

	// 1. Compare Mode: markdown output with state recreation hazard detection
	cmdCompare := exec.Command(tmpBin,
		"--old", gkeV1Dir,
		"--new", gkeV2Dir,
		"--format", "markdown",
		"--check-recreation=true",
	)
	outCompare, err := cmdCompare.CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(outCompare), "Terraform AST Compatibility Diff Report")
	assert.Contains(t, string(outCompare), "State Destruction & Recreation Hazards")

	// 1b. Preview-only codemod for removed wrapper arguments
	wrapperMainPath := filepath.Join(gkeWrapperDir, "main.tf")
	wrapperBefore, err := os.ReadFile(wrapperMainPath)
	require.NoError(t, err)
	cmdFixPreview := exec.Command(tmpBin,
		"--wrapper", gkeWrapperDir,
		"--upstream", gkeV2Dir,
		"--fix-preview",
	)
	outPreview, err := cmdFixPreview.CombinedOutput()
	require.NoError(t, err, string(outPreview))
	assert.Contains(t, string(outPreview), "--- a/main.tf")
	assert.Contains(t, string(outPreview), "ip_range_services")
	wrapperAfter, err := os.ReadFile(wrapperMainPath)
	require.NoError(t, err)
	assert.Equal(t, wrapperBefore, wrapperAfter, "preview mode must not modify Terraform files")

	// 2. Wrapper Mode: json output with blast-radius scanning
	cmdWrapper := exec.Command(tmpBin,
		"--wrapper", gkeWrapperDir,
		"--upstream", gkeV2Dir,
		"--blast-radius", consumerDir,
		"--format", "json",
	)
	outWrapper, err := cmdWrapper.CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(outWrapper), "can_safely_update")
	assert.Contains(t, string(outWrapper), "total_call_sites_scanned")

	// 3. Wrapper Mode with --exit-code enforcement: returns exit code 1 on breaking changes
	cmdExitCode := exec.Command(tmpBin,
		"--wrapper", gkeWrapperDir,
		"--upstream", gkeV2Dir,
		"--exit-code",
	)
	err = cmdExitCode.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
}

// TestTerratest_WrapperRecreationHazardsAndSARIF validates SARIF v2.1.0 reporting and hazard detection on wrappers.
func TestTerratest_WrapperRecreationHazardsAndSARIF(t *testing.T) {
	wrapperDir, err := filepath.Abs("../fixtures/gke_wrapper")
	require.NoError(t, err)
	gkeV1Dir, err := filepath.Abs("../fixtures/upstream_gke_v1")
	require.NoError(t, err)
	gkeV2Dir, err := filepath.Abs("../fixtures/upstream_gke_v2")
	require.NoError(t, err)

	wMod, err := ast.Inspect(wrapperDir)
	require.NoError(t, err)
	g1, err := ast.Inspect(gkeV1Dir)
	require.NoError(t, err)
	g2, err := ast.Inspect(gkeV2Dir)
	require.NoError(t, err)

	audit := wrapper.AnalyzeWrapper(wMod, g2, g1, "v30.0.0", "v29.0.0")

	// 1. AnalyzeWrapperHazards
	hazards := recreation.AnalyzeWrapperHazards(audit)
	assert.NotEmpty(t, hazards)
	formattedHazards := recreation.FormatHazardsMarkdown(hazards)
	assert.Contains(t, formattedHazards, "State Destruction & Recreation Hazards (ForceNew)")
	assert.Contains(t, formattedHazards, "google_container_cluster")

	// 2. RenderWrapperSARIF
	sarifBytes, err := report.RenderWrapperSARIF(audit)
	require.NoError(t, err)
	assert.Contains(t, string(sarifBytes), "sarif-schema-2.1.0.json")
	assert.Contains(t, string(sarifBytes), "TF-DIFF-BREAKING-CHANGE")
	assert.Contains(t, string(sarifBytes), "PassedRemovedInput")
}
