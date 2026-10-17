package ast

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistentCacheLifecycle(t *testing.T) {
	isolateModuleCache(t)

	// Clear with empty dir does nothing
	SetPersistentCacheDir("")
	assert.Equal(t, "", GetPersistentCacheDir())
	require.NoError(t, ClearPersistentCache())

	cacheDir := t.TempDir()
	SetPersistentCacheDir(cacheDir)
	assert.Equal(t, cacheDir, GetPersistentCacheDir())
	t.Cleanup(func() { SetPersistentCacheDir("") })

	modDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "main.tf"), []byte(`variable "p" { type = string }`), 0o600))

	// First inspection populates L1 and L2
	info1, err := Inspect(modDir)
	require.NoError(t, err)
	assert.Equal(t, "string", info1.Variables["p"].Type)

	// Verify L2 disk file was created
	files, err := os.ReadDir(cacheDir)
	require.NoError(t, err)
	require.Len(t, files, 1)

	// Reset L1 in-memory cache to force reading from L2 disk cache
	resetModuleCache()

	info2, err := Inspect(modDir)
	require.NoError(t, err)
	assert.Equal(t, "string", info2.Variables["p"].Type)

	// Corrupt L2 disk file: should gracefully fallback to source parsing
	resetModuleCache()
	diskFilePath := filepath.Join(cacheDir, files[0].Name())
	require.NoError(t, os.WriteFile(diskFilePath, []byte("corrupt json"), 0o600))

	info3, err := Inspect(modDir)
	require.NoError(t, err)
	assert.Equal(t, "string", info3.Variables["p"].Type)

	// Clearing removes the cache entries but keeps the directory itself. It used to delete the
	// whole tree, so `--cache-dir .` destroyed the repository.
	require.NoError(t, ClearPersistentCache())
	assert.DirExists(t, cacheDir, "the cache directory itself must survive a clear")
	remaining, err := os.ReadDir(cacheDir)
	require.NoError(t, err)
	assert.Empty(t, remaining, "cache entries must be removed")

	// A second clear of an already-empty directory is a no-op, not an error.
	require.NoError(t, ClearPersistentCache())

	// Clearing a directory that no longer exists is not an error either.
	require.NoError(t, os.RemoveAll(cacheDir))
	require.NoError(t, ClearPersistentCache())
}

// TestClearPersistentCacheRefusesForeignDirectories is the regression test for the destructive
// behaviour: ClearPersistentCache used to call os.RemoveAll on whatever --cache-dir named, so a
// typo destroyed an unrelated directory. It now refuses anything it did not write.
func TestClearPersistentCacheRefusesForeignDirectories(t *testing.T) {
	t.Run("refuses a directory holding a non-cache file", func(t *testing.T) {
		dir := t.TempDir()
		victim := filepath.Join(dir, "main.tf")
		require.NoError(t, os.WriteFile(victim, []byte(`variable "p" { type = string }`), 0o600))
		t.Cleanup(func() { SetPersistentCacheDir("") })
		SetPersistentCacheDir(dir)

		err := ClearPersistentCache()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refusing to clear")
		assert.FileExists(t, victim, "a file this cache never wrote must not be deleted")
	})

	t.Run("refuses the repository root", func(t *testing.T) {
		// The realistic footgun: --cache-dir .
		dir := t.TempDir()
		readme := filepath.Join(dir, "README.md")
		require.NoError(t, os.WriteFile(readme, []byte("# module"), 0o600))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "modules"), 0o750))
		t.Cleanup(func() { SetPersistentCacheDir("") })
		SetPersistentCacheDir(dir)

		err := ClearPersistentCache()
		require.Error(t, err)
		assert.FileExists(t, readme)
		assert.DirExists(t, filepath.Join(dir, "modules"))
	})

	t.Run("refuses a json file that is not a fingerprint", func(t *testing.T) {
		// A .json suffix alone is not enough: the name must be a hex SHA-256, which is the only
		// shape cacheModule writes.
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o600))
		t.Cleanup(func() { SetPersistentCacheDir("") })
		SetPersistentCacheDir(dir)

		err := ClearPersistentCache()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refusing to clear")
		assert.FileExists(t, filepath.Join(dir, "package.json"))
	})

	t.Run("clears a directory holding only cache entries", func(t *testing.T) {
		dir := t.TempDir()
		fingerprint := strings.Repeat("a", 64)
		require.NoError(t, os.WriteFile(filepath.Join(dir, fingerprint+".json"), []byte("{}"), 0o600))
		t.Cleanup(func() { SetPersistentCacheDir("") })
		SetPersistentCacheDir(dir)

		require.NoError(t, ClearPersistentCache())
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("reports an unreadable cache directory", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root: directory permissions are not enforced")
		}
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o000))
		t.Cleanup(func() {
			_ = os.Chmod(dir, 0o700)
			SetPersistentCacheDir("")
		})
		SetPersistentCacheDir(dir)

		err := ClearPersistentCache()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read cache directory")
	})
}

