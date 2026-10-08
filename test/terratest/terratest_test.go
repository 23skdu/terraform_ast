package terratest

import (
	"path/filepath"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/diff"
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
	}

	for _, relDir := range fixtureDirs {
		dir, err := filepath.Abs(relDir)
		require.NoError(t, err)

		t.Run(filepath.Base(dir), func(t *testing.T) {
			opts := &terraform.Options{
				TerraformDir: dir,
				NoColor:      true,
			}
			terraform.InitAndValidate(t, opts)
		})
	}
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
