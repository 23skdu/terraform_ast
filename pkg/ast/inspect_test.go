package ast

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInspect_InvalidPaths(t *testing.T) {
	_, err := Inspect("/non/existent/path/for/ast/test")
	assert.Error(t, err)

	tmpFile := filepath.Join(t.TempDir(), "file.txt")
	err = os.WriteFile(tmpFile, []byte("hello"), 0644)
	require.NoError(t, err)

	_, err = Inspect(tmpFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is not a directory")
}

func TestInspect_DiagnosticsError(t *testing.T) {
	dir := t.TempDir()
	badHCL := `
variable "foo" {
  type = "unterminated string
`
	err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(badHCL), 0644)
	require.NoError(t, err)

	_, err = Inspect(dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "diagnostics error loading module")
}

func TestInspect_ValidModule(t *testing.T) {
	dir := t.TempDir()

	mainTF := `
terraform {
  required_version = ">= 1.5.0"
  required_providers {
    google = {
      source                = "hashicorp/google"
      version               = ">= 5.0"
      configuration_aliases = [google.west]
    }
  }
}

module "gke" {
  source  = "terraform-google-modules/kubernetes-engine/google"
  version = "~> 29.0"

  project_id = var.project_id
  name       = var.cluster_name
  regional   = true
}

output "cluster_endpoint" {
  description = "Cluster endpoint"
  value       = module.gke.endpoint
}
`

	variablesTF := `
variable "project_id" {
  type        = string
  description = "GCP Project ID"
}

variable "cluster_name" {
  type        = string
  description = "Name of the GKE cluster"
  default     = "production-cluster"
  validation {
    condition     = length(var.cluster_name) > 0
    error_message = "Cluster name must not be empty."
  }
}

variable "untyped_var" {
  description = "No type defined"
  default     = null
}

variable "sensitive_token" {
  type      = string
  sensitive = true
  default   = "secret"
}
`

	outputsTF := `
output "custom_output" {
  description = "Custom static output"
  value       = "custom-val"
  sensitive   = false
}
`
	// Also add non-tf file and unparseable tf file to test enrichment edge cases
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(mainTF), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "variables.tf"), []byte(variablesTF), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "outputs.tf"), []byte(outputsTF), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("random text"), 0644))

	info, err := Inspect(dir)
	require.NoError(t, err)
	require.NotNil(t, info)

	// Check core and providers
	assert.Contains(t, info.RequiredCore, ">= 1.5.0")
	require.Contains(t, info.RequiredProviders, "google")
	assert.Equal(t, "hashicorp/google", info.RequiredProviders["google"].Source)
	assert.Equal(t, []string{"google.west"}, info.RequiredProviders["google"].ConfigurationAliases)

	// Check variables
	require.Contains(t, info.Variables, "project_id")
	assert.True(t, info.Variables["project_id"].Required)
	assert.False(t, info.Variables["project_id"].HasDefault)
	assert.Equal(t, "string", info.Variables["project_id"].Type)

	require.Contains(t, info.Variables, "cluster_name")
	assert.False(t, info.Variables["cluster_name"].Required)
	assert.True(t, info.Variables["cluster_name"].HasDefault)
	assert.Equal(t, "production-cluster", info.Variables["cluster_name"].Default)
	assert.Len(t, info.Variables["cluster_name"].ValidationRules, 1)
	assert.Contains(t, info.Variables["cluster_name"].ValidationRules[0], "length(var.cluster_name)")

	require.Contains(t, info.Variables, "untyped_var")
	assert.Equal(t, "any", info.Variables["untyped_var"].Type)

	require.Contains(t, info.Variables, "sensitive_token")
	assert.True(t, info.Variables["sensitive_token"].Sensitive)

	// Check outputs
	require.Contains(t, info.Outputs, "custom_output")
	assert.Equal(t, "Custom static output", info.Outputs["custom_output"].Description)

	// Check module calls
	require.Contains(t, info.ModuleCalls, "gke")
	mc := info.ModuleCalls["gke"]
	assert.Equal(t, "terraform-google-modules/kubernetes-engine/google", mc.Source)
	assert.Equal(t, "~> 29.0", mc.Version)
	assert.Contains(t, mc.PassedInputs, "project_id")
	assert.Contains(t, mc.PassedInputs, "name")
	assert.Contains(t, mc.PassedInputs, "regional")
	assert.Equal(t, "var.project_id", mc.InputExpressions["project_id"])

	// Check output delegations
	require.Contains(t, info.WrapperOutputReferences, "cluster_endpoint")
	require.NotEmpty(t, info.WrapperOutputReferences["cluster_endpoint"])
	ref := info.WrapperOutputReferences["cluster_endpoint"][0]
	assert.Equal(t, "gke", ref.TargetModule)
	assert.Equal(t, "endpoint", ref.TargetOutput)
}

