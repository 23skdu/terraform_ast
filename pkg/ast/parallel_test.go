package ast

import (
	"context"
	"os"
	"path/filepath"
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

	// Clear persistent cache
	require.NoError(t, ClearPersistentCache())
	assert.NoDirExists(t, cacheDir)
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