// TestIsCacheFingerprint pins the filename contract directly, including the shapes it must reject.
func TestIsCacheFingerprint(t *testing.T) {
	fingerprint := strings.Repeat("0", 64)

	assert.True(t, isCacheFingerprint(fingerprint+".json"), "a 64-char hex name is a cache entry")
	assert.True(t, isCacheFingerprint(strings.Repeat("a1b2c3d4", 8)+".json"))

	// Wrong length.
	assert.False(t, isCacheFingerprint(strings.Repeat("a", 63)+".json"))
	assert.False(t, isCacheFingerprint(strings.Repeat("a", 65)+".json"))

	// Not hex.
	assert.False(t, isCacheFingerprint(strings.Repeat("z", 64)+".json"))
	assert.False(t, isCacheFingerprint(strings.Repeat("A", 64)+".json"), "uppercase hex is not written")

	// Missing or wrong suffix.
	assert.False(t, isCacheFingerprint(fingerprint))
	assert.False(t, isCacheFingerprint(fingerprint+".yaml"))
	assert.False(t, isCacheFingerprint(fingerprint+".json.bak"))
	assert.False(t, isCacheFingerprint(""))
}

func TestInspectModulesParallel(t *testing.T) {
	ctx := context.Background()

	// Empty dirs returns nil
	res, err := InspectModulesParallel(ctx, nil, 2)
	require.NoError(t, err)
	assert.Nil(t, res)

	// Create test modules
	dir1 := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "main.tf"), []byte(`variable "mod1" { type = string }`), 0o600))

	dir2 := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "main.tf"), []byte(`variable "mod2" { type = number }`), 0o600))

	// Parallel inspection with default concurrency (concurrency <= 0)
	modules, err := InspectModulesParallel(ctx, []string{dir1, dir2}, 0)
	require.NoError(t, err)
	require.Len(t, modules, 2)
	assert.Contains(t, modules[0].Variables, "mod1")
	assert.Contains(t, modules[1].Variables, "mod2")

	// Error branch: one module fails to parse
	badDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(badDir, "main.tf"), []byte(`invalid {`), 0o600))

	_, err = InspectModulesParallel(ctx, []string{dir1, badDir}, 2)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parallel inspect failed")

	// Canceled context
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = InspectModulesParallel(canceledCtx, []string{dir1, dir2}, 1)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestClearPersistentCacheReportsRemovalFailure covers the per-entry removal failing after the
// directory has been validated. Contents are checked before anything is removed, so this only
// happens when the directory becomes unwritable mid-clear.
func TestClearPersistentCacheReportsRemovalFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	dir := t.TempDir()
	fingerprint := strings.Repeat("b", 64)
	require.NoError(t, os.WriteFile(filepath.Join(dir, fingerprint+".json"), []byte("{}"), 0o600))
	// The name validates, so the guard passes; the unlink then fails on the read-only directory.
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o700)
		SetPersistentCacheDir("")
	})
	SetPersistentCacheDir(dir)

	err := ClearPersistentCache()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to remove cache entry")
}