func TestInspect_EdgeCases(t *testing.T) {
	dir := t.TempDir()

	// Subdirectory to test entry.IsDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "submodule"), 0755))

	edgeTF := `
module "standalone" {
  foo = "bar"
}

output "unreferenced" {
  value = "static"
}

output "no_val" {
  description = "No value attribute"
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "edge.tf"), []byte(edgeTF), 0644))

	info, err := Inspect(dir)
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Contains(t, info.ModuleCalls, "standalone")
	assert.Contains(t, info.ModuleCalls["standalone"].PassedInputs, "foo")

	// Directly call enrichFromHCLFiles for unlabelled blocks, invalid syntax, and invalid dirs
	edgeDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(edgeDir, "unlabelled.tf"), []byte("module {}\noutput {}\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(edgeDir, "invalid.tf"), []byte("bad hcl {{{"), 0644))

	unreadableFile := filepath.Join(edgeDir, "unreadable.tf")
	require.NoError(t, os.WriteFile(unreadableFile, []byte("output {}"), 0000))
	defer func() { _ = os.Chmod(unreadableFile, 0644) }()

	info2 := &ModuleInfo{
		ModuleCalls:             make(map[string]ModuleCallDef),
		WrapperOutputReferences: make(map[string][]OutputReference),
	}
	enrichFromHCLFiles(dir, info2)
	enrichFromHCLFiles(edgeDir, info2)
	enrichFromHCLFiles("/non/existent/path/for/read/dir", info2)
}

func TestInspect_NullabilityAndModuleMetaArguments(t *testing.T) {
	dir := t.TempDir()
	source := `
variable "name" {
  type     = string
  nullable = false
}

module "child" {
  source   = "./child"
  for_each = toset(["one"])
  name     = each.value
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(source), 0644))

	info, err := Inspect(dir)
	require.NoError(t, err)
	require.NotNil(t, info.Variables["name"].Nullable)
	assert.False(t, *info.Variables["name"].Nullable)
	require.Contains(t, info.ModuleCalls, "child")
	assert.Equal(t, []string{"name"}, info.ModuleCalls["child"].PassedInputs)
	assert.NotContains(t, info.ModuleCalls["child"].PassedInputs, "for_each")
}

