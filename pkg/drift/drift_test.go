package drift

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompareSnapshots(t *testing.T) {
	baseline := Snapshot{Resources: []Resource{
		{Address: "aws_instance.a", Attributes: map[string]any{"ami": "ami-1", "type": "t2.micro"}},
		{Address: "aws_instance.b", Attributes: map[string]any{"ami": "ami-2"}},
	}}
	live := Snapshot{Resources: []Resource{
		{Address: "aws_instance.a", Attributes: map[string]any{"ami": "ami-1", "type": "t2.large"}},
		{Address: "aws_instance.c", Attributes: map[string]any{"ami": "ami-3"}},
	}}

	report := CompareSnapshots(baseline, live)
	assert.True(t, report.HasDrift)
	assert.Equal(t, []string{"aws_instance.c"}, report.Added)
	assert.Equal(t, []string{"aws_instance.b"}, report.Removed)
	require.Len(t, report.Changed, 1)
	assert.Equal(t, "aws_instance.a", report.Changed[0].Address)
	assert.Equal(t, []string{"type"}, report.Changed[0].ChangedAttributes)
	assert.Contains(t, report.Summary, "DRIFT DETECTED")

	// In sync.
	inSync := CompareSnapshots(baseline, baseline)
	assert.False(t, inSync.HasDrift)
	assert.Empty(t, inSync.Added)
	assert.Empty(t, inSync.Removed)
	assert.Empty(t, inSync.Changed)
	assert.Contains(t, inSync.Summary, "IN SYNC")

	// Empty snapshots.
	empty := CompareSnapshots(Snapshot{}, Snapshot{})
	assert.False(t, empty.HasDrift)
	assert.Equal(t, []string{}, empty.Added)
	assert.Equal(t, []string{}, empty.Removed)
	assert.Equal(t, []ResourceDrift{}, empty.Changed)
}

func TestCompareSnapshotsMultipleChanged(t *testing.T) {
	baseline := Snapshot{Resources: []Resource{
		{Address: "r1", Attributes: map[string]any{"v": 1}},
		{Address: "r2", Attributes: map[string]any{"v": 2}},
	}}
	live := Snapshot{Resources: []Resource{
		{Address: "r1", Attributes: map[string]any{"v": 10}},
		{Address: "r2", Attributes: map[string]any{"v": 20}},
	}}
	report := CompareSnapshots(baseline, live)
	require.Len(t, report.Changed, 2)
	assert.Equal(t, "r1", report.Changed[0].Address)
	assert.Equal(t, "r2", report.Changed[1].Address)
}
func TestChangedAttributes(t *testing.T) {
	attrs := changedAttributes(
		map[string]any{"a": 1, "b": "x", "c": true},
		map[string]any{"a": 2, "b": "x", "d": nil},
	)
	assert.Equal(t, []string{"a", "c", "d"}, attrs)

	assert.Nil(t, changedAttributes(nil, nil))
	// changedAttributes returns nil when there are no differences.
	assert.Nil(t, changedAttributes(map[string]any{"a": 1}, map[string]any{"a": 1}))
}

func TestEqualValue(t *testing.T) {
	assert.True(t, equalValue(1, 1.0)) // both marshal to 1? 1 marshals "1", 1.0 marshals "1"
	assert.False(t, equalValue(1, 2))
	assert.False(t, equalValue(func() {}, 1))
}

func TestLoadSaveSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")
	snap := Snapshot{Resources: []Resource{{Address: "r1", Attributes: map[string]any{"k": "v"}}}}
	require.NoError(t, SaveSnapshot(path, snap))
	loaded, err := LoadSnapshot(path)
	require.NoError(t, err)
	assert.Equal(t, snap, loaded)

	// Missing file.
	_, err = LoadSnapshot(filepath.Join(dir, "missing.json"))
	assert.Error(t, err)
	// Bad JSON.
	bad := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte("{"), 0o600))
	_, err = LoadSnapshot(bad)
	assert.Error(t, err)

	// SaveSnapshot encode error.
	badSnap := Snapshot{Resources: []Resource{{Address: "r", Attributes: map[string]any{"f": func() {}}}}}
	err = SaveSnapshot(filepath.Join(dir, "bad.json"), badSnap)
	assert.Error(t, err)
	// Save nil-ish? encode always succeeds; test directory target.
	err = SaveSnapshot(dir, snap)
	assert.Error(t, err)
}
