package goldentest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssertGoldenMatch(t *testing.T) {
	tmpDir := t.TempDir()
	goldenFile := filepath.Join(tmpDir, "sub", "test.golden")
	content := []byte("canonical golden content")

	// 1. Initial creation with updateGolden = true
	AssertGoldenMatch(t, goldenFile, content, true)

	// 2. Verification against saved content
	AssertGoldenMatch(t, goldenFile, content, false)

	// 3. Verification with UPDATE_GOLDEN env var
	t.Setenv("UPDATE_GOLDEN", "true")
	AssertGoldenMatch(t, goldenFile, []byte("updated content"), false)

	// Read file to verify it updated
	saved, err := os.ReadFile(goldenFile)
	require.NoError(t, err)
	assert.Equal(t, "updated content", string(saved))
}

func TestAssertGoldenMatch_WorkspaceSanitization(t *testing.T) {
	root := getRepoRoot()
	require.NotEmpty(t, root, "getRepoRoot must locate the module root from the test working directory")

	goldenFile := filepath.Join(t.TempDir(), "paths.golden")
	AssertGoldenMatch(t, goldenFile, []byte("root="+root+"\nslash="+filepath.ToSlash(root)+"\n"), true)

	saved, err := os.ReadFile(goldenFile)
	require.NoError(t, err)
	assert.Equal(t, "root=<WORKSPACE>\nslash=<WORKSPACE>\n", string(saved))
	assert.False(t, strings.Contains(string(saved), t.TempDir()))
}

func TestGetRepoRootOutsideAModule(t *testing.T) {
	// A temporary directory has no go.mod anywhere up the tree.
	outside := t.TempDir()
	t.Chdir(outside)
	assert.Empty(t, getRepoRoot())
}

func TestGetRepoRootWithoutAWorkingDirectory(t *testing.T) {
	// Removing the process working directory makes os.Getwd fail.
	dir := t.TempDir()
	sub := filepath.Join(dir, "gone")
	require.NoError(t, os.MkdirAll(sub, 0750))
	t.Chdir(sub)
	require.NoError(t, os.RemoveAll(dir))

	assert.Empty(t, getRepoRoot())
}

func TestAssertGoldenMatch_WithoutWorkspaceRoot(t *testing.T) {
	outside := t.TempDir()
	t.Chdir(outside)
	require.Empty(t, getRepoRoot())

	goldenFile := filepath.Join(outside, "rooted.golden")
	AssertGoldenMatch(t, goldenFile, []byte("plain content"), true)

	saved, err := os.ReadFile(goldenFile)
	require.NoError(t, err)
	assert.Equal(t, "plain content", string(saved), "content is written verbatim outside a module")
}
