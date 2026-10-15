package wrapper

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeWrapper_NilInputs(t *testing.T) {
	report := AnalyzeWrapper(nil, nil, nil, "1.0.0", "1.0.0")
	assert.NotNil(t, report)
	assert.True(t, report.CanSafelyUpdate)
	assert.Equal(t, "SAFE_TO_PIN", report.Verdict)
}

func TestAnalyzeWrapper_NonSemverVersion(t *testing.T) {
	report := AnalyzeWrapper(nil, nil, nil, "latest", "")
	assert.NotNil(t, report)
	assert.Equal(t, `version = "latest"`, report.RecommendedPin)

	reportEmpty := AnalyzeWrapper(nil, nil, nil, "", "")
	assert.NotNil(t, reportEmpty)
	assert.Equal(t, `version = ""`, reportEmpty.RecommendedPin)
}

func TestAnalyzeWrapper_BreakingIssues(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		Path: "/path/to/wrapper",
		ModuleCalls: map[string]ast.ModuleCallDef{
			"gke": {
				Name:         "gke",
				Source:       "terraform-google-modules/kubernetes-engine/google",
				PassedInputs: []string{"project_id", "obsolete_param"},
			},
		},
		Variables: map[string]ast.VariableDef{
			"project_id": {Name: "project_id", Type: "string"},
			"tags":       {Name: "tags", Type: "string"}, // type mismatch with map
		},
		Outputs: map[string]ast.OutputDef{
			"endpoint": {Name: "endpoint"},
		},
		WrapperOutputReferences: map[string]ast.OutputReference{
			"endpoint": {
				OutputName:   "endpoint",
				TargetModule: "gke",
				TargetOutput: "legacy_endpoint", // removed in upstream
			},
		},
	}

	targetUpstream := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"project_id": {
				Name:     "project_id",
				Type:     "string",
				Required: true,
			},
			"new_required_arg": {
				Name:       "new_required_arg",
				Type:       "string",
				Required:   true,
				HasDefault: false, // missing in wrapper call
			},
			"tags": {
				Name:       "tags",
				Type:       "map(string)", // mismatch with wrapper string
				HasDefault: true,
			},
			"optional_future_feature": {
				Name:       "optional_future_feature",
				Type:       "bool",
				HasDefault: true,
			},
		},
		Outputs: map[string]ast.OutputDef{
			"cluster_id": {Name: "cluster_id"},
		},
	}

	pinnedUpstream := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"project_id":     {Name: "project_id", Type: "string"},
			"obsolete_param": {Name: "obsolete_param", Type: "string"},
		},
		Outputs: map[string]ast.OutputDef{
			"legacy_endpoint": {Name: "legacy_endpoint"},
		},
	}

	report := AnalyzeWrapper(wrapperMod, targetUpstream, pinnedUpstream, "v30.0.0", "v29.0.0")

	assert.False(t, report.CanSafelyUpdate)
	assert.Equal(t, "BREAKING_CHANGES_DETECTED", report.Verdict)
	assert.Equal(t, 4, report.TotalBreaking)
	assert.NotEmpty(t, report.RemediationSteps)
	assert.Equal(t, `version = "30.0.0"`, report.RecommendedPin)
	assert.Equal(t, "v29.0.0", report.SafePinVersion)
	assert.Contains(t, report.SafePinRationale, "currently pinned")
	assert.NotNil(t, report.UpstreamDiff)
	assert.NotEmpty(t, report.NewFeatureAdoptions)
	assert.Contains(t, report.Summary, "CANNOT SAFELY PIN")
	for _, issue := range report.Issues {
		if issue.Category == "TypeMismatch" {
			assert.Equal(t, 8, issue.ConfidenceScore)
		} else {
			assert.Equal(t, 9, issue.ConfidenceScore)
		}
	}
	assert.Equal(t, 9, report.NewFeatureAdoptions[0].ConfidenceScore)
}

func TestAnalyzeWrapper_SafeWithWarnings(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		ModuleCalls: map[string]ast.ModuleCallDef{
			"eks": {
				Name:         "eks",
				PassedInputs: []string{"zones", "subnets"},
			},
		},
		Variables: map[string]ast.VariableDef{
			"zones":   {Name: "zones", Type: "list(string)"},
			"subnets": {Name: "subnets", Type: "list(string)"},
		},
	}

	targetUpstream := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"zones":   {Name: "zones", Type: "set(string)", HasDefault: true},
			"subnets": {Name: "subnets", Type: "set(string)", HasDefault: true},
		},
	}

	report := AnalyzeWrapper(wrapperMod, targetUpstream, nil, "20.0.0", "19.0.0")
	assert.True(t, report.CanSafelyUpdate)
	assert.Equal(t, "SAFE_WITH_WARNINGS", report.Verdict)
	assert.Equal(t, 2, report.TotalWarnings)
	assert.Contains(t, report.Summary, "CAN SAFELY PIN WITH WARNINGS")
	assert.Equal(t, "20.0.0", report.SafePinVersion)
}

func TestAnalyzeWrapper_NullabilityMismatch(t *testing.T) {
	upstreamNullable := false
	report := AnalyzeWrapper(
		&ast.ModuleInfo{
			Variables: map[string]ast.VariableDef{
				"name": {Name: "name", Type: "string"},
			},
		},
		&ast.ModuleInfo{
			Variables: map[string]ast.VariableDef{
				"name": {Name: "name", Type: "string", Nullable: &upstreamNullable},
			},
		},
		nil,
		"v2.0.0",
		"v1.0.0",
	)

	require.False(t, report.CanSafelyUpdate)
	require.Equal(t, 1, report.TotalBreaking)
	assert.Equal(t, "VariableNullability", report.Issues[0].Category)
	assert.Equal(t, 9, report.Issues[0].ConfidenceScore)
}

func TestAnalyzeWrapper_WithoutPinnedVersionRecommendsNoSafePin(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		ModuleCalls: map[string]ast.ModuleCallDef{
			"gke": {Name: "gke", PassedInputs: []string{"obsolete"}},
		},
	}
	target := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{}}

	report := AnalyzeWrapper(wrapperMod, target, nil, "v30.0.0", "")
	require.False(t, report.CanSafelyUpdate)
	assert.Equal(t, "BREAKING_CHANGES_DETECTED", report.Verdict)
	assert.Empty(t, report.SafePinVersion)
	assert.Equal(t, "No current pin was supplied, so there is no verified safe version to recommend.", report.SafePinRationale)
}

func TestAnalyzeWrapper_NilInputsAreTolerated(t *testing.T) {
	report := AnalyzeWrapper(nil, nil, nil, "v1.0.0", "v0.9.0")
	require.NotNil(t, report)
	assert.Empty(t, report.TargetModuleName)
	assert.Equal(t, "SAFE_TO_PIN", report.Verdict)
	assert.True(t, report.CanSafelyUpdate)
	assert.Equal(t, `version = "1.0.0"`, report.RecommendedPin)
}

func TestAnalyzeWrapper_EmptyVersionKeepsRawPin(t *testing.T) {
	report := AnalyzeWrapper(&ast.ModuleInfo{}, &ast.ModuleInfo{}, nil, "latest", "")
	assert.Equal(t, `version = "latest"`, report.RecommendedPin)
}