func TestInspect_TerraformJSON(t *testing.T) {
	dir := t.TempDir()
	source := `{
  "variable": {
    "name": {
      "type": "string",
      "nullable": false
    }
  },
  "module": {
    "child": {
      "source": "./child",
      "count": 1,
      "input": "${var.name}"
    }
  },
  "output": {
    "child_id": {
      "value": "${module.child[0].id}"
    },
    "literal": {
      "value": "module.child.not_a_reference"
    }
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf.json"), []byte(source), 0644))

	info, err := Inspect(dir)
	require.NoError(t, err)
	require.Contains(t, info.Variables, "name")
	require.NotNil(t, info.Variables["name"].Nullable)
	assert.False(t, *info.Variables["name"].Nullable)
	require.Contains(t, info.ModuleCalls, "child")
	assert.Equal(t, []string{"input"}, info.ModuleCalls["child"].PassedInputs)
	assert.Equal(t, `"${var.name}"`, info.ModuleCalls["child"].InputExpressions["input"])
	require.Contains(t, info.WrapperOutputReferences, "child_id")
	assert.Equal(t, []OutputReference{{
		OutputName:   "child_id",
		TargetModule: "child",
		TargetOutput: "id",
	}}, info.WrapperOutputReferences["child_id"])
	assert.NotContains(t, info.WrapperOutputReferences, "literal")
}

func TestInspect_CacheDefensiveCopyAndInvalidation(t *testing.T) {
	dir := t.TempDir()
	variablesFile := filepath.Join(dir, "variables.tf")
	require.NoError(t, os.WriteFile(variablesFile, []byte(`variable "name" { type = string }`), 0644))

	first, err := Inspect(dir)
	require.NoError(t, err)
	assert.Equal(t, "string", first.Variables["name"].Type)
	first.Variables["name"] = VariableDef{Name: "name", Type: "mutated"}

	second, err := Inspect(dir)
	require.NoError(t, err)
	assert.Equal(t, "string", second.Variables["name"].Type)

	require.NoError(t, os.WriteFile(variablesFile, []byte(`variable "name" { type = number }`), 0644))
	updated, err := Inspect(dir)
	require.NoError(t, err)
	assert.Equal(t, "number", updated.Variables["name"].Type)
}

func TestRecoverParseErrorConvertsPanics(t *testing.T) {
	var err error
	func() {
		defer recoverParseError("modules/gke", &err)
		panic("boom")
	}()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panic during AST parsing of module modules/gke")
	assert.Contains(t, err.Error(), "boom")

	// Without a panic the named error is left untouched.
	err = nil
	func() { defer recoverParseError("modules/gke", &err) }()
	assert.NoError(t, err)
}

func TestModuleFingerprintErrors(t *testing.T) {
	_, err := moduleFingerprint("/non/existent/module/dir")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fingerprint module directory")

	dir := t.TempDir()
	unreadable := filepath.Join(dir, "unreadable.tf")
	require.NoError(t, os.WriteFile(unreadable, []byte(`variable "a" {}`), 0000))
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0644) })

	if _, err := moduleFingerprint(dir); err == nil {
		t.Skip("filesystem allows reading mode-000 files; skipping the unreadable-file branch")
	} else {
		assert.Contains(t, err.Error(), "failed to fingerprint module file")
	}

	// Inspect surfaces the same failure instead of returning a partial module.
	info, err := Inspect(dir)
	assert.Nil(t, info)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fingerprint module file")
}

func TestCachedModuleDecodesCorruptEntry(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`variable "a" { type = string }`), 0644))

	_, err := Inspect(dir)
	require.NoError(t, err)

	key := filepath.Clean(dir)

	// Corrupt the serialized payload to exercise the cache decode failure path.
	moduleCache.Lock()
	entry := moduleCache.entries[key]
	entry.data = []byte("{not json")
	moduleCache.entries[key] = entry
	moduleCache.Unlock()

	decoded, found, err := cachedModule(key, entry.fingerprint)
	assert.Nil(t, decoded)
	assert.False(t, found)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode cached module inspection")

	// Inspect falls back to a fresh parse and transparently discards the corrupt entry.
	recovered, err := Inspect(dir)
	require.NoError(t, err)
	require.NotNil(t, recovered)
	assert.Equal(t, "string", recovered.Variables["a"].Type)

	moduleCache.Lock()
	entry, reCached := moduleCache.entries[key]
	moduleCache.Unlock()
	require.True(t, reCached, "the module must be cached again after re-inspection")
	assert.NotEqual(t, "{not json", string(entry.data), "the corrupt payload must have been replaced")
}

func TestCacheModuleRejectsUnserializableInfo(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`variable "a" { type = string }`), 0644))
	key := filepath.Clean(dir)

	// A default value that JSON cannot encode must surface as an error rather than be cached.
	unserializable := &ModuleInfo{
		Variables: map[string]VariableDef{
			"a": {Name: "a", Type: "string", Default: func() {}},
		},
	}
	err := cacheModule(key, "fingerprint", unserializable)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to cache module inspection")

	moduleCache.Lock()
	_, leaked := moduleCache.entries[key]
	moduleCache.Unlock()
	assert.False(t, leaked, "a failed cache write must not leave a stale entry")
}

func TestModuleCacheEvictsOldestEntryBeyondCapacity(t *testing.T) {
	isolateModuleCache(t)

	for i := 0; i < moduleCacheCapacity+3; i++ {
		path := "module-cache-eviction-key"
		require.NoError(t, cacheModule(path+itoa(i), "fingerprint", &ModuleInfo{Path: path + itoa(i)}))
	}

	moduleCache.Lock()
	assert.Len(t, moduleCache.entries, moduleCacheCapacity)
	assert.Equal(t, moduleCacheCapacity, len(moduleCache.order))
	oldest := moduleCache.order[0]
	_, evicted := moduleCache.entries[oldest]
	assert.True(t, evicted, "the oldest key is also the newest entry by overwrite order")
	moduleCache.Unlock()
}

func TestEnrichFromHCLFilesOutputTraversalShapes(t *testing.T) {
	cases := []struct {
		name     string
		source   string
		expected map[string]string
	}{
		{
			name:     "an index step is skipped and the next attribute wins",
			source:   `output "o" { value = module.gke[0].outputs }`,
			expected: map[string]string{"o": "outputs"},
		},
		{
			name:   "indexed module reference is not a delegation",
			source: `output "o" { value = module[0].outputs }`,
		},
		{
			name:   "a non-module root is not a delegation",
			source: `output "o" { value = data.aws_thing.name }`,
		},
		{
			name:     "nested attribute access resolves to the first delegated output",
			source:   `output "o" { value = module.gke.outputs.name }`,
			expected: map[string]string{"o": "outputs"},
		},
		{
			name:     "indexing the delegated output still resolves the attribute",
			source:   `output "o" { value = module.gke.outputs[0] }`,
			expected: map[string]string{"o": "outputs"},
		},
		{
			name:   "indexed module reference is not a delegation",
			source: `output "o" { value = module[0].outputs }`,
		},
		{
			name:   "non module root is ignored",
			source: `output "o" { value = local.gke.outputs }`,
		},
		{
			name:   "single step traversal is ignored",
			source: `output "o" { value = local }`,
		},
		{
			name:   "a bare module reference has no delegated output",
			source: `output "o" { value = module.gke }`,
		},
		{
			name:     "plain module delegation",
			source:   `output "o" { value = module.gke.endpoint }`,
			expected: map[string]string{"o": "endpoint"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "outputs.tf"), []byte(tc.source), 0644))

			info := &ModuleInfo{WrapperOutputReferences: make(map[string][]OutputReference)}
			enrichFromHCLFiles(dir, info)
			assert.Len(t, info.WrapperOutputReferences, len(tc.expected))
			for output, target := range tc.expected {
				refs, ok := info.WrapperOutputReferences[output]
				require.True(t, ok)
				require.NotEmpty(t, refs)
				assert.Equal(t, target, refs[0].TargetOutput)
				assert.Equal(t, "gke", refs[0].TargetModule)
			}
		})
	}
}

func TestEnrichFromHCLFilesSkipsUnsupportedFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("variable \"a\" { nullable = false }"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "variables.tf.json"), []byte(`{"variable":{"a":{"type":"string","nullable":false}}}`), 0644))

	info := &ModuleInfo{
		Variables:               map[string]VariableDef{"a": {Name: "a", Type: "string"}},
		WrapperOutputReferences: make(map[string][]OutputReference),
	}
	enrichFromHCLFiles(dir, info)
	require.NotNil(t, info.Variables["a"].Nullable)
	assert.False(t, *info.Variables["a"].Nullable)
}

func TestEnrichFromHCLFilesValidationRuleWithoutMessage(t *testing.T) {
	dir := t.TempDir()
	source := `
variable "name" {
  type = string
  validation {
    condition     = length(var.name) > 0
    error_message = "must not be empty"
  }
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "variables.tf"), []byte(source), 0644))

	info, err := Inspect(dir)
	require.NoError(t, err)
	require.Len(t, info.Variables["name"].ValidationRules, 1)
	assert.Contains(t, info.Variables["name"].ValidationRules[0], "must not be empty")
}

