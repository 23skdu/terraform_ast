package diff

import (
	"strings"
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
	assert.Equal(t, "2.0.0", report.SafePinVersion)
	assert.Contains(t, report.SafePinRationale, "downgrade")
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
	assert.Equal(t, "1.0.0", report.SafePinVersion)
	assert.Contains(t, report.SafePinRationale, "currently analyzed version")
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
				VersionConstraints: []string{">= 4.1"}, // updated constraint (non-major)
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
	assert.Equal(t, "1.1.0", report.SafePinVersion)
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

func TestCompare_PopulatesModuleName(t *testing.T) {
	// The compared module is identified from the inspected module paths so reports and SARIF
	// artifact locations are never blank.
	report := Compare(
		&ast.ModuleInfo{Path: "/tmp/checkouts/gke-v29"},
		&ast.ModuleInfo{Path: "/tmp/checkouts/gke-v30"},
		"v29.0.0", "v30.0.0")
	assert.Equal(t, "gke-v30", report.ModuleName)

	// Falls back to the old module when the new one has no path.
	report = Compare(
		&ast.ModuleInfo{Path: "/tmp/checkouts/gke-v29"},
		&ast.ModuleInfo{},
		"v29.0.0", "v30.0.0")
	assert.Equal(t, "gke-v29", report.ModuleName)

	// Remains empty when nothing identifies the module.
	report = Compare(&ast.ModuleInfo{}, &ast.ModuleInfo{}, "v29.0.0", "v30.0.0")
	assert.Equal(t, "", report.ModuleName)
	report = Compare(nil, nil, "v29.0.0", "v30.0.0")
	assert.Equal(t, "", report.ModuleName)

	// Paths that normalise to the repository root stay meaningful.
	report = Compare(&ast.ModuleInfo{}, &ast.ModuleInfo{Path: "/"}, "v1.0.0", "v2.0.0")
	assert.Equal(t, "/", report.ModuleName)
}

func TestCompare_ProviderAliasRemovedAndSourceChanged(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		RequiredProviders: map[string]ast.ProviderRequirement{
			"aws": {
				Name:                 "aws",
				Source:               "hashicorp/aws",
				VersionConstraints:   []string{">= 5.0"},
				ConfigurationAliases: []string{"aws.west", "aws.east"},
			},
		},
	}
	newMod := &ast.ModuleInfo{
		RequiredProviders: map[string]ast.ProviderRequirement{
			"aws": {
				Name:                 "aws",
				Source:               "hashicorp/aws-fork",
				VersionConstraints:   []string{">= 5.1"},
				ConfigurationAliases: []string{"aws.west"},
			},
		},
	}

	report := Compare(oldMod, newMod, "1.0.0", "2.0.0")
	require.Equal(t, 1, report.TotalBreaking, "only the provider source change is breaking")
	require.Equal(t, 2, report.TotalWarnings, "the dropped alias and the bumped constraint both warn")

	var sourceChange *DifferenceItem
	for i := range report.BreakingChanges {
		if report.BreakingChanges[i].Category == "ProviderSource" {
			sourceChange = &report.BreakingChanges[i]
		}
	}
	require.NotNil(t, sourceChange)
	breaking := *sourceChange
	assert.Equal(t, "ProviderSource", breaking.Category)
	assert.Equal(t, "aws", breaking.Name)
	assert.Equal(t, "hashicorp/aws", breaking.OldValue)
	assert.Equal(t, "hashicorp/aws-fork", breaking.NewValue)
	assert.Equal(t, 9, breaking.ConfidenceScore)

	var aliasWarning *DifferenceItem
	for i := range report.Warnings {
		if report.Warnings[i].Category == "ProviderConfigurationAlias" {
			aliasWarning = &report.Warnings[i]
		}
	}
	require.NotNil(t, aliasWarning)
	assert.Equal(t, "aws.east", aliasWarning.Name)
	assert.Equal(t, SeverityWarning, aliasWarning.Severity)
}

