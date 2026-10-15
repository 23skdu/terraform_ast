package codemod

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreviewRemovedInputsIsNonMutating(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.tf")
	original := `module "gke" {
  source = "./upstream"

  old_input = "remove-me"
  keep      = "keep-me"
}
`
	require.NoError(t, os.WriteFile(path, []byte(original), 0600))

	preview, err := PreviewRemovedInputs(dir, "gke", []string{"old_input"})
	require.NoError(t, err)
	assert.Contains(t, preview, "--- a/main.tf")
	assert.Contains(t, preview, "+++ b/main.tf")
	assert.Contains(t, preview, "-  old_input = \"remove-me\"")
	assert.NotContains(t, preview[strings.Index(preview, "+++ b/main.tf"):], "+  old_input")
	assert.Contains(t, preview, "+  keep = \"keep-me\"")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, original, string(after))
}

func TestPreviewRemovedInputsValidatesModuleAndFile(t *testing.T) {
	_, err := PreviewRemovedInputs(t.TempDir(), "", []string{"old"})
	assert.ErrorContains(t, err, "module name is required")

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte("not valid {"), 0600))
	_, err = PreviewRemovedInputs(dir, "gke", []string{"old"})
	assert.ErrorContains(t, err, "parse Terraform file")
}

func TestPreviewRemovedInputsShortCircuits(t *testing.T) {
	// No module name: rejected before touching the filesystem.
	_, err := PreviewRemovedInputs("/non/existent/dir", "", []string{"old"})
	assert.ErrorContains(t, err, "module name is required")

	// Nothing to remove: no patch is produced and the directory is never read.
	preview, err := PreviewRemovedInputs("/non/existent/dir", "gke", nil)
	require.NoError(t, err)
	assert.Empty(t, preview)

	// Unreadable wrapper directory.
	_, err = PreviewRemovedInputs("/non/existent/dir", "gke", []string{"old"})
	assert.ErrorContains(t, err, "read wrapper directory")
}

func TestPreviewRemovedInputsUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	unreadable := filepath.Join(dir, "locked.tf")
	require.NoError(t, os.WriteFile(unreadable, []byte("module \"gke\" {}\n"), 0000))
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0644) })

	if _, err := os.ReadFile(unreadable); err == nil {
		t.Skip("filesystem allows reading mode-000 files; skipping the unreadable-file branch")
	}
	_, err := PreviewRemovedInputs(dir, "gke", []string{"old"})
	assert.ErrorContains(t, err, "read Terraform file")
}

func TestPreviewRemovedInputsSkipsNonPatchableFiles(t *testing.T) {
	dir := t.TempDir()
	// JSON modules and blocks with a different label cannot be patched.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "module.tf.json"), []byte(`{"module":{"gke":{"old_input":"x"}}}`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.tf"), []byte("module \"other\" {\n  old_input = \"x\"\n}\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("old_input"), 0600))

	preview, err := PreviewRemovedInputs(dir, "gke", []string{"old_input"})
	require.NoError(t, err)
	assert.Empty(t, preview, "no patchable module block was found")
}

func TestUnifiedReplacementHandlesEmptyContent(t *testing.T) {
	// Empty original content collapses to zero removed lines.
	patch := unifiedReplacement("main.tf", "", "resource \"null_resource\" \"a\" {}\n")
	assert.Contains(t, patch, "@@ -1,0 +1,1 @@")
	assert.NotContains(t, patch, "\n-\n")

	// Empty replacement content collapses to zero added lines.
	patch = unifiedReplacement("main.tf", "resource \"null_resource\" \"a\" {}\n", "")
	assert.Contains(t, patch, "@@ -1,1 +1,0 @@")
	assert.NotContains(t, patch, "+\n")

	// Content without a trailing newline behaves identically.
	patch = unifiedReplacement("main.tf", `variable "a" {}`, `variable "b" {}`)
	assert.Contains(t, patch, "@@ -1,1 +1,1 @@")
	assert.Contains(t, patch, "-variable \"a\" {}")
	assert.Contains(t, patch, "+variable \"b\" {}")
}
