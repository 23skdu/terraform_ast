package diff

import (
	"context"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/semverutil"
)

// DiffStreamEventKind identifies the kind of payload carried by a DiffStreamEvent.
type DiffStreamEventKind string

const (
	// DiffStreamBreaking marks a DifferenceItem from BreakingChanges.
	DiffStreamBreaking DiffStreamEventKind = "breaking"
	// DiffStreamWarning marks a DifferenceItem from Warnings.
	DiffStreamWarning DiffStreamEventKind = "warning"
	// DiffStreamFeature marks a DifferenceItem from NewFeatures.
	DiffStreamFeature DiffStreamEventKind = "feature"
	// DiffStreamDone marks the terminal event carrying the completed report.
	DiffStreamDone DiffStreamEventKind = "done"
)

// DiffStreamEvent is one result emitted by CompareStream. Consumers receive each difference
// item as soon as its comparison phase finishes, followed by a terminal Done event carrying a
// summary report with totals, verdict, safe pin, and address shifts. The summary deliberately
// has empty item slices: the items have already been streamed, so retaining them would defeat
// bounded-memory consumption.
type DiffStreamEvent struct {
	Kind   DiffStreamEventKind
	Item   DifferenceItem
	Report *DiffReport
}

// CompareStream compares two modules and streams its individual difference items on the
// returned channel. The channel is buffered and closed after the terminal Done event, so
// callers can range over it without blocking the producer.
//
// Each finding is emitted as soon as it is produced; no item slices are accumulated at all.
// Cancellation is supported via CompareStreamContext. Callers that need the complete report with
// all item slices should use Compare instead. The terminal report contains totals, verdict, safe
// pin, and address shifts only.
func CompareStream(oldMod, newMod *ast.ModuleInfo, oldVer, newVer string, hazardLookups ...ast.HazardLookup) <-chan DiffStreamEvent {
	return CompareStreamContext(context.Background(), oldMod, newMod, oldVer, newVer, hazardLookups...)
}

// CompareStreamContext is CompareStream with cancellation. When ctx is cancelled the producer
// stops at the next finding, closes the channel, and never emits a Done event, so consumers that
// stop early do not leak a blocked goroutine.
func CompareStreamContext(ctx context.Context, oldMod, newMod *ast.ModuleInfo, oldVer, newVer string, hazardLookups ...ast.HazardLookup) <-chan DiffStreamEvent {
	ch := make(chan DiffStreamEvent, 64)
	go func() {
		defer close(ch)
		var hazardLookup ast.HazardLookup
		if len(hazardLookups) > 0 {
			hazardLookup = hazardLookups[0]
		}
		oldMod, newMod = normalizeModules(oldMod, newMod)
		bump := semverutil.DetermineBumpType(oldVer, newVer)
		summary := &DiffReport{
			ModuleName:  moduleDisplayName(newMod, oldMod),
			OldVersion:  oldVer,
			NewVersion:  newVer,
			BumpType:    bump,
			IsDowngrade: bump == semverutil.BumpDowngrade,
		}

		// The sink forwards every finding directly to the consumer. Only a bounded summary is
		// retained, so memory use does not grow with the number of differences in the module.
		sink := &diffSink{
			report: summary,
			emit: func(kind DiffStreamEventKind, item DifferenceItem) bool {
				if !sendDiffEvent(ctx, ch, DiffStreamEvent{Kind: kind, Item: item}) {
					return false
				}
				switch kind {
				case DiffStreamBreaking:
					summary.TotalBreaking++
				case DiffStreamWarning:
					summary.TotalWarnings++
				case DiffStreamFeature:
					summary.TotalNewFeatures++
				}
				return true
			},
		}
		phases := []struct {
			run func(*ast.ModuleInfo, *ast.ModuleInfo, *diffSink)
		}{
			{compareVariables},
			{compareOutputs},
			{compareRequirements},
			{compareModernBlocks},
			{compareResources},
			{compareModuleCalls},
			{func(old, new *ast.ModuleInfo, s *diffSink) {
				compareDestructiveToggles(old, new, s, hazardLookup)
			}},
		}
		for _, phase := range phases {
			if ctx.Err() != nil {
				return
			}
			phase.run(oldMod, newMod, sink)
		}

		computeVerdict(summary)
		recommendSafePin(summary)
		sendDiffEvent(ctx, ch, DiffStreamEvent{Kind: DiffStreamDone, Report: summary})
	}()
	return ch
}

// sendDiffEvent returns false when the consumer is no longer listening.
func sendDiffEvent(ctx context.Context, ch chan<- DiffStreamEvent, event DiffStreamEvent) bool {
	// Check eagerly: with a cancelled context and buffer space both cases of the
	// select below would be ready, and Go would pick one at random.
	if ctx.Err() != nil {
		return false
	}
	select {
	case ch <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

// DiffIterator walks a completed DiffReport's difference items in a stable order:
// breaking changes, then warnings, then new features.
type DiffIterator struct {
	report *DiffReport
	group  int
	index  int
}

// NewDiffIterator returns an iterator over report. A nil report yields no items.
func NewDiffIterator(report *DiffReport) *DiffIterator {
	return &DiffIterator{report: report}
}

// Next returns the next difference item and true, or the zero value and false
// when iteration is complete.
func (it *DiffIterator) Next() (DifferenceItem, bool) {
	if it == nil || it.report == nil {
		return DifferenceItem{}, false
	}
	groups := [][]DifferenceItem{it.report.BreakingChanges, it.report.Warnings, it.report.NewFeatures}
	for it.group < len(groups) {
		if it.index < len(groups[it.group]) {
			item := groups[it.group][it.index]
			it.index++
			return item, true
		}
		it.group++
		it.index = 0
	}
	return DifferenceItem{}, false
}

// CollectDiff drains a CompareStream channel into a slice of events and returns
// the terminal report (if any). It is a convenience for callers that want the
// streamed items plus the final aggregate.
func CollectDiff(ch <-chan DiffStreamEvent) ([]DiffStreamEvent, *DiffReport) {
	var events []DiffStreamEvent
	var report *DiffReport
	for event := range ch {
		events = append(events, event)
		if event.Kind == DiffStreamDone {
			report = event.Report
		}
	}
	return events, report
}
