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

	// Generic fallback
	desc, ok = LookupHazard("custom-module", "cluster_name")
	assert.True(t, ok)
	assert.Equal(t, "aws_eks_cluster", desc.Resource)

	// Unknown
	_, ok = LookupHazard("custom-module", "some_random_var")
	assert.False(t, ok)
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
}
