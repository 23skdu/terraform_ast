package diff

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPass5_CompareResourcesRemovedAndAdded(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.legacy": {Mode: "managed", Type: "aws_s3_bucket", Name: "legacy", Provider: "aws"},
		},
		DataResources: map[string]ast.ResourceDef{
			"data.aws_ami.old": {Mode: "data", Type: "aws_ami", Name: "old", Provider: "aws"},
		},
	}
	newMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.legacy": {Mode: "managed", Type: "aws_s3_bucket", Name: "legacy", Provider: "aws"},
			"aws_s3_bucket.new":    {Mode: "managed", Type: "aws_s3_bucket", Name: "new", Provider: "aws"},
		},
		DataResources: map[string]ast.ResourceDef{},
	}
	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	var hasRemovedData, hasAddedManaged bool
	for _, bc := range report.BreakingChanges {
		if bc.Category == "Resource" && bc.Name == "data.aws_ami.old" {
			hasRemovedData = true
		}
	}
	for _, nf := range report.NewFeatures {
		if nf.Category == "Resource" && nf.Name == "aws_s3_bucket.new" {
			hasAddedManaged = true
		}
	}
	assert.True(t, hasRemovedData)
	assert.True(t, hasAddedManaged)
}

func TestPass5_CompareResourcesProviderAndTypeChange(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.x": {Mode: "managed", Type: "aws_s3_bucket", Name: "x", Provider: "aws"},
		},
	}
	newMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.x": {Mode: "managed", Type: "aws_s3_bucket", Name: "x", Provider: "google"},
		},
	}
	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	var hasProvider bool
	for _, bc := range report.BreakingChanges {
		if bc.Category == "ResourceProvider" {
			hasProvider = true
		}
	}
	assert.True(t, hasProvider)

	// type reassignment (same label, different type)
	oldMod2 := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.x": {Mode: "managed", Type: "aws_s3_bucket", Name: "x", Provider: "aws"},
		},
	}
	newMod2 := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"google_storage_bucket.x": {Mode: "managed", Type: "google_storage_bucket", Name: "x", Provider: "google"},
		},
	}
	report2 := Compare(oldMod2, newMod2, "1.0.0", "1.1.0")
	var hasType bool
	for _, bc := range report2.BreakingChanges {
		if bc.Category == "ResourceProvider" {
			hasType = true
		}
	}
	assert.True(t, hasType)
}

func TestPass5_CompareResourcesIdentity(t *testing.T) {
	m := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.x": {Mode: "managed", Type: "aws_s3_bucket", Name: "x", Provider: "aws"},
		},
	}
	report := Compare(m, m, "1.0.0", "1.0.0")
	assert.Equal(t, 0, report.TotalBreaking)
	assert.Equal(t, 0, report.TotalNewFeatures)
	assert.Equal(t, 0, report.TotalWarnings)
}

func TestPass5_CompareResourcesNil(t *testing.T) {
	report := Compare(nil, nil, "1.0.0", "1.0.0")
	require.NotNil(t, report)
	assert.Equal(t, 0, report.TotalBreaking)
}

func TestPass5_ModernBlocksRemovedDestroy(t *testing.T) {
	oldMod := &ast.ModuleInfo{}
	newMod := &ast.ModuleInfo{
		RemovedBlocks: []ast.RemovedBlockDef{{From: "aws_s3_bucket.x", Destroy: true}},
	}
	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	var has bool
	for _, bc := range report.BreakingChanges {
		if bc.Category == "RemovedDestroy" {
			has = true
		}
	}
	assert.True(t, has)
}

