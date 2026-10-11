package diff

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompare_NilInputs(t *testing.T) {
	report := Compare(nil, nil, "1.0.0", "1.0.0")
	assert.NotNil(t, report)
	assert.True(t, report.CanSafelyUpdate)
	assert.Equal(t, "SAFE", report.Verdict)
}

func TestCompare_Downgrade(t *testing.T) {
	report := Compare(nil, nil, "2.0.0", "1.0.0")
	assert.False(t, report.CanSafelyUpdate)
	assert.True(t, report.IsDowngrade)
	assert.Equal(t, "DOWNGRADE", report.Verdict)
	assert.Contains(t, report.Summary, "DOWNGRADE HAZARD")
}

func TestCompare_BreakingChanges(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"removed_var": {
				Name:     "removed_var",
				Type:     "string",
				Required: true,
			},
			"type_changed_var": {
				Name: "type_changed_var",
				Type: "string",
			},
			"became_required_var": {
				Name:       "became_required_var",
				Type:       "string",
				HasDefault: true,
				Required:   false,
			},
		},
		Outputs: map[string]ast.OutputDef{
			"removed_output": {
				Name: "removed_output",
			},
			"type_changed_output": {
				Name: "type_changed_output",
				Type: "string",
			},
		},
	}

	newMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"type_changed_var": {
				Name: "type_changed_var",
				Type: "number", // breaking type change
			},
			"became_required_var": {
				Name:       "became_required_var",
				Type:       "string",
				HasDefault: false,
				Required:   true, // now required without default
			},
			"newly_required_input": {
				Name:       "newly_required_input",
				Type:       "string",
				Required:   true,
				HasDefault: false, // newly required
			},
		},
		Outputs: map[string]ast.OutputDef{
			"type_changed_output": {
				Name: "type_changed_output",
				Type: "list(string)", // breaking output type
			},
		},
	}

	report := Compare(oldMod, newMod, "1.0.0", "2.0.0")
	assert.False(t, report.CanSafelyUpdate)
	assert.Equal(t, "BREAKING_CHANGES", report.Verdict)
	assert.Equal(t, 6, report.TotalBreaking)
	assert.Contains(t, report.Summary, "CANNOT SAFELY UPDATE")
}

func TestCompare_WarningsAndFeatures(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"widened_var": {
				Name: "widened_var",
				Type: "list(string)",
			},
			"sensitive_transition": {
				Name:      "sensitive_transition",
				Sensitive: false,
			},
			"deprecated_transition": {
				Name: "deprecated_transition",
			},
		},
		Outputs: map[string]ast.OutputDef{
			"output_sensitive_transition": {
				Name:      "output_sensitive_transition",
				Sensitive: false,
			},
			"output_deprecated_transition": {
				Name: "output_deprecated_transition",
			},
		},
		RequiredCore: []string{">= 1.0"},
		RequiredProviders: map[string]ast.ProviderRequirement{
			"google": {
				Name:               "google",
				VersionConstraints: []string{">= 4.0"},
			},
			"removed_provider": {
				Name:               "removed_provider",
				VersionConstraints: []string{">= 1.0"},
			},
		},
	}

	newMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"widened_var": {
				Name: "widened_var",
				Type: "set(string)", // widened
			},
			"sensitive_transition": {
				Name:      "sensitive_transition",
				Sensitive: true,
			},
			"deprecated_transition": {
				Name:       "deprecated_transition",
				Deprecated: "use replacement_var instead",
			},
			"new_optional_var": {
				Name:       "new_optional_var",
				Type:       "string",
				HasDefault: true,
				Required:   false,
			},
		},
		Outputs: map[string]ast.OutputDef{
			"output_sensitive_transition": {
				Name:      "output_sensitive_transition",
				Sensitive: true,
			},
			"output_deprecated_transition": {
				Name:       "output_deprecated_transition",
				Deprecated: "will be removed in v3.0",
			},
			"brand_new_output": {
				Name: "brand_new_output",
				Type: "string",
			},
		},
		RequiredCore: []string{">= 1.5"},
		RequiredProviders: map[string]ast.ProviderRequirement{
			"google": {
				Name:               "google",
				VersionConstraints: []string{">= 5.0"}, // updated constraint
			},
		},
	}

	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	assert.True(t, report.CanSafelyUpdate)
	assert.Equal(t, "WARNINGS_ONLY", report.Verdict)
	assert.Equal(t, 0, report.TotalBreaking)
	assert.Equal(t, 2, report.TotalNewFeatures) // 1 var + 1 output
	assert.Equal(t, 8, report.TotalWarnings)
	assert.Contains(t, report.Summary, "UPDATE SAFE WITH WARNINGS")
}

