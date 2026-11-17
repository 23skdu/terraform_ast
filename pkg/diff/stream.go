package diff

import (
	"github.com/23skdu/terraform_ast/pkg/ast"
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

// DiffStreamEvent is one incremental result emitted by CompareStream. Consumers
// receive every difference item followed by a single terminal Done event that
// carries the fully populated DiffReport.
type DiffStreamEvent struct {
	Kind   DiffStreamEventKind
	Item   DifferenceItem
	Report *DiffReport
}

// CompareStream runs Compare and streams its individual difference items on the
// returned channel. The channel is buffered and closed after the terminal Done
// event, so callers can range over it without blocking the producer.
func CompareStream(oldMod, newMod *ast.ModuleInfo, oldVer, newVer string, hazardLookups ...ast.HazardLookup) <-chan DiffStreamEvent {
	ch := make(chan DiffStreamEvent, 64)
	go func() {
		defer close(ch)
		report := Compare(oldMod, newMod, oldVer, newVer, hazardLookups...)
		for _, item := range report.BreakingChanges {
			ch <- DiffStreamEvent{Kind: DiffStreamBreaking, Item: item}
		}
		for _, item := range report.Warnings {
			ch <- DiffStreamEvent{Kind: DiffStreamWarning, Item: item}
		}
		for _, item := range report.NewFeatures {
			ch <- DiffStreamEvent{Kind: DiffStreamFeature, Item: item}
		}
		ch <- DiffStreamEvent{Kind: DiffStreamDone, Report: report}
	}()
	return ch
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