func TestPass5_ModernBlocksRemovals(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		CheckBlocks:      map[string]ast.CheckBlockDef{"c": {Name: "c"}},
		Functions:        map[string]ast.FunctionDef{"f": {Name: "f"}},
		ImportBlocks:     []ast.ImportBlockDef{{To: "aws_s3_bucket.x", ID: "1"}},
		EncryptionBlocks: []ast.EncryptionBlockDef{{KeyProvider: "pbkdf2"}},
		RemovedBlocks:    []ast.RemovedBlockDef{{From: "r", Destroy: false}},
	}
	newMod := &ast.ModuleInfo{}
	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	cats := map[string]bool{}
	for _, w := range report.Warnings {
		cats[w.Category] = true
	}
	for _, bc := range report.BreakingChanges {
		cats[bc.Category] = true
	}
	assert.True(t, cats["CheckBlockRemoved"])
	assert.True(t, cats["ImportBlockRemoved"])
	assert.True(t, cats["FunctionRemoved"])
	assert.True(t, cats["EncryptionRemoved"])
	assert.True(t, cats["RemovedBlockRemoved"])
}

func TestPass5_ModernBlocksIdentity(t *testing.T) {
	m := &ast.ModuleInfo{
		CheckBlocks: map[string]ast.CheckBlockDef{"c": {Name: "c"}},
	}
	report := Compare(m, m, "1.0.0", "1.0.0")
	assert.Equal(t, 0, report.TotalNewFeatures)
}

func TestPass5_DefaultChange(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"region": {Name: "region", Type: "string", Default: "us-east-1", HasDefault: true},
		},
	}
	newMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"region": {Name: "region", Type: "string", Default: "eu-west-1", HasDefault: true},
		},
	}
	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	var has bool
	for _, bc := range report.BreakingChanges {
		if bc.Category == "VariableDefault" {
			has = true
		}
	}
	assert.True(t, has)
}

func TestPass5_OutputValueChange(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		Outputs: map[string]ast.OutputDef{
			"out": {Name: "out", Type: "string", Value: "old_expr", Sensitive: false},
		},
	}
	newMod := &ast.ModuleInfo{
		Outputs: map[string]ast.OutputDef{
			"out": {Name: "out", Type: "string", Value: "new_expr", Sensitive: false},
		},
	}
	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	var hasWarn bool
	for _, w := range report.Warnings {
		if w.Category == "OutputValue" {
			hasWarn = true
		}
	}
	assert.True(t, hasWarn)

	// promoted to breaking when sensitive changes
	oldMod2 := &ast.ModuleInfo{
		Outputs: map[string]ast.OutputDef{
			"out": {Name: "out", Type: "string", Value: "old_expr", Sensitive: false},
		},
	}
	newMod2 := &ast.ModuleInfo{
		Outputs: map[string]ast.OutputDef{
			"out": {Name: "out", Type: "string", Value: "new_expr", Sensitive: true},
		},
	}
	report2 := Compare(oldMod2, newMod2, "1.0.0", "1.1.0")
	var hasBreak bool
	for _, bc := range report2.BreakingChanges {
		if bc.Category == "OutputValue" {
			hasBreak = true
		}
	}
	assert.True(t, hasBreak)
}

func TestPass5_ProviderMajorBump(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		RequiredProviders: map[string]ast.ProviderRequirement{
			"aws": {Name: "aws", Source: "hashicorp/aws", VersionConstraints: []string{"~> 4.0"}},
		},
	}
	newMod := &ast.ModuleInfo{
		RequiredProviders: map[string]ast.ProviderRequirement{
			"aws": {Name: "aws", Source: "hashicorp/aws", VersionConstraints: []string{"~> 6.0"}},
		},
	}
	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	var has bool
	for _, bc := range report.BreakingChanges {
		if bc.Category == "ProviderMajorUpgrade" {
			has = true
		}
	}
	assert.True(t, has)
}

func TestPass5_ProviderConstraintNoise(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		RequiredProviders: map[string]ast.ProviderRequirement{
			"aws": {Name: "aws", Source: "hashicorp/aws", VersionConstraints: []string{">= 4.0", ">= 3.0"}},
		},
	}
	newMod := &ast.ModuleInfo{
		RequiredProviders: map[string]ast.ProviderRequirement{
			"aws": {Name: "aws", Source: "hashicorp/aws", VersionConstraints: []string{">= 3.0", ">= 4.0"}},
		},
	}
	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	// no provider requirement warning
	for _, w := range report.Warnings {
		assert.NotEqual(t, "ProviderRequirement", w.Category)
	}
}