func TestInspect_SkipsNonNullableExpressions(t *testing.T) {
	dir := t.TempDir()
	source := `
variable "dynamic_nullable" {
  type     = string
  nullable = var.enable_nullable
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(source), 0644))

	info, err := Inspect(dir)
	require.NoError(t, err)
	assert.Nil(t, info.Variables["dynamic_nullable"].Nullable)
}

func TestInspect_ModernBlocks(t *testing.T) {
	isolateModuleCache(t)
	dir := t.TempDir()
	source := `
removed {
  from = google_container_cluster.legacy
  lifecycle {
    destroy = false
  }
}

removed {
  from = aws_s3_bucket.old
}

check "health_check" {
  assert {
    condition = true
  }
}

import {
  to = aws_s3_bucket.primary
  id = "prod-bucket-123"
}

function "format_name" {
  summary = "Formats a resource name"
  return  = "string"
}

encryption {
  key_provider "pbkdf2" "passphrase" {
    passphrase = "test"
  }
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "modern.tf"), []byte(source), 0644))

	info, err := Inspect(dir)
	require.NoError(t, err)

	require.Len(t, info.RemovedBlocks, 2)
	assert.Equal(t, "google_container_cluster.legacy", info.RemovedBlocks[0].From)
	assert.False(t, info.RemovedBlocks[0].Destroy)
	assert.Equal(t, "aws_s3_bucket.old", info.RemovedBlocks[1].From)
	assert.True(t, info.RemovedBlocks[1].Destroy)

	require.Contains(t, info.CheckBlocks, "health_check")
	assert.Equal(t, []string{"true"}, info.CheckBlocks["health_check"].AssertRules)

	require.Len(t, info.ImportBlocks, 1)
	assert.Equal(t, "aws_s3_bucket.primary", info.ImportBlocks[0].To)
	assert.Equal(t, "prod-bucket-123", info.ImportBlocks[0].ID)

	require.Contains(t, info.Functions, "format_name")
	assert.Equal(t, "Formats a resource name", info.Functions["format_name"].Summary)
	assert.Equal(t, "string", info.Functions["format_name"].ReturnType)

	require.Len(t, info.EncryptionBlocks, 1)
	assert.Equal(t, "pbkdf2", info.EncryptionBlocks[0].KeyProvider)
}

