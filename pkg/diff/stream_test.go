package diff

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompareStream(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"widened_var": {Name: "widened_var", Type: "list(string)"},
		},
		Outputs:           make(map[string]ast.OutputDef),
		RequiredProviders: make(map[string]ast.ProviderRequirement),
		ManagedResources:  make(map[string]ast.ResourceDef),
		DataResources:     make(map[string]ast.ResourceDef),
	}
	newMod := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"widened_var": {Name: "widened_var", Type: "set(string)"},
		},
		Outputs:           make(map[string]ast.OutputDef),
		RequiredProviders: make(map[string]ast.ProviderRequirement),
		ManagedResources:  make(map[string]ast.ResourceDef),
		DataResources:     make(map[string]ast.ResourceDef),
	}

	ch := CompareStream(oldMod, newMod, "1.0", "2.0")
	events, report := CollectDiff(ch)
	require.NotNil(t, report)
	assert.Equal(t, "1.0", report.OldVersion)

	breaking, warnings, features := 0, 0, 0
	for _, e := range events {
		switch e.Kind {
		case DiffStreamBreaking:
			breaking++
		case DiffStreamWarning:
			warnings++
		case DiffStreamFeature:
			features++
		case DiffStreamDone:
			assert.Same(t, report, e.Report)
		}
	}
	assert.Equal(t, len(report.BreakingChanges), breaking)
	assert.Equal(t, len(report.Warnings), warnings)
	assert.Equal(t, len(report.NewFeatures), features)

	// Exercise the breaking-change streaming path with a type change.
	breakingModOld := &ast.ModuleInfo{
		Variables:         map[string]ast.VariableDef{"x": {Name: "x", Type: "string"}},
		Outputs:           make(map[string]ast.OutputDef),
		RequiredProviders: make(map[string]ast.ProviderRequirement),
		ManagedResources:  make(map[string]ast.ResourceDef),
		DataResources:     make(map[string]ast.ResourceDef),
	}
	breakingModNew := &ast.ModuleInfo{
		Variables:         map[string]ast.VariableDef{"x": {Name: "x", Type: "number"}},
		Outputs:           make(map[string]ast.OutputDef),
		RequiredProviders: make(map[string]ast.ProviderRequirement),
		ManagedResources:  make(map[string]ast.ResourceDef),
		DataResources:     make(map[string]ast.ResourceDef),
	}
	bEvents, bReport := CollectDiff(CompareStream(breakingModOld, breakingModNew, "1.0", "2.0"))
	assert.NotNil(t, bReport)
	assert.NotEmpty(t, bEvents)

	// The streamed report must match a direct Compare.
	direct := Compare(oldMod, newMod, "1.0", "2.0")
	assert.Equal(t, direct.TotalBreaking, report.TotalBreaking)

	// Exercise the new-feature streaming path.
	featureOld := &ast.ModuleInfo{
		Variables:         make(map[string]ast.VariableDef),
		Outputs:           make(map[string]ast.OutputDef),
		RequiredProviders: make(map[string]ast.ProviderRequirement),
		ManagedResources:  make(map[string]ast.ResourceDef),
		DataResources:     make(map[string]ast.ResourceDef),
	}
	featureNew := &ast.ModuleInfo{
		Variables:         map[string]ast.VariableDef{"new_optional_var": {Name: "new_optional_var", Type: "string", HasDefault: true, Required: false}},
		Outputs:           make(map[string]ast.OutputDef),
		RequiredProviders: make(map[string]ast.ProviderRequirement),
		ManagedResources:  make(map[string]ast.ResourceDef),
		DataResources:     make(map[string]ast.ResourceDef),
	}
	fEvents, fReport := CollectDiff(CompareStream(featureOld, featureNew, "1.0", "2.0"))
	assert.NotNil(t, fReport)
	assert.NotEmpty(t, fEvents)

	// Nil modules are tolerated exactly like Compare.
	nilCh := CompareStream(nil, nil, "", "")
	nilEvents, nilReport := CollectDiff(nilCh)
	assert.NotEmpty(t, nilEvents)
	assert.NotNil(t, nilReport)
}

func TestDiffIterator(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		Variables:         make(map[string]ast.VariableDef),
		Outputs:           make(map[string]ast.OutputDef),
		RequiredProviders: make(map[string]ast.ProviderRequirement),
		ManagedResources:  make(map[string]ast.ResourceDef),
		DataResources:     make(map[string]ast.ResourceDef),
	}
	newMod := &ast.ModuleInfo{
		Variables:         map[string]ast.VariableDef{"new_optional_var": {Name: "new_optional_var", Type: "string", HasDefault: true, Required: false}},
		Outputs:           make(map[string]ast.OutputDef),
		RequiredProviders: make(map[string]ast.ProviderRequirement),
		ManagedResources:  make(map[string]ast.ResourceDef),
		DataResources:     make(map[string]ast.ResourceDef),
	}
	report := Compare(oldMod, newMod, "1.0", "2.0")
	it := NewDiffIterator(report)
	var count int
	for {
		_, ok := it.Next()
		if !ok {
			break
		}
		count++
	}
	assert.Equal(t, len(report.BreakingChanges)+len(report.Warnings)+len(report.NewFeatures), count)
	assert.Greater(t, count, 0)

	// Exhausted iterator returns false.
	_, ok := it.Next()
	assert.False(t, ok)

	// Nil safety.
	nilIt := NewDiffIterator(nil)
	_, ok = nilIt.Next()
	assert.False(t, ok)
	_, ok = (*DiffIterator)(nil).Next()
	assert.False(t, ok)
}

func TestCollectDiffEmptyChannel(t *testing.T) {
	ch := make(chan DiffStreamEvent)
	close(ch)
	events, report := CollectDiff(ch)
	assert.Empty(t, events)
	assert.Nil(t, report)
}