func TestCompare_SafePinRationales(t *testing.T) {
	t.Run("downgrade keeps the analyzed version pinned", func(t *testing.T) {
		report := Compare(&ast.ModuleInfo{}, &ast.ModuleInfo{}, "v2.0.0", "v1.0.0")
		assert.True(t, report.IsDowngrade)
		assert.Equal(t, "v2.0.0", report.SafePinVersion)
		assert.Contains(t, report.SafePinRationale, "downgrade")
	})

	t.Run("safe transition pins the target", func(t *testing.T) {
		report := Compare(&ast.ModuleInfo{}, &ast.ModuleInfo{}, "v1.0.0", "v1.1.0")
		assert.Equal(t, "v1.1.0", report.SafePinVersion)
		assert.Contains(t, report.SafePinRationale, "no detected breaking changes")
	})

	t.Run("breaking transition keeps the analyzed version pinned", func(t *testing.T) {
		oldMod := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{
			"gone": {Name: "gone", Type: "string"},
		}}
		report := Compare(oldMod, &ast.ModuleInfo{Variables: map[string]ast.VariableDef{}}, "v1.0.0", "v2.0.0")
		assert.Equal(t, "v1.0.0", report.SafePinVersion)
		assert.Contains(t, report.SafePinRationale, "until the breaking changes are resolved")
	})

	t.Run("breaking changes without any version information", func(t *testing.T) {
		oldMod := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{
			"gone": {Name: "gone", Type: "string"},
		}}
		report := Compare(oldMod, &ast.ModuleInfo{Variables: map[string]ast.VariableDef{}}, "", "")
		assert.Equal(t, 1, report.TotalBreaking)
		assert.Empty(t, report.SafePinVersion)
		assert.Contains(t, report.SafePinRationale, "No safe pin can be recommended")
	})
}

func TestSameStrings(t *testing.T) {
	assert.True(t, sameStrings(nil, nil))
	assert.True(t, sameStrings([]string{"a"}, []string{"a"}))
	assert.True(t, sameStrings([]string{"a", "b"}, []string{"b", "a"}), "ordering is not significant")

	assert.False(t, sameStrings([]string{"a"}, []string{"a", "b"}), "different lengths")
	assert.False(t, sameStrings([]string{"a", "a"}, []string{"a", "b"}), "duplicates collapse to a smaller set")
	assert.False(t, sameStrings([]string{"a", "b"}, []string{"a", "c"}), "different members")
}

func TestCompare_ModernBlocksAndSoftRemovals(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"legacy_var": {Name: "legacy_var", Type: "string"},
			"dead_var":   {Name: "dead_var", Type: "string"},
		},
		CheckBlocks: map[string]ast.CheckBlockDef{
			"existing_check": {Name: "existing_check"},
		},
		Functions: map[string]ast.FunctionDef{
			"existing_fn": {Name: "existing_fn"},
		},
	}

	newMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{},
		RemovedBlocks: []ast.RemovedBlockDef{
			{From: "var.legacy_var", Destroy: false},
			{From: "google_compute_instance.vm", Destroy: true},
		},
		CheckBlocks: map[string]ast.CheckBlockDef{
			"existing_check": {Name: "existing_check"},
			"new_check":      {Name: "new_check", AssertRules: []string{"true"}},
		},
		ImportBlocks: []ast.ImportBlockDef{
			{To: "aws_s3_bucket.b", ID: "bucket-1"},
		},
		Functions: map[string]ast.FunctionDef{
			"existing_fn": {Name: "existing_fn"},
			"new_fn":      {Name: "new_fn", ReturnType: "string"},
		},
		EncryptionBlocks: []ast.EncryptionBlockDef{
			{KeyProvider: "pbkdf2"},
		},
	}

	report := Compare(oldMod, newMod, "v1.0.0", "v1.1.0")

	// legacy_var was soft-removed with destroy=false, so it's a WARNING rather than BREAKING
	// dead_var was hard-removed without removed block, so it's BREAKING
	// google_compute_instance.vm was removed with destroy=true, so it's BREAKING (RemovedDestroy)
	assert.Equal(t, 2, report.TotalBreaking)
	var hasDeadVar, hasRemovedDestroy bool
	for _, bc := range report.BreakingChanges {
		if bc.Name == "dead_var" {
			hasDeadVar = true
		}
		if bc.Name == "google_compute_instance.vm" && bc.Category == "RemovedDestroy" {
			hasRemovedDestroy = true
		}
	}
	assert.True(t, hasDeadVar)
	assert.True(t, hasRemovedDestroy)

	var hasSoftRemovedWarning bool
	for _, w := range report.Warnings {
		if w.Name == "legacy_var" && strings.Contains(w.Description, "soft-removed") {
			hasSoftRemovedWarning = true
			break
		}
	}
	assert.True(t, hasSoftRemovedWarning)

	// Verify NewFeatures from modern blocks
	categories := make(map[string]bool)
	for _, nf := range report.NewFeatures {
		categories[nf.Category] = true
	}
	assert.True(t, categories["SoftRemovedBlock"])
	assert.True(t, categories["CheckBlockAdded"])
	assert.True(t, categories["ImportBlockAdded"])
	assert.True(t, categories["FunctionAdded"])
	assert.True(t, categories["EncryptionConfigured"])
}

