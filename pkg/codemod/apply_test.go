package codemod

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyRemovedInputsModifiesFileInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.tf")
	content := `module "gke" {
  source = "./upstream"
  old_input = "val"
  keep = "val"
}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	modified, err := ApplyRemovedInputs(dir, "gke", []string{"old_input"})
	require.NoError(t, err)
	assert.Equal(t, []string{path}, modified)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(after), "old_input")
	assert.Contains(t, string(after), "keep")
}

func TestApplyRemovedInputsValidationAndErrors(t *testing.T) {
	_, err := ApplyRemovedInputs(t.TempDir(), "", []string{"a"})
	assert.ErrorContains(t, err, "module name is required")

	mod, err := ApplyRemovedInputs(t.TempDir(), "gke", nil)
	require.NoError(t, err)
	assert.Nil(t, mod)

	_, err = ApplyRemovedInputs("/non/existent/dir", "gke", []string{"a"})
	assert.ErrorContains(t, err, "read wrapper directory")

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte("invalid {"), 0o600))
	_, err = ApplyRemovedInputs(dir, "gke", []string{"a"})
	assert.ErrorContains(t, err, "parse Terraform file")

	// Skip non-tf and non-matching blocks
	dir2 := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir2, "subdir"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "file.txt"), []byte("txt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "other.tf"), []byte("module \"other\" { a = 1 }\n"), 0o600))
	mod2, err := ApplyRemovedInputs(dir2, "gke", []string{"a"})
	require.NoError(t, err)
	assert.Empty(t, mod2)
}

func TestApplyRemovedInputsUnreadableAndUnwritable(t *testing.T) {
	dir := t.TempDir()
	unreadable := filepath.Join(dir, "locked.tf")
	require.NoError(t, os.WriteFile(unreadable, []byte("module \"gke\" { a = 1 }\n"), 0o000))
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o644) })

	if _, err := os.ReadFile(unreadable); err == nil {
		t.Skip("filesystem allows reading mode-000 files")
	}
	_, err := ApplyRemovedInputs(dir, "gke", []string{"a"})
	assert.ErrorContains(t, err, "read Terraform file")
}

func TestApplyRemovedInputsWriteError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.tf")
	require.NoError(t, os.WriteFile(target, []byte("module \"gke\" { a = 1 }\n"), 0o600))
	// Make directory read-only so write fails if supported
	require.NoError(t, os.Chmod(target, 0o400))
	t.Cleanup(func() { _ = os.Chmod(target, 0o600) })

	if err := os.WriteFile(target, []byte("test"), 0o600); err == nil {
		t.Skip("filesystem allows overwriting read-only files")
	}
	_, err := ApplyRemovedInputs(dir, "gke", []string{"a"})
	assert.ErrorContains(t, err, "write updated Terraform file")
}

func TestAddMissingInputsModifiesFileInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.tf")
	content := `module "gke" {
  source = "./upstream"
  keep   = "val"
}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	modified, err := AddMissingInputs(dir, "gke", map[string]string{
		"new_required": "default-val",
		"keep":         "should-not-overwrite",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{path}, modified)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(after), `new_required = "default-val"`)
	assert.Contains(t, string(after), `keep`)
	assert.Contains(t, string(after), `"val"`)
}

func TestAddMissingInputsValidationAndErrors(t *testing.T) {
	_, err := AddMissingInputs(t.TempDir(), "", map[string]string{"a": "b"})
	assert.ErrorContains(t, err, "module name is required")

	mod, err := AddMissingInputs(t.TempDir(), "gke", nil)
	require.NoError(t, err)
	assert.Nil(t, mod)

	_, err = AddMissingInputs("/non/existent/dir", "gke", map[string]string{"a": "b"})
	assert.ErrorContains(t, err, "read wrapper directory")

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte("invalid {"), 0o600))
	_, err = AddMissingInputs(dir, "gke", map[string]string{"a": "b"})
	assert.ErrorContains(t, err, "parse Terraform file")

	// Skip non-tf and non-matching blocks
	dir2 := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir2, "subdir"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "file.txt"), []byte("txt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "other.tf"), []byte("module \"other\" { a = 1 }\n"), 0o600))
	mod2, err := AddMissingInputs(dir2, "gke", map[string]string{"a": "b"})
	require.NoError(t, err)
	assert.Empty(t, mod2)
}

func TestAddMissingInputsUnreadableAndUnwritable(t *testing.T) {
	dir := t.TempDir()
	unreadable := filepath.Join(dir, "locked.tf")
	require.NoError(t, os.WriteFile(unreadable, []byte("module \"gke\" {}\n"), 0o000))
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o644) })

	if _, err := os.ReadFile(unreadable); err == nil {
		t.Skip("filesystem allows reading mode-000 files")
	}
	_, err := AddMissingInputs(dir, "gke", map[string]string{"a": "b"})
	assert.ErrorContains(t, err, "read Terraform file")
}

func TestAddMissingInputsWriteError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.tf")
	require.NoError(t, os.WriteFile(target, []byte("module \"gke\" {}\n"), 0o600))
	require.NoError(t, os.Chmod(target, 0o400))
	t.Cleanup(func() { _ = os.Chmod(target, 0o600) })

	if err := os.WriteFile(target, []byte("test"), 0o600); err == nil {
		t.Skip("filesystem allows overwriting read-only files")
	}
	_, err := AddMissingInputs(dir, "gke", map[string]string{"a": "b"})
	assert.ErrorContains(t, err, "write updated Terraform file")
}