func itoa(i int) string {
	return strconv.Itoa(i)
}

func resetModuleCache() {
	moduleCache.Lock()
	defer moduleCache.Unlock()
	moduleCache.entries = make(map[string]moduleCacheEntry)
	moduleCache.order = nil
}

// isolateModuleCache clears the package level inspection cache and restores it when the test ends.
func isolateModuleCache(t *testing.T) {
	t.Helper()
	resetModuleCache()
	t.Cleanup(resetModuleCache)
}

// writeModule is a helper that lays out a small multi-block Terraform module on disk.
func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	}
	return dir
}

// TestInspectSource_UsesBufferOverDisk is the contract the language server depends on: the
// in-memory text wins over the file on disk, because an editor holds unsaved changes.
func TestInspectSource_UsesBufferOverDisk(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": "variable \"on_disk\" {\n  type = string\n}\n",
	})
	isolateModuleCache(t)

	file := filepath.Join(dir, "main.tf")
	info, err := InspectSource(dir, file, "variable \"in_buffer\" {\n  type = string\n}\n")
	require.NoError(t, err)

	require.Contains(t, info.Variables, "in_buffer", "the buffer content must be analysed")
	assert.NotContains(t, info.Variables, "on_disk", "the disk content must be superseded")
}

// TestInspectSource_ReportsRealPaths covers the path rebasing: positions come from a temporary
// staging copy, so they must be rewritten to the caller's real files or an editor cannot map a
// diagnostic back to a line.
func TestInspectSource_ReportsRealPaths(t *testing.T) {
	dir := writeModule(t, map[string]string{
		// A subdirectory proves subtrees are never staged: only top-level .tf files are
		// module sources, so this file must not reach the analysis.
		"modules/deep/main.tf": "variable \"nested\" {\n  type = string\n}\n",
		"main.tf":              "variable \"disk_only\" {\n  type = string\n}\n",
		// A sibling block type the rebase loop has to cover.
		"checks.tf": "check \"health\" {\n  assert {\n    condition     = true\n    error_message = \"x\"\n  }\n}\n",
		// The remaining position-bearing block kinds, so every rebase branch is exercised.
		"imports.tf": "import {\n  to = aws_s3_bucket.primary\n  id = \"prod-bucket\"\n}\n",
		"sealed.tf":  "encryption {\n  key_provider \"pbkdf2\" \"passphrase\" {\n    passphrase = \"test\"\n  }\n}\n",
		"removed.tf": "removed {\n  from = a.b\n  lifecycle {\n    destroy = false\n  }\n}\n",
		// A custom function block, matching the shape the parser already accepts.
		"fn.tf": "function \"slug\" {\n  summary = \"Formats a name\"\n  return  = \"string\"\n}\n",
	})
	isolateModuleCache(t)

	file := filepath.Join(dir, "main.tf")
	info, err := InspectSource(dir, file, "variable \"buffered\" {\n  type = string\n}\n")
	require.NoError(t, err)

	assert.Equal(t, dir, info.Path, "Path must describe the real module, not the staging copy")

	// Every recorded position must live inside the real directory and never in a temp dir.
	assertFilename := func(what, filename string) {
		t.Helper()
		assert.True(t, filepath.IsAbs(filename) || filepath.Dir(filename) == dir,
			"%s filename %q must be under %q", what, filename, dir)
		assert.NotContains(t, filename, "tfastdiff_overlay",
			"%s filename %q leaked the staging directory", what, filename)
	}

	for name, v := range info.Variables {
		assertFilename("variable "+name, v.Pos.Filename)
		assert.Equal(t, dir, filepath.Dir(v.Pos.Filename))
	}
	for name, cb := range info.CheckBlocks {
		assertFilename("check "+name, cb.Pos.Filename)
		assert.Equal(t, dir, filepath.Dir(cb.Pos.Filename))
	}
	for name, fn := range info.Functions {
		assertFilename("function "+name, fn.Pos.Filename)
		assert.Equal(t, dir, filepath.Dir(fn.Pos.Filename))
	}
	for _, rb := range info.RemovedBlocks {
		assertFilename("removed", rb.Pos.Filename)
		assert.Equal(t, dir, filepath.Dir(rb.Pos.Filename))
	}
	for _, ib := range info.ImportBlocks {
		assertFilename("import", ib.Pos.Filename)
		assert.Equal(t, dir, filepath.Dir(ib.Pos.Filename))
	}
	for _, eb := range info.EncryptionBlocks {
		assertFilename("encryption", eb.Pos.Filename)
		assert.Equal(t, dir, filepath.Dir(eb.Pos.Filename))
	}
}