// TestCompareModernBlocks_UnchangedBlocksAreNotNewFeatures is the regression test for the
// false positive where every import {} and encryption {} block in the new module was reported
// as a new feature without ever being compared against the old module.
func TestCompareModernBlocks_UnchangedBlocksAreNotNewFeatures(t *testing.T) {
	build := func() *ast.ModuleInfo {
		return &ast.ModuleInfo{
			Path:             "module",
			ImportBlocks:     []ast.ImportBlockDef{{To: "google_compute_network.vpc", ID: "net-1"}},
			EncryptionBlocks: []ast.EncryptionBlockDef{{KeyProvider: "pbkdf2"}},
			CheckBlocks:      map[string]ast.CheckBlockDef{"health": {Name: "health"}},
			Functions:        map[string]ast.FunctionDef{"slug": {Name: "slug"}},
			Variables:        map[string]ast.VariableDef{},
			Outputs:          map[string]ast.OutputDef{},
		}
	}

	report := Compare(build(), build(), "v1.0.0", "v1.1.0")
	assert.Empty(t, report.NewFeatures,
		"identical import/encryption/check/function blocks must not be reported as new features")

	// A changed import id on an already-imported address is still not a new capability.
	oldMod := build()
	newMod := build()
	newMod.ImportBlocks[0].ID = "net-2"
	assert.Empty(t, Compare(oldMod, newMod, "v1.0.0", "v1.1.0").NewFeatures,
		"re-identifying an already-imported address must not be reported as a new feature")
}

// TestCompareModernBlocks_NewBlocksAreDetected confirms the fix above did not disable detection.
func TestCompareModernBlocks_NewBlocksAreDetected(t *testing.T) {
	empty := &ast.ModuleInfo{
		Path:        "module",
		Variables:   map[string]ast.VariableDef{},
		Outputs:     map[string]ast.OutputDef{},
		CheckBlocks: map[string]ast.CheckBlockDef{},
		Functions:   map[string]ast.FunctionDef{},
	}
	newMod := &ast.ModuleInfo{
		Path:             "module",
		Variables:        map[string]ast.VariableDef{},
		Outputs:          map[string]ast.OutputDef{},
		ImportBlocks:     []ast.ImportBlockDef{{To: "aws_instance.web", ID: "i-1"}},
		EncryptionBlocks: []ast.EncryptionBlockDef{{KeyProvider: "aes_gcm"}},
		CheckBlocks:      map[string]ast.CheckBlockDef{"healthy": {Name: "healthy"}},
		Functions:        map[string]ast.FunctionDef{"slugify": {Name: "slugify"}},
	}

	categories := map[string]bool{}
	for _, nf := range Compare(empty, newMod, "v1.0.0", "v1.1.0").NewFeatures {
		categories[nf.Category] = true
	}
	for _, want := range []string{"ImportBlockAdded", "EncryptionConfigured", "CheckBlockAdded", "FunctionAdded"} {
		assert.True(t, categories[want], "expected a newly declared %s to be reported", want)
	}
}

// TestCompareModernBlocks_EncryptionBackendSwap covers swapping the encryption backend, which
// is an operator-visible configuration change even though a block was already present.
func TestCompareModernBlocks_EncryptionBackendSwap(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		Path: "module", Variables: map[string]ast.VariableDef{}, Outputs: map[string]ast.OutputDef{},
		EncryptionBlocks: []ast.EncryptionBlockDef{{KeyProvider: "pbkdf2"}},
	}
	newMod := &ast.ModuleInfo{
		Path: "module", Variables: map[string]ast.VariableDef{}, Outputs: map[string]ast.OutputDef{},
		EncryptionBlocks: []ast.EncryptionBlockDef{{KeyProvider: "aes_gcm"}},
	}
	report := Compare(oldMod, newMod, "v1.0.0", "v1.1.0")
	require.Len(t, report.NewFeatures, 1)
	assert.Equal(t, "EncryptionConfigured", report.NewFeatures[0].Category)
	assert.Equal(t, "aes_gcm", report.NewFeatures[0].Name)
}

func TestCompare_NewlyAddedRequiredProvider(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		RequiredProviders: map[string]ast.ProviderRequirement{},
	}
	newMod := &ast.ModuleInfo{
		RequiredProviders: map[string]ast.ProviderRequirement{
			"helm": {
				Source:             "hashicorp/helm",
				VersionConstraints: []string{">= 2.0.0"},
			},
			"aws": {
				Source:               "hashicorp/aws",
				VersionConstraints:   []string{">= 5.0.0"},
				ConfigurationAliases: []string{"aws.west"},
			},
		},
	}
	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	require.Len(t, report.Warnings, 2)
	require.Len(t, report.BreakingChanges, 1)
	assert.Equal(t, "ProviderConfigurationAlias", report.BreakingChanges[0].Category)
	assert.Equal(t, "aws.west", report.BreakingChanges[0].Name)
}
