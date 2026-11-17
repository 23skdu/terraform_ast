// Package drift compares recorded Terraform state snapshots and reports resource-level
// drift between a baseline (state) and a live (or recorded) snapshot.
package drift

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Resource is one entry in a recorded state snapshot.
type Resource struct {
	Address    string         `json:"address"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// Snapshot is a recorded collection of resources.
type Snapshot struct {
	Resources []Resource `json:"resources"`
}

// ResourceDrift describes one resource present in both snapshots whose attributes differ.
type ResourceDrift struct {
	Address           string   `json:"address"`
	ChangedAttributes []string `json:"changed_attributes"`
}

// Report summarizes the drift between two snapshots.
type Report struct {
	Added    []string        `json:"added,omitempty"`
	Removed  []string        `json:"removed,omitempty"`
	Changed  []ResourceDrift `json:"changed,omitempty"`
	HasDrift bool            `json:"has_drift"`
	Summary  string          `json:"summary"`
}

// CompareSnapshots compares a baseline snapshot against a live snapshot and returns a
// deterministic drift report. Resources are keyed by their address.
func CompareSnapshots(baseline, live Snapshot) Report {
	baselineMap := make(map[string]Resource, len(baseline.Resources))
	for _, r := range baseline.Resources {
		baselineMap[r.Address] = r
	}
	liveMap := make(map[string]Resource, len(live.Resources))
	for _, r := range live.Resources {
		liveMap[r.Address] = r
	}

	var added, removed []string
	for addr := range liveMap {
		if _, ok := baselineMap[addr]; !ok {
			added = append(added, addr)
		}
	}
	for addr := range baselineMap {
		if _, ok := liveMap[addr]; !ok {
			removed = append(removed, addr)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)

	var changed []ResourceDrift
	for addr, baseRes := range baselineMap {
		liveRes, ok := liveMap[addr]
		if !ok {
			continue
		}
		attrs := changedAttributes(baseRes.Attributes, liveRes.Attributes)
		if len(attrs) > 0 {
			changed = append(changed, ResourceDrift{Address: addr, ChangedAttributes: attrs})
		}
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].Address < changed[j].Address })

	report := Report{
		Added:   added,
		Removed: removed,
		Changed: changed,
	}
	report.HasDrift = len(added) > 0 || len(removed) > 0 || len(changed) > 0
	report.Summary = fmt.Sprintf("baseline=%d live=%d added=%d removed=%d changed=%d",
		len(baseline.Resources), len(live.Resources), len(added), len(removed), len(changed))
	if report.HasDrift {
		report.Summary += " (DRIFT DETECTED)"
	} else {
		report.Summary += " (IN SYNC)"
	}
	if report.Added == nil {
		report.Added = []string{}
	}
	if report.Removed == nil {
		report.Removed = []string{}
	}
	if report.Changed == nil {
		report.Changed = []ResourceDrift{}
	}
	return report
}

func changedAttributes(a, b map[string]any) []string {
	keys := make(map[string]struct{})
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range b {
		keys[k] = struct{}{}
	}
	var changed []string
	for k := range keys {
		va, oka := a[k]
		vb, okb := b[k]
		if !oka || !okb || !equalValue(va, vb) {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	return changed
}

func equalValue(a, b any) bool {
	// JSON round-trips through map[string]any produce float64 for numbers; compare via
	// canonical JSON encoding to avoid type mismatches.
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ab) == string(bb)
}

// LoadSnapshot reads a recorded state snapshot from disk.
func LoadSnapshot(path string) (Snapshot, error) {
	// #nosec G304 -- snapshot path is operator-supplied by design.
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read snapshot %s: %w", path, err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return Snapshot{}, fmt.Errorf("decode snapshot %s: %w", path, err)
	}
	return snap, nil
}

// SaveSnapshot writes a snapshot to disk.
func SaveSnapshot(path string, snap Snapshot) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	data = append(data, '\n')
	// #nosec G306 -- snapshot is intentionally readable.
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write snapshot %s: %w", path, err)
	}
	return nil
}