// TestInspectSource_KeepsSiblingFiles confirms a buffer replaces only its own file: the module
// still has to be understood as a whole.
func TestInspectSource_KeepsSiblingFiles(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf":    "variable \"on_disk\" {\n  type = string\n}\n",
		"outputs.tf": "output \"kept\" {\n  value = \"x\"\n}\n",
		"calls.tf":   "module \"up\" {\n  source = \"./upstream\"\n}\n",
	})
	isolateModuleCache(t)

	file := filepath.Join(dir, "main.tf")
	info, err := InspectSource(dir, file, "variable \"added\" {\n  type = string\n}\n")
	require.NoError(t, err)

	assert.Contains(t, info.Variables, "added")
	assert.Contains(t, info.Outputs, "kept", "a sibling file must still be inspected")
	assert.Contains(t, info.ModuleCalls, "up", "a sibling file must still be inspected")
}

// TestInspectSource_HandlesJSONAndNonTerraformFiles covers the staging copy's file selection.
func TestInspectSource_HandlesJSONAndNonTerraformFiles(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"module.tf.json": `{"variable": {"from_json": {"type": "string"}}}`,
		"README.md":      "not terraform",
		"notes.txt":      "ignore me",
	})
	isolateModuleCache(t)

	file := filepath.Join(dir, "module.tf.json")
	info, err := InspectSource(dir, file, `{"variable": {"edited": {"type": "string"}}}`)
	require.NoError(t, err)
	assert.Contains(t, info.Variables, "edited")
	assert.NotContains(t, info.Variables, "from_json")
}

// TestInspectSource_MissingDirectoryAndUnreadableSiblings covers the error and skip paths in the
// staging copy.
func TestInspectSource_MissingDirectoryAndUnreadableSiblings(t *testing.T) {
	isolateModuleCache(t)

	// A missing directory is reported, not silently ignored.
	_, err := InspectSource("/non/existent/overlay/dir", "/non/existent/overlay/dir/main.tf", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to access module path")

	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}

	// A module that can be stat'ed but not read must fail the whole inspection. Falling back to
	// an empty staging copy would report every variable as removed.
	unreadable := t.TempDir()
	require.NoError(t, os.Chmod(unreadable, 0o000))
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o700) })

	_, err = InspectSource(unreadable, filepath.Join(unreadable, "main.tf"), "variable \"a\" {\n  type = string\n}\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read module directory")
}

