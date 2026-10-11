package ast

import (
	"os"
	"path/filepath"
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
	defer os.Chmod(unreadableFile, 0644)

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
