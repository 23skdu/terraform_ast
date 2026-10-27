package recreation

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
	"github.com/stretchr/testify/assert"
)

func TestLookupHazard(t *testing.T) {
	// GKE matches
	desc, ok := LookupHazard("terraform-google-modules/kubernetes-engine/google", "ip_range_pods")
	assert.True(t, ok)
	assert.Equal(t, "google_container_cluster / node_pools", desc.Resource)

	// EKS matches
	desc, ok = LookupHazard("terraform-aws-modules/eks/aws", "vpc_id")
	assert.True(t, ok)
	assert.Equal(t, "aws_eks_cluster", desc.Resource)

	// AKS matches
	desc, ok = LookupHazard("Azure/aks/azurerm", "dns_prefix")
	assert.True(t, ok)
	assert.Equal(t, "azurerm_kubernetes_cluster", desc.Resource)

	// No cross-provider bleed: an unclassified module asserts no static hazard, because the
	// tables name a specific resource type and naming the wrong one is a false positive.
	_, ok = LookupHazard("custom-module", "cluster_name")
	assert.False(t, ok)
	_, ok = LookupHazard("gke", "vpc_id")
	assert.False(t, ok, "vpc_id is an EKS attribute and must not be attributed to a GKE module")

	// Unknown
	_, ok = LookupHazard("custom-module", "some_random_var")
	assert.False(t, ok)
}

// TestMatchModuleCategory pins the classification. A raw substring test classified "checks" as
// AWS EKS because it contains "eks".
func TestMatchModuleCategory(t *testing.T) {
	for _, tc := range []struct {
		module string
		want   string
	}{
		{"terraform-google-modules/kubernetes-engine/google", "gke"},
		{"gke", "gke"},
		{"my_gke_wrapper", "gke"},
		{"gkeWrapper", "gke"},
		{"terraform-aws-modules/eks/aws", "eks"},
		{"my-eks-wrapper", "eks"},
		{"Azure/aks/azurerm", "aks"},
		{"azurerm_aks_wrapper", "aks"},
		{"custom-module", ""},
		{"checks", ""},
		{"weekends", ""},
		{"index", ""},
		{"", ""},
	} {
		assert.Equal(t, tc.want, matchModuleCategory(tc.module),
			"classifying %q", tc.module)
	}
}

// TestLookupHazard_ProviderSpecificAttribution confirms a classified module resolves only its own
// table, and that the GKE table still reports its own hazards.
func TestLookupHazard_ProviderSpecificAttribution(t *testing.T) {
	desc, ok := LookupHazard("gke", "ip_range_pods")
	assert.True(t, ok)
	assert.Equal(t, "google_container_cluster / node_pools", desc.Resource)

	desc, ok = LookupHazard("eks", "cluster_name")
	assert.True(t, ok)
	assert.Equal(t, "aws_eks_cluster", desc.Resource)

	_, ok = LookupHazard("aks", "ip_range_pods")
	assert.False(t, ok, "a GKE CIDR attribute must not be reported for an AKS module")
}

func TestAnalyzeHazards(t *testing.T) {
	// Nil check
	assert.Nil(t, AnalyzeHazards(nil))

	report := &diff.DiffReport{
		ModuleName: "terraform-google-modules/kubernetes-engine/google",
		BreakingChanges: []diff.DifferenceItem{
			{
				Category:    "Variable",
				Name:        "ip_range_services",
				Description: "Input variable removed",
			},
			{
				Category:    "Variable",
				Name:        "random_unrelated",
				Description: "Unrelated variable removed",
			},
		},
		Warnings: []diff.DifferenceItem{
			{
				Category:    "VariableType",
				Name:        "location",
				Description: "Widened",
			},
		},
	}

	hazards := AnalyzeHazards(report)
	assert.Len(t, hazards, 2)
	assert.Equal(t, "ip_range_services", hazards[0].Variable)
	assert.Equal(t, "CRITICAL_HAZARD", hazards[0].Severity)
	assert.Equal(t, "location", hazards[1].Variable)
	assert.Equal(t, "WARNING_HAZARD", hazards[1].Severity)

	md := FormatHazardsMarkdown(hazards)
	assert.Contains(t, md, "State Destruction & Recreation Hazards")
	assert.Contains(t, md, "ip_range_services")

	// Empty hazards markdown
	assert.Empty(t, FormatHazardsMarkdown(nil))
}

func TestAnalyzeWrapperHazards(t *testing.T) {
	assert.Nil(t, AnalyzeWrapperHazards(nil))

	audit := &wrapper.WrapperCompatibilityReport{
		TargetModuleName: "terraform-aws-modules/eks/aws",
		Issues: []wrapper.WrapperIssue{
			{
				Category:    "BreakingChange",
				Target:      "vpc_id",
				Description: "Changed",
			},
		},
	}

	hazards := AnalyzeWrapperHazards(audit)
	assert.Len(t, hazards, 1)
	assert.Equal(t, "vpc_id", hazards[0].Variable)
	assert.Contains(t, hazards[0].Description, "Wrapper issue on immutable variable")

	auditUpstream := &wrapper.WrapperCompatibilityReport{
		TargetModuleName: "terraform-aws-modules/eks/aws",
		UpstreamDiff: &diff.DiffReport{
			BreakingChanges: []diff.DifferenceItem{
				{Category: "VariableDefault", Name: "vpc_id"},
				{Category: "VariableDefault", Name: "not_a_hazard"},
			},
			Warnings: []diff.DifferenceItem{
				{Category: "VariableDefault", Name: "location"},
				{Category: "VariableDefault", Name: "not_a_hazard_2"},
			},
		},
	}
	hazardsUp := AnalyzeWrapperHazardsWithRegistry(auditUpstream, nil)
	assert.NotEmpty(t, hazardsUp)
}

func TestFormatHazardsText(t *testing.T) {
	assert.Equal(t, "", FormatHazardsText(nil))
	assert.Equal(t, "", FormatHazardsText([]RecreationHazard{}))

	text := FormatHazardsText([]RecreationHazard{
		{
			Variable:    "location",
			Resource:    "google_container_cluster",
			HazardType:  "VariableType",
			Severity:    "WARNING_HAZARD",
			Description: "Location is immutable",
			Remediation: "Keep location unchanged",
		},
		{
			Variable: "project_id",
			Resource: "google_container_cluster",
			Severity: "CRITICAL_HAZARD",
		},
	})

	assert.Contains(t, text, "STATE DESTRUCTION & RECREATION HAZARDS (ForceNew)")
	assert.Contains(t, text, "[WARNING_HAZARD] location (google_container_cluster)")
	assert.Contains(t, text, "Remediation: Keep location unchanged")
	assert.Contains(t, text, "[CRITICAL_HAZARD] project_id")
	assert.NotContains(t, text, "Remediation: \n")
}

// TestFormatHazards_RendersUnattributedResource covers a hazard that came from the
// provider-agnostic table. It has no resource type, and the formatters must say so rather than
// print an empty column or invent a provider.
func TestFormatHazards_RendersUnattributedResource(t *testing.T) {
	hazards := []RecreationHazard{
		{Variable: "location", Severity: "CRITICAL_HAZARD", Description: "region changed", Remediation: "keep it"},
	}

	text := FormatHazardsText(hazards)
	assert.Contains(t, text, "location (provider-agnostic resource)")

	md := FormatHazardsMarkdown(hazards)
	assert.Contains(t, md, "`provider-agnostic resource`")
	assert.NotContains(t, md, "()")

	assert.Equal(t, "google_container_cluster", displayResource(HazardDescriptor{Resource: "google_container_cluster"}))
}