func TestPass5_RequiredVersionRemoved(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		RequiredCore: []string{">= 1.5.0"},
	}
	newMod := &ast.ModuleInfo{}
	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	var has bool
	for _, w := range report.Warnings {
		if w.Category == "TerraformCore" {
			has = true
		}
	}
	assert.True(t, has)
}

func TestPass5_MajorFromConstraint(t *testing.T) {
	n, ok := majorFromConstraint("~> 4.0")
	assert.True(t, ok)
	assert.Equal(t, 4, n)
	_, ok2 := majorFromConstraint("no-version")
	assert.False(t, ok2)
}

func TestPass5_IsMajorProviderBumpFalse(t *testing.T) {
	// minor bump only
	old := []string{">= 4.0"}
	new := []string{">= 4.1"}
	assert.False(t, isMajorProviderBump(old, new))
	// empty
	assert.False(t, isMajorProviderBump(nil, new))
	assert.False(t, isMajorProviderBump(old, nil))
}

func TestPass5_CompareResourcesRenamedShift(t *testing.T) {
	// renamed resource (same type, different name) -> suppressed (reported as address shift)
	oldMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.old": {Mode: "managed", Type: "aws_s3_bucket", Name: "old", Provider: "aws"},
		},
	}
	newMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.new": {Mode: "managed", Type: "aws_s3_bucket", Name: "new", Provider: "aws"},
		},
	}
	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	// should not report removed/added (shift covers it)
	for _, bc := range report.BreakingChanges {
		assert.NotEqual(t, "Resource", bc.Category)
	}
	for _, nf := range report.NewFeatures {
		assert.NotEqual(t, "Resource", nf.Category)
	}
}

func TestPass5_CompareResourcesConsumedNewInTypeReassign(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.x":         {Mode: "managed", Type: "aws_s3_bucket", Name: "x", Provider: "aws"},
			"google_storage_bucket.x": {Mode: "managed", Type: "google_storage_bucket", Name: "x", Provider: "google"},
		},
	}
	newMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.x": {Mode: "managed", Type: "aws_s3_bucket", Name: "x", Provider: "aws"}, // exact match for first
			"azure_blob.x":    {Mode: "managed", Type: "azure_blob", Name: "x", Provider: "azure"},
		},
	}
	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	require.NotNil(t, report)
}

func TestPass5_CompareModernBlocksNil(t *testing.T) {
	report := &DiffReport{}
	compareModernBlocks(nil, nil, report)
	require.NotNil(t, report)
}

func TestPass5_CompareResourcesNilDirect(t *testing.T) {
	report := &DiffReport{}
	compareResources(nil, nil, report)
	require.NotNil(t, report)
}

func TestPass5_IsMajorProviderBumpMixedDigits(t *testing.T) {
	// outer continue (old has a no-digit entry later)
	assert.False(t, isMajorProviderBump([]string{">= 4.0", "latest"}, []string{">= 4.0"}))
	// inner continue (new has a no-digit entry later)
	assert.False(t, isMajorProviderBump([]string{">= 4.0"}, []string{">= 4.0", "latest"}))
}

func TestPass5_ModernBlocksExistedSkip(t *testing.T) {
	m := &ast.ModuleInfo{
		RemovedBlocks: []ast.RemovedBlockDef{
			{From: "r", Destroy: false},
			{From: "r2", Destroy: true},
		},
	}
	report := Compare(m, m, "1.0.0", "1.0.0")
	// soft removed block existed in old -> not re-reported
	for _, nf := range report.NewFeatures {
		assert.NotEqual(t, "SoftRemovedBlock", nf.Category)
	}
	for _, bc := range report.BreakingChanges {
		assert.NotEqual(t, "RemovedDestroy", bc.Category)
	}
}

