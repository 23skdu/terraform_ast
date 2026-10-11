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
