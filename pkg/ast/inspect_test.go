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
	ref := info.WrapperOutputReferences["cluster_endpoint"]
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
		WrapperOutputReferences: make(map[string]OutputReference),
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
	assert.Equal(t, OutputReference{
		OutputName:   "child_id",
		TargetModule: "child",
		TargetOutput: "id",
	}, info.WrapperOutputReferences["child_id"])
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

			info := &ModuleInfo{WrapperOutputReferences: make(map[string]OutputReference)}
			enrichFromHCLFiles(dir, info)
			assert.Len(t, info.WrapperOutputReferences, len(tc.expected))
			for output, target := range tc.expected {
				ref, ok := info.WrapperOutputReferences[output]
				require.True(t, ok)
				assert.Equal(t, target, ref.TargetOutput)
				assert.Equal(t, "gke", ref.TargetModule)
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
		WrapperOutputReferences: make(map[string]OutputReference),
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