func TestCompare_SafeTransition(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"cluster_name": {Name: "cluster_name", Type: "string"},
		},
		Outputs: map[string]ast.OutputDef{
			"id": {Name: "id", Type: "string"},
		},
	}

	newMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"cluster_name": {Name: "cluster_name", Type: "string"},
			"tags":         {Name: "tags", Type: "map(string)", HasDefault: true},
		},
		Outputs: map[string]ast.OutputDef{
			"id": {Name: "id", Type: "string"},
		},
	}

	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	assert.True(t, report.CanSafelyUpdate)
	assert.Equal(t, "SAFE", report.Verdict)
	assert.Equal(t, 0, report.TotalBreaking)
	assert.Equal(t, 0, report.TotalWarnings)
	assert.Equal(t, 1, report.TotalNewFeatures)
	assert.Contains(t, report.Summary, "SAFE TO UPDATE")
}

func TestCompare_NullabilityAndOutputTypeChanges(t *testing.T) {
	oldNullable := true
	newNullable := false
	oldMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"input": {Name: "input", Type: "string", Nullable: &oldNullable},
			"items": {Name: "items", Type: "list(string)"},
		},
		Outputs: map[string]ast.OutputDef{
			"items": {Name: "items", Type: "list(string)"},
		},
	}
	newMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"input": {Name: "input", Type: "string", Nullable: &newNullable},
			"items": {Name: "items", Type: "set(string)"},
		},
		Outputs: map[string]ast.OutputDef{
			"items": {Name: "items", Type: "any"},
		},
	}

	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	require.Equal(t, 1, report.TotalBreaking)
	assert.Equal(t, "VariableNullability", report.BreakingChanges[0].Category)
	assert.Equal(t, 9, report.BreakingChanges[0].ConfidenceScore)
	assert.Equal(t, 2, report.TotalWarnings)
	assert.Equal(t, "OutputType", report.Warnings[0].Category)
	assert.Equal(t, 8, report.Warnings[0].ConfidenceScore)
	assert.Equal(t, 8, report.Warnings[1].ConfidenceScore)
	assert.Equal(t, "BREAKING_CHANGES", report.Verdict)
	assert.False(t, report.CanSafelyUpdate)
}

func TestCompare_NullabilityWideningWarning(t *testing.T) {
	oldNullable := false
	newNullable := true
	oldMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"input": {Name: "input", Type: "string", Nullable: &oldNullable},
		},
	}
	newMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"input": {Name: "input", Type: "string", Nullable: &newNullable},
		},
	}

	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	assert.Equal(t, 1, report.TotalWarnings)
	assert.Equal(t, "VariableNullability", report.Warnings[0].Category)
	assert.True(t, report.CanSafelyUpdate)
}

func TestCompare_ValidationAndProviderAliasChanges(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"region": {
				Name:            "region",
				Type:            "string",
				ValidationRules: []string{`contains(["us-east-1"], var.region)`},
			},
		},
		RequiredProviders: map[string]ast.ProviderRequirement{
			"aws": {
				Name:                 "aws",
				Source:               "hashicorp/aws",
				ConfigurationAliases: []string{"aws.west"},
			},
		},
	}
	newMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"region": {
				Name:            "region",
				Type:            "string",
				ValidationRules: []string{`contains(["us-east-1", "us-west-2"], var.region)`},
			},
		},
		RequiredProviders: map[string]ast.ProviderRequirement{
			"aws": {
				Name:                 "aws",
				Source:               "hashicorp/aws",
				ConfigurationAliases: []string{"aws.west", "aws.east"},
			},
		},
	}

	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	require.Equal(t, 1, report.TotalBreaking)
	assert.Equal(t, "ProviderConfigurationAlias", report.BreakingChanges[0].Category)
	assert.Equal(t, 9, report.BreakingChanges[0].ConfidenceScore)
	require.Equal(t, 1, report.TotalWarnings)
	assert.Equal(t, "VariableValidation", report.Warnings[0].Category)
	assert.Equal(t, 6, report.Warnings[0].ConfidenceScore)
}