func TestPass5_IsMajorProviderBumpDowngradeAndNoise(t *testing.T) {
	// downgrade: new major lower
	assert.False(t, isMajorProviderBump([]string{">= 5.0"}, []string{">= 4.0"}))
	// new constraint with no digit
	assert.False(t, isMajorProviderBump([]string{">= 4.0"}, []string{"latest"}))
	// fallback false already covered; also cover om != 0 false path via same major
	assert.False(t, isMajorProviderBump([]string{">= 4.0"}, []string{">= 4.0"}))
}

func TestPass5_MovedBlocksIdentity(t *testing.T) {
	m := &ast.ModuleInfo{
		MovedBlocks: []ast.MovedBlockDef{{From: "a", To: "b"}},
	}
	report := Compare(m, m, "1.0.0", "1.0.0")
	for _, nf := range report.NewFeatures {
		assert.NotEqual(t, "MovedBlockAdded", nf.Category)
	}
	for _, bc := range report.BreakingChanges {
		assert.NotEqual(t, "StateMigrationRemoved", bc.Category)
	}
}

func TestPass5_MovedBlocks(t *testing.T) {
	oldMod := &ast.ModuleInfo{}
	newMod := &ast.ModuleInfo{
		MovedBlocks: []ast.MovedBlockDef{{From: "aws_s3_bucket.old", To: "aws_s3_bucket.new"}},
	}
	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	var has bool
	for _, nf := range report.NewFeatures {
		if nf.Category == "MovedBlockAdded" {
			has = true
		}
	}
	assert.True(t, has)

	// removed moved block -> breaking
	oldMod2 := &ast.ModuleInfo{
		MovedBlocks: []ast.MovedBlockDef{{From: "aws_s3_bucket.old", To: "aws_s3_bucket.new"}},
	}
	newMod2 := &ast.ModuleInfo{}
	report2 := Compare(oldMod2, newMod2, "1.0.0", "1.1.0")
	var hasBreak bool
	for _, bc := range report2.BreakingChanges {
		if bc.Category == "StateMigrationRemoved" {
			hasBreak = true
		}
	}
	assert.True(t, hasBreak)
}

func TestPass5_DetectAddressShiftsMovedSuppresses(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.old": {Mode: "managed", Type: "aws_s3_bucket", Name: "old"},
		},
	}
	newMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.new": {Mode: "managed", Type: "aws_s3_bucket", Name: "new"},
		},
		MovedBlocks: []ast.MovedBlockDef{{From: "aws_s3_bucket.old", To: "aws_s3_bucket.new"}},
	}
	shifts := ast.DetectAddressShifts(oldMod, newMod)
	require.Len(t, shifts, 1)
	assert.False(t, shifts[0].IsRecreation)
}

func TestPass5_ResourceFullAddressAndModeLabel(t *testing.T) {
	r := ast.ResourceDef{Mode: "data", Type: "aws_ami", Name: "x"}
	assert.Equal(t, "data.aws_ami.x", resourceFullAddress("data", r))
	assert.Equal(t, "managed", modeLabel("managed"))
	assert.Equal(t, "data", modeLabel("data"))
	mr := ast.ResourceDef{Type: "aws_s3_bucket", Name: "y"}
	assert.Equal(t, "aws_s3_bucket.y", resourceFullAddress("managed", mr))
}

func TestPass5_CompareResourcesTypeReassignmentNoDuplicate(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.x": {Mode: "managed", Type: "aws_s3_bucket", Name: "x", Provider: "aws"},
		},
	}
	newMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"google_storage_bucket.x": {Mode: "managed", Type: "google_storage_bucket", Name: "x", Provider: "google"},
		},
	}
	report := Compare(oldMod, newMod, "1.0.0", "1.1.0")
	// should not also add as new feature
	for _, nf := range report.NewFeatures {
		assert.NotEqual(t, "google_storage_bucket.x", nf.Name)
	}
}