func TestFormatMovedBlocks(t *testing.T) {
	assert.Empty(t, FormatMovedBlocks(nil))

	blocks := []MovedBlock{
		{From: "module.old", To: "module.new"},
		{From: "", To: "module.ignored"},
		{From: "module.ignored2", To: ""},
	}
	formatted := FormatMovedBlocks(blocks)
	assert.Contains(t, formatted, "moved {")
	assert.Contains(t, formatted, "from = module.old")
	assert.Contains(t, formatted, "to   = module.new")
	assert.NotContains(t, formatted, "module.ignored")
}

func TestWriteMovedBlocks(t *testing.T) {
	// Empty blocks does nothing
	require.NoError(t, WriteMovedBlocks("/path/not/created", nil))

	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "moved.tf")
	blocks := []MovedBlock{
		{From: "module.a", To: "module.b"},
	}

	// Creates new file and directory
	require.NoError(t, WriteMovedBlocks(target, blocks))
	content1, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Contains(t, string(content1), "from = module.a")

	// Appends to existing file
	moreBlocks := []MovedBlock{
		{From: "module.c", To: "module.d"},
	}
	require.NoError(t, WriteMovedBlocks(target, moreBlocks))
	content2, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Contains(t, string(content2), "from = module.a")
	assert.Contains(t, string(content2), "from = module.c")

	// Calling again with the same blocks is idempotent (does not duplicate)
	require.NoError(t, WriteMovedBlocks(target, moreBlocks))
	content3, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, string(content2), string(content3))

	// Blank from/to blocks are ignored
	blankBlocks := []MovedBlock{
		{From: "  ", To: "module.e"},
		{From: "module.e", To: ""},
	}
	require.NoError(t, WriteMovedBlocks(target, blankBlocks))
	content4, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, string(content3), string(content4))
}

func TestWriteMovedBlocksErrors(t *testing.T) {
	// MkdirAll error: path is a file
	dir := t.TempDir()
	filePath := filepath.Join(dir, "blocked")
	require.NoError(t, os.WriteFile(filePath, []byte("x"), 0o600))
	err := WriteMovedBlocks(filepath.Join(filePath, "moved.tf"), []MovedBlock{{From: "a", To: "b"}})
	assert.ErrorContains(t, err, "create directory for moved blocks")

	// Read error: directory instead of file
	dirAsFile := filepath.Join(dir, "dirAsFile")
	require.NoError(t, os.Mkdir(dirAsFile, 0o750))
	err = WriteMovedBlocks(dirAsFile, []MovedBlock{{From: "a", To: "b"}})
	assert.ErrorContains(t, err, "read existing moved blocks file")

	// Write error: read-only file
	readOnlyFile := filepath.Join(dir, "readonly.tf")
	require.NoError(t, os.WriteFile(readOnlyFile, []byte("existing"), 0o400))
	t.Cleanup(func() { _ = os.Chmod(readOnlyFile, 0o600) })
	if err := os.WriteFile(readOnlyFile, []byte("overwrite"), 0o600); err == nil {
		t.Skip("filesystem allows overwriting read-only files")
	}
	err = WriteMovedBlocks(readOnlyFile, []MovedBlock{{From: "a", To: "b"}})
	assert.ErrorContains(t, err, "write moved blocks file")
}

func TestSynthesizeMovedBlocks(t *testing.T) {
	shifts := []ast.ResourceAddressShift{
		{
			OldAddress: ast.ResourceAddress{Type: "aws_subnet", Name: "this"},
			NewAddress: ast.ResourceAddress{Type: "aws_subnet", Name: "renamed"},
		},
		{
			OldAddress: ast.ResourceAddress{ModulePath: "module.old", Type: "aws_vpc", Name: "main"},
			NewAddress: ast.ResourceAddress{ModulePath: "module.new", Type: "aws_vpc", Name: "main"},
		},
		// Invalid / empty address shift should be skipped
		{
			OldAddress: ast.ResourceAddress{Type: "aws_s3_bucket", Name: "data"},
			NewAddress: ast.ResourceAddress{},
		},
		// Same address shift should be skipped
		{
			OldAddress: ast.ResourceAddress{Type: "aws_iam_role", Name: "role"},
			NewAddress: ast.ResourceAddress{Type: "aws_iam_role", Name: "role"},
		},
	}

	blocks := SynthesizeMovedBlocksForShifts(shifts)
	require.Len(t, blocks, 2)
	assert.Equal(t, "aws_subnet.this", blocks[0].From)
	assert.Equal(t, "aws_subnet.renamed", blocks[0].To)
	assert.Equal(t, "module.old.aws_vpc.main", blocks[1].From)
	assert.Equal(t, "module.new.aws_vpc.main", blocks[1].To)

	// Test duplicate shifts deduplication
	shiftsWithDup := append(shifts, shifts[0])
	blocksDup := SynthesizeMovedBlocksForShifts(shiftsWithDup)
	assert.Len(t, blocksDup, 2)

	// Test SynthesizeAndWriteMovedBlocks
	target := filepath.Join(t.TempDir(), "moved.tf")
	require.NoError(t, SynthesizeAndWriteMovedBlocks(target, shifts))
	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Contains(t, string(content), `from = aws_subnet.this`)
	assert.Contains(t, string(content), `to   = aws_subnet.renamed`)
}
