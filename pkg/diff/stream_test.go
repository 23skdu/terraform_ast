package diff

import (
	"context"
	"fmt"
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
	direct := Compare(oldMod, newMod, "1.0", "2.0")

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
	// The terminal report is a summary: items were already streamed, so only the
	// counters are retained. They must match a full buffered Compare.
	assert.Equal(t, direct.TotalBreaking, breaking)
	assert.Equal(t, direct.TotalWarnings, warnings)
	assert.Equal(t, direct.TotalNewFeatures, features)
	assert.Equal(t, breaking, report.TotalBreaking)
	assert.Equal(t, warnings, report.TotalWarnings)
	assert.Equal(t, features, report.TotalNewFeatures)

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

	// The breaking-change summary must match a direct Compare.
	directBreaking := Compare(breakingModOld, breakingModNew, "1.0", "2.0")
	assert.Equal(t, directBreaking.TotalBreaking, bReport.TotalBreaking)

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

// doneOnlyContext models a context whose Done channel has fired but whose Err
// has not yet been observed, so the select branch of sendDiffEvent is exercised.
type doneOnlyContext struct {
	context.Context
	done chan struct{}
}

func (c doneOnlyContext) Done() <-chan struct{} { return c.done }
func (c doneOnlyContext) Err() error            { return nil }

func TestSendDiffEventStopsOnDoneChannel(t *testing.T) {
	ctx := doneOnlyContext{Context: context.Background(), done: make(chan struct{})}
	close(ctx.done)
	// No receiver on the unbuffered channel: the only ready case is ctx.Done().
	assert.False(t, sendDiffEvent(ctx, make(chan DiffStreamEvent), DiffStreamEvent{}))
}

func TestCompareStreamContextCancellation(t *testing.T) {
	oldMod := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{"x": {Name: "x", Type: "string"}}}
	newMod := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{"x": {Name: "x", Type: "number"}}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events, report := CollectDiff(CompareStreamContext(ctx, oldMod, newMod, "1.0.0", "2.0.0"))
	assert.Empty(t, events)
	assert.Nil(t, report)
}

func TestCompareStreamContextStopsOnMidStreamCancellation(t *testing.T) {
	oldMod := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{}}
	newMod := &ast.ModuleInfo{Variables: make(map[string]ast.VariableDef)}
	// More findings than the channel buffer forces the producer to observe cancellation
	// while the phase is still emitting, not merely at a phase boundary.
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("new_var_%d", i)
		newMod.Variables[name] = ast.VariableDef{Name: name, Type: "string", HasDefault: true}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := CompareStreamContext(ctx, oldMod, newMod, "1.0.0", "2.0.0")
	first, ok := <-ch
	require.True(t, ok)
	assert.Equal(t, DiffStreamFeature, first.Kind)
	cancel()

	var received, done int
	for event := range ch {
		received++
		if event.Kind == DiffStreamDone {
			done++
		}
	}
	assert.Less(t, received, 200)
	assert.Zero(t, done, "a cancelled stream must not emit a terminal report")
}

func TestSendDiffEventStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.False(t, sendDiffEvent(ctx, make(chan DiffStreamEvent, 1), DiffStreamEvent{}))
}

func TestCompareStreamPreservesItemsAndHazardLookup(t *testing.T) {
	resource := func(cidr string) map[string]ast.ResourceDef {
		return map[string]ast.ResourceDef{
			"aws_vpc.main": {Type: "aws_vpc", Name: "main", Attributes: map[string]string{"cidr_block": cidr}},
		}
	}
	oldMod := &ast.ModuleInfo{ManagedResources: resource("10.0.0.0/16")}
	newMod := &ast.ModuleInfo{ManagedResources: resource("10.1.0.0/16")}

	events, streamed := CollectDiff(CompareStream(oldMod, newMod, "1.0.0", "2.0.0", simpleHazardLookup{}))
	require.NotNil(t, streamed)
	direct := Compare(oldMod, newMod, "1.0.0", "2.0.0", simpleHazardLookup{})
	// The streamed terminal event carries only summary state; the items were emitted
	// earlier and are deliberately not retained.
	expectedSummary := *direct
	expectedSummary.BreakingChanges = nil
	expectedSummary.Warnings = nil
	expectedSummary.NewFeatures = nil
	assert.Equal(t, &expectedSummary, streamed)

	var breaking, warnings, features []DifferenceItem
	for _, event := range events {
		switch event.Kind {
		case DiffStreamBreaking:
			breaking = append(breaking, event.Item)
		case DiffStreamWarning:
			warnings = append(warnings, event.Item)
		case DiffStreamFeature:
			features = append(features, event.Item)
		case DiffStreamDone:
			assert.Same(t, streamed, event.Report)
		}
	}
	// Items are emitted per phase, so their order can differ from the globally sorted
	// buffered report; every item must still be present exactly once.
	assert.ElementsMatch(t, direct.BreakingChanges, breaking)
	assert.ElementsMatch(t, direct.Warnings, warnings)
	assert.ElementsMatch(t, direct.NewFeatures, features)
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