// TestInspectSource_SkipsUnreadableSibling covers an unreadable file that the overlay does not
// replace: it is skipped so one unreadable file cannot break diagnostics for the whole module,
// while the files that are readable are still analysed.
func TestInspectSource_SkipsUnreadableSibling(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	dir := writeModule(t, map[string]string{
		"main.tf":   "variable \"a\" {\n  type = string\n}\n",
		"secret.tf": "variable \"secret\" {\n  type = string\n}\n",
		"module.tf": "variable \"b\" {\n  type = string\n}\n",
	})
	isolateModuleCache(t)

	require.NoError(t, os.Chmod(filepath.Join(dir, "secret.tf"), 0o000))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "secret.tf"), 0o600) })

	file := filepath.Join(dir, "module.tf")
	info, err := InspectSource(dir, file, "variable \"edited\" {\n  type = string\n}\n")
	require.NoError(t, err)
	assert.Contains(t, info.Variables, "edited")
	assert.Contains(t, info.Variables, "a", "readable siblings are still analysed")
	assert.NotContains(t, info.Variables, "secret")
}

// TestInspectSource_StagesUnreadableOverlaidFile covers the path where a file cannot be read from
// disk but the overlay supplies its content: the caller's text is authoritative.
func TestInspectSource_StagesUnreadableOverlaidFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	dir := writeModule(t, map[string]string{"main.tf": "variable \"a\" {\n  type = string\n}\n"})
	isolateModuleCache(t)

	blocked := filepath.Join(dir, "blocked.tf")
	require.NoError(t, os.WriteFile(blocked, []byte("variable \"old\" {\n  type = string\n}\n"), 0600))
	require.NoError(t, os.Chmod(blocked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o600) })

	info, err := InspectSource(dir, blocked, "variable \"from_overlay\" {\n  type = string\n}\n")
	require.NoError(t, err)
	assert.Contains(t, info.Variables, "from_overlay")
	assert.NotContains(t, info.Variables, "old")
}

// TestInspectSource_RejectsUnparsableBuffer confirms a broken buffer is an error, which is what
// lets the language server fall back to the last good state instead of showing an error.
func TestInspectSource_RejectsUnparsableBuffer(t *testing.T) {
	dir := writeModule(t, map[string]string{"main.tf": "variable \"a\" {\n  type = string\n}\n"})
	isolateModuleCache(t)

	file := filepath.Join(dir, "main.tf")
	_, err := InspectSource(dir, file, "variable \"a\" {\n  type = \n")
	assert.Error(t, err)
}

// TestInspectSource_StagingFailureIsReported covers an unwritable staging destination.
func TestInspectSource_StagingFailureIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	dir := writeModule(t, map[string]string{"main.tf": "variable \"a\" {\n  type = string\n}\n"})
	isolateModuleCache(t)

	// Point TMPDIR at a read-only directory so MkdirTemp fails.
	blocked := t.TempDir()
	require.NoError(t, os.Chmod(blocked, 0o500))
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o700) })
	t.Setenv("TMPDIR", blocked)

	_, err := InspectSource(dir, filepath.Join(dir, "main.tf"), "variable \"b\" {\n  type = string\n}\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to stage module overlay")
}

// TestInspectSource_StagingWriteFailureIsReported covers a staging directory that disappears
// between creation and use, which makes the per-file write fail.
func TestInspectSource_StagingWriteFailureIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	isolateModuleCache(t)

	dir := writeModule(t, map[string]string{
		"main.tf":   "variable \"a\" {\n  type = string\n}\n",
		"second.tf": "variable \"b\" {\n  type = string\n}\n",
	})

	// A staging root that is read-only after creation: MkdirTemp under it succeeds only if the
	// parent allows it, so instead make the staged copy unwritable by removing write permission
	// on the directory the temp file lands in.
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	require.NoError(t, os.Chmod(root, 0o500))
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

	_, err := InspectSource(dir, filepath.Join(dir, "main.tf"), "variable \"c\" {\n  type = string\n}\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to stage module overlay")
}

