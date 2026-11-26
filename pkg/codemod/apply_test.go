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
		"flag_true":    "true",
		"flag_false":   "false",
		"count_num":    "0",
		"keep":         "should-not-overwrite",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{path}, modified)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(after), `new_required = "default-val"`)
	assert.Contains(t, string(after), `flag_true    = true`)
	assert.Contains(t, string(after), `flag_false   = false`)
	assert.Contains(t, string(after), `count_num    = 0`)
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
		{From: "aws_instance.server[count]", To: "aws_instance.new_server[for_each]"},
		{From: "bad [ traversal", To: "other"},
		{From: "", To: "module.ignored"},
		{From: "module.ignored2", To: ""},
	}
	formatted := FormatMovedBlocks(blocks)
	assert.Contains(t, formatted, "moved {")
	assert.Contains(t, formatted, "from = module.old")
	assert.Contains(t, formatted, "to   = module.new")
	assert.Contains(t, formatted, "from = aws_instance.server")
	assert.Contains(t, formatted, "to   = aws_instance.new_server")
	assert.NotContains(t, formatted, "[count]")
	assert.NotContains(t, formatted, "[for_each]")
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

	// covered skip
	covered := map[string]struct{}{"aws_subnet.this->aws_subnet.renamed": {}}
	blocksCov := SynthesizeMovedBlocksForShifts(shifts, covered)
	assert.Len(t, blocksCov, 1)

	// CoveredMovedAddresses helper
	ca := CoveredMovedAddresses([]ast.MovedBlockDef{{From: "a", To: "b"}})
	assert.Contains(t, ca, "a->b")

	// Test SynthesizeAndWriteMovedBlocks
	target := filepath.Join(t.TempDir(), "moved.tf")
	require.NoError(t, SynthesizeAndWriteMovedBlocks(target, shifts))
	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Contains(t, string(content), `from = aws_subnet.this`)
	assert.Contains(t, string(content), `to   = aws_subnet.renamed`)
}

func TestFormatImportBlocks(t *testing.T) {
	assert.Equal(t, "", FormatImportBlocks(nil))
	assert.Equal(t, "", FormatImportBlocks([]ImportBlock{{To: "", ID: "id"}, {To: "r", ID: ""}}))
	out := FormatImportBlocks([]ImportBlock{{To: "aws_s3_bucket.b", ID: "bucket-id", Provider: "aws.west"}})
	assert.Contains(t, out, "import {")
	assert.Contains(t, out, "aws_s3_bucket.b")
	assert.Contains(t, out, "bucket-id")
	assert.Contains(t, out, "aws.west")
}

func TestNormalizeImportBlocks(t *testing.T) {
	normalized := NormalizeImportBlocks([]ImportBlock{
		{To: "  aws_s3_bucket.b ", ID: " b-id ", Provider: " aws.west "},
		{To: "aws_s3_bucket.b", ID: "b-id", Provider: "aws.west"}, // duplicate after trimming
		{To: "aws_s3_bucket.b", ID: "a-id"},
		{To: "aws_s3_bucket.b", ID: "a-id", Provider: "aws.east"},
		{To: "aws_s3_bucket.a", ID: "z-id"},
		{To: "", ID: "ignored"},
	})
	assert.Len(t, normalized, 4)
	assert.Equal(t, "aws_s3_bucket.a", normalized[0].To)
	assert.Equal(t, "a-id", normalized[1].ID)
	assert.Equal(t, "aws.east", normalized[2].Provider)
	assert.Equal(t, "aws.west", normalized[3].Provider)
}

func TestWriteImportBlocksIfChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "imports.tf")
	blocks := []ImportBlock{{To: "aws_s3_bucket.b", ID: "b-id"}}

	changed, err := WriteImportBlocksIfChanged(path, blocks)
	require.NoError(t, err)
	assert.True(t, changed)

	changed, err = WriteImportBlocksIfChanged(path, blocks)
	require.NoError(t, err)
	assert.False(t, changed)

	changed, err = WriteImportBlocksIfChanged(filepath.Join(dir, "empty.tf"), []ImportBlock{{To: "", ID: ""}})
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestWriteImportBlocks(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	path := filepath.Join(dir, "imports.tf")
	blocks := []ImportBlock{{To: "aws_s3_bucket.b", ID: "bucket-id"}}

	require.NoError(t, WriteImportBlocks(path, blocks))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "import {")
	assert.Contains(t, string(data), "bucket-id")

	// Idempotent: second write does not duplicate.
	require.NoError(t, WriteImportBlocks(path, blocks))
	data2, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(data2))

	// Empty blocks is a no-op.
	emptyPath := filepath.Join(dir, "empty.tf")
	require.NoError(t, WriteImportBlocks(emptyPath, nil))
	_, err = os.Stat(emptyPath)
	assert.True(t, os.IsNotExist(err))

	// Skip invalid blocks.
	invalidPath := filepath.Join(dir, "invalid.tf")
	require.NoError(t, WriteImportBlocks(invalidPath, []ImportBlock{{To: "", ID: "x"}, {To: "r", ID: ""}}))
	_, err = os.Stat(invalidPath)
	assert.True(t, os.IsNotExist(err))

	// Append path: existing file with different import block.
	appendPath := filepath.Join(dir, "append.tf")
	require.NoError(t, os.WriteFile(appendPath, []byte("import { to = aws_s3_bucket.a id = \"a-id\" }\n"), 0o600))
	require.NoError(t, WriteImportBlocks(appendPath, []ImportBlock{{To: "aws_s3_bucket.b", ID: "b-id"}}))
	data3, err := os.ReadFile(appendPath)
	require.NoError(t, err)
	assert.Contains(t, string(data3), "b-id")

	// MkdirAll error.
	err = WriteImportBlocks(filepath.Join(blocker, "imports.tf"), blocks)
	assert.Error(t, err)
	// WriteFile error (target is a directory).
	err = WriteImportBlocks(dir, blocks)
	assert.Error(t, err)

	// Create parent dir.
	nested := filepath.Join(dir, "nested", "imports.tf")
	require.NoError(t, WriteImportBlocks(nested, blocks))
	_, err = os.Stat(nested)
	assert.NoError(t, err)

	// Read error (existing unreadable path is a directory).
	badDir := t.TempDir()
	err = WriteImportBlocks(badDir, blocks)
	assert.Error(t, err)
}

func TestWriteImportBlocksWriteError(t *testing.T) {
	dir := t.TempDir()
	blocks := []ImportBlock{{To: "aws_s3_bucket.b", ID: "b-id"}}
	roPath := filepath.Join(dir, "readonly.tf")
	require.NoError(t, os.WriteFile(roPath, []byte("import { to = aws_s3_bucket.a id = \"a-id\" }\n"), 0o444))
	err := WriteImportBlocks(roPath, blocks)
	assert.Error(t, err)
	_ = os.Chmod(roPath, 0o600)
}

func TestResourceTransforms(t *testing.T) {
	transforms := []ResourceTransform{
		{From: "aws_instance.old", To: []string{"aws_instance.new"}},
		{From: "aws_instance.mono", To: []string{"aws_instance.a", "aws_instance.b"}},
		{From: "", To: []string{"x"}},
		{From: "y", To: []string{""}},
	}
	blocks := SynthesizeTransformMovedBlocks(transforms)
	assert.Len(t, blocks, 3)

	// MovedBlocks method.
	assert.Len(t, ResourceTransform{From: "a", To: []string{"b", "c"}}.MovedBlocks(), 2)
	assert.Empty(t, ResourceTransform{From: "", To: []string{"b"}}.MovedBlocks())

	// Load from disk.
	dir := t.TempDir()
	path := filepath.Join(dir, "transforms.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"transforms":[{"from":"a","to":["b"]}]}`), 0o600))
	loaded, err := LoadResourceTransforms(path)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t, "a", loaded[0].From)

	// Missing file.
	_, err = LoadResourceTransforms(filepath.Join(dir, "missing.json"))
	assert.Error(t, err)

	// Bad JSON.
	bad := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte("{"), 0o600))
	_, err = LoadResourceTransforms(bad)
	assert.Error(t, err)
}

func TestLoadResourceTransformsValidation(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"empty.json":    `{}`,
		"no-from.json":  `{"transforms":[{"from":"","to":["b"]}]}`,
		"no-to.json":    `{"transforms":[{"from":"a","to":[]}]}`,
		"empty-to.json": `{"transforms":[{"from":"a","to":[""]}]}`,
		"split.json":    `{"transforms":[{"from":"a","to":["b","c"]}]}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name)
			require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
			_, err := LoadResourceTransforms(path)
			assert.Error(t, err)
		})
	}
}