// TestRebasePath covers the path rewrite directly, including the cases that must be left alone.
func TestRebasePath(t *testing.T) {
	from, to := "/tmp/staging", "/real/module"

	assert.Equal(t, "/real/module/main.tf", rebasePath("/tmp/staging/main.tf", from, to))
	assert.Equal(t, "/real/module/nested/main.tf", rebasePath("/tmp/staging/nested/main.tf", from, to))

	// An empty filename has nothing to rebase.
	assert.Equal(t, "", rebasePath("", from, to))

	// A path outside the staging directory is left untouched rather than being forced in.
	assert.Equal(t, "/elsewhere/main.tf", rebasePath("/elsewhere/main.tf", from, to))

	// A relative "up and out" path is also left alone.
	assert.Equal(t, "../sibling/main.tf", rebasePath("../sibling/main.tf", from, to))
}

// TestInspectSource_IgnoresNestedSubtree confirms the staging copy only takes top-level module
// files. A nested module directory must not leak into the analysis of its parent.
func TestInspectSource_IgnoresNestedSubtree(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf":                 "variable \"top\" {\n  type = string\n}\n",
		"modules/child/main.tf":   "variable \"child\" {\n  type = string\n}\n",
		"modules/child/child2.tf": "variable \"child2\" {\n  type = string\n}\n",
	})
	isolateModuleCache(t)

	file := filepath.Join(dir, "main.tf")
	info, err := InspectSource(dir, file, "variable \"buffered\" {\n  type = string\n}\n")
	require.NoError(t, err)

	assert.Contains(t, info.Variables, "buffered")
	assert.NotContains(t, info.Variables, "child", "a nested module is not part of its parent")
	assert.NotContains(t, info.Variables, "child2")
}

// TestStageModuleFiles_ReadFailureIsReported covers the staging copy failing to enumerate the
// module directory. Reported rather than ignored, since an unreadable module cannot be analysed.
func TestStageModuleFiles_ReadFailureIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := stageModuleFiles(dir, t.TempDir(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read module directory")
}

// TestStageModuleFiles_WriteFailureIsReported covers a staging destination that rejects writes.
// The file must not be skipped silently: a missing declaration would be analysed as if the module
// never declared it.
func TestStageModuleFiles_WriteFailureIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	source := writeModule(t, map[string]string{
		"main.tf": "variable \"a\" {\n  type = string\n}\n",
	})

	staging := t.TempDir()
	require.NoError(t, os.Chmod(staging, 0o500))
	t.Cleanup(func() { _ = os.Chmod(staging, 0o700) })

	err := stageModuleFiles(source, staging, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to stage main.tf")
}

func TestInspect_ResourcesAndRebase(t *testing.T) {
	dir := t.TempDir()
	content := `
provider "aws" {
  alias = "west"
}
resource "aws_vpc" "main" {
  provider = aws.west
  cidr_block = "10.0.0.0/16"
}
resource "aws_subnet" "sub" {
  vpc_id = aws_vpc.main.id
}
data "aws_ami" "ubuntu" {
  provider = aws.west
  most_recent = true
}
data "aws_region" "current" {}
`
	mainPath := filepath.Join(dir, "main.tf")
	require.NoError(t, os.WriteFile(mainPath, []byte(content), 0644))

	info, err := Inspect(dir)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Len(t, info.ManagedResources, 2)
	assert.Len(t, info.DataResources, 2)
	assert.Equal(t, "aws.west", info.ManagedResources["aws_vpc.main"].Provider)
	assert.Equal(t, "aws", info.ManagedResources["aws_subnet.sub"].Provider)
	assert.Equal(t, "aws.west", info.DataResources["data.aws_ami.ubuntu"].Provider)
	assert.Equal(t, "aws", info.DataResources["data.aws_region.current"].Provider)

	// Test InspectSource overlay to trigger rebaseModulePaths on resources
	overlayInfo, err := InspectSource(dir, mainPath, content)
	require.NoError(t, err)
	require.NotNil(t, overlayInfo)
	assert.Equal(t, mainPath, overlayInfo.ManagedResources["aws_vpc.main"].Pos.Filename)
	assert.Equal(t, mainPath, overlayInfo.DataResources["data.aws_ami.ubuntu"].Pos.Filename)
}
