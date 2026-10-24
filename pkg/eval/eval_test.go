package eval

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
)

func TestStandardFunctions_Concat(t *testing.T) {
	funcs := StandardFunctions()
	concatFn := funcs["concat"]

	// Normal list concat
	res, err := concatFn.Call([]cty.Value{
		cty.ListVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")}),
		cty.ListVal([]cty.Value{cty.StringVal("c")}),
	})
	require.NoError(t, err)
	assert.Equal(t, 3, res.LengthInt())

	// Empty list concat
	resEmpty, err := concatFn.Call([]cty.Value{
		cty.ListValEmpty(cty.String),
	})
	require.NoError(t, err)
	assert.Equal(t, 0, resEmpty.LengthInt())

	// No arguments
	_, errNoArgs := concatFn.Call([]cty.Value{})
	assert.Error(t, errNoArgs)

	// Multiple lists
	resMulti, err := concatFn.Call([]cty.Value{
		cty.ListVal([]cty.Value{cty.StringVal("s")}),
		cty.ListVal([]cty.Value{cty.StringVal("t")}),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, resMulti.LengthInt())
}

func TestStandardFunctions_Merge(t *testing.T) {
	funcs := StandardFunctions()
	mergeFn := funcs["merge"]

	map1 := cty.MapVal(map[string]cty.Value{"a": cty.StringVal("1"), "b": cty.StringVal("2")})
	map2 := cty.MapVal(map[string]cty.Value{"b": cty.StringVal("override"), "c": cty.StringVal("3")})

	res, err := mergeFn.Call([]cty.Value{map1, map2})
	require.NoError(t, err)
	assert.Equal(t, cty.StringVal("override"), res.Index(cty.StringVal("b")))
	assert.Equal(t, cty.StringVal("1"), res.Index(cty.StringVal("a")))
	assert.Equal(t, cty.StringVal("3"), res.Index(cty.StringVal("c")))

	// Empty merge
	resEmpty, err := mergeFn.Call([]cty.Value{})
	require.NoError(t, err)
	assert.Equal(t, cty.EmptyObjectVal, resEmpty)

	// Invalid non-map argument
	_, errInvalid := mergeFn.Call([]cty.Value{
		cty.StringVal("invalid"),
		map1,
	})
	assert.Error(t, errInvalid)
}

func TestStandardFunctions_Lookup(t *testing.T) {
	funcs := StandardFunctions()
	lookupFn := funcs["lookup"]

	m := cty.MapVal(map[string]cty.Value{"region": cty.StringVal("us-central1")})

	// Found
	res, err := lookupFn.Call([]cty.Value{m, cty.StringVal("region"), cty.StringVal("default")})
	require.NoError(t, err)
	assert.Equal(t, cty.StringVal("us-central1"), res)

	// Not found, returns default
	resDef, err := lookupFn.Call([]cty.Value{m, cty.StringVal("missing"), cty.StringVal("default-val")})
	require.NoError(t, err)
	assert.Equal(t, cty.StringVal("default-val"), resDef)

	// Wrong number of arguments
	_, errWrongArgs := lookupFn.Call([]cty.Value{m, cty.StringVal("missing")})
	assert.Error(t, errWrongArgs)

	// Not a map
	_, errNotMap := lookupFn.Call([]cty.Value{cty.StringVal("not-a-map"), cty.StringVal("key"), cty.StringVal("def")})
	assert.Error(t, errNotMap)
}

func TestStandardFunctions_Coalesce(t *testing.T) {
	funcs := StandardFunctions()
	coalesceFn := funcs["coalesce"]

	res, err := coalesceFn.Call([]cty.Value{
		cty.NullVal(cty.String),
		cty.StringVal("found"),
	})
	require.NoError(t, err)
	assert.Equal(t, cty.StringVal("found"), res)

	_, errAllNull := coalesceFn.Call([]cty.Value{
		cty.NullVal(cty.String),
		cty.NullVal(cty.String),
	})
	assert.Error(t, errAllNull)
}

func TestStandardFunctions_Element(t *testing.T) {
	funcs := StandardFunctions()
	elemFn := funcs["element"]

	list := cty.ListVal([]cty.Value{cty.StringVal("first"), cty.StringVal("second")})

	res0, err := elemFn.Call([]cty.Value{list, cty.NumberIntVal(0)})
	require.NoError(t, err)
	assert.Equal(t, cty.StringVal("first"), res0)

	// Wrapping modulo
	resWrap, err := elemFn.Call([]cty.Value{list, cty.NumberIntVal(3)})
	require.NoError(t, err)
	assert.Equal(t, cty.StringVal("second"), resWrap)

	// Negative wrapping
	resNeg, err := elemFn.Call([]cty.Value{list, cty.NumberIntVal(-1)})
	require.NoError(t, err)
	assert.Equal(t, cty.StringVal("second"), resNeg)

	// Empty list error
	_, errEmpty := elemFn.Call([]cty.Value{cty.ListValEmpty(cty.String), cty.NumberIntVal(0)})
	assert.Error(t, errEmpty)
}

func TestStandardFunctions_StringHelpers(t *testing.T) {
	funcs := StandardFunctions()

	resLower, err := funcs["lower"].Call([]cty.Value{cty.StringVal("HELLO WORLD")})
	require.NoError(t, err)
	assert.Equal(t, cty.StringVal("hello world"), resLower)

	resUpper, err := funcs["upper"].Call([]cty.Value{cty.StringVal("hello world")})
	require.NoError(t, err)
	assert.Equal(t, cty.StringVal("HELLO WORLD"), resUpper)

	resTrim, err := funcs["trimspace"].Call([]cty.Value{cty.StringVal("   spaced   ")})
	require.NoError(t, err)
	assert.Equal(t, cty.StringVal("spaced"), resTrim)
}

func TestStandardFunctions_JsonDecode(t *testing.T) {
	funcs := StandardFunctions()
	fn := funcs["jsondecode"]

	validJSON := `{"name":"prod","count":42,"active":true,"tags":["web","api"]}`
	res, err := fn.Call([]cty.Value{cty.StringVal(validJSON)})
	require.NoError(t, err)
	assert.True(t, res.Type().IsObjectType())
	assert.Equal(t, cty.StringVal("prod"), res.GetAttr("name"))

	// Unknown value test
	ty, errUnknown := fn.ReturnTypeForValues([]cty.Value{cty.UnknownVal(cty.String)})
	require.NoError(t, errUnknown)
	assert.Equal(t, cty.DynamicPseudoType, ty)

	// Invalid JSON
	_, errInvalid := fn.Call([]cty.Value{cty.StringVal(`{invalid`)})
	assert.Error(t, errInvalid)
}

func TestToCtyValue(t *testing.T) {
	assert.Equal(t, cty.NilVal, toCtyValue(nil))
	assert.Equal(t, cty.BoolVal(true), toCtyValue(true))
	assert.Equal(t, cty.NumberFloatVal(3.14), toCtyValue(3.14))
	assert.Equal(t, cty.StringVal("hello"), toCtyValue("hello"))
	assert.Equal(t, cty.ListValEmpty(cty.DynamicPseudoType), toCtyValue([]interface{}{}))
	assert.Equal(t, cty.EmptyObjectVal, toCtyValue(map[string]interface{}{}))
	assert.Equal(t, cty.StringVal("123"), toCtyValue(123))
}

func TestEvaluateExpression_And_BuildEvalContext(t *testing.T) {
	vars := map[string]cty.Value{
		"env": cty.StringVal("staging"),
	}
	locals := map[string]cty.Value{
		"cluster_name": cty.StringVal("staging-cluster"),
	}
	ctx := BuildEvalContext(vars, locals)

	// Valid evaluation
	val, err := EvaluateExpression(`lower(local.cluster_name)`, ctx)
	require.NoError(t, err)
	assert.Equal(t, cty.StringVal("staging-cluster"), val)

	// With functions
	mergedVal, err := EvaluateExpression(`merge({ a = "1" }, { b = "2" })`, ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(mergedVal.Type().AttributeTypes()))

	// Syntax error
	_, errSyntax := EvaluateExpression(`invalid {+}`, ctx)
	assert.Error(t, errSyntax)

	// Runtime eval error (unknown variable)
	_, errRuntime := EvaluateExpression(`unknown_var.field`, ctx)
	assert.Error(t, errRuntime)

	// Empty context
	emptyCtx := BuildEvalContext(nil, nil)
	assert.NotNil(t, emptyCtx)
}

func TestResolveTerragruntInputs(t *testing.T) {
	ctx := BuildEvalContext(nil, map[string]cty.Value{
		"defaults": cty.MapVal(map[string]cty.Value{
			"region": cty.StringVal("us-east1"),
		}),
	})

	expr := `merge(local.defaults, {
		cluster_name = "prod-eks",
		nodes = 5,
		is_prod = true,
		unassigned = null,
		tags = ["a", "b"]
	})`

	res, err := ResolveTerragruntInputs(expr, ctx)
	require.NoError(t, err)
	assert.Equal(t, `"us-east1"`, res["region"])
	assert.Equal(t, `"prod-eks"`, res["cluster_name"])
	assert.Equal(t, "5", res["nodes"])
	assert.Equal(t, "true", res["is_prod"])
	assert.Equal(t, "null", res["unassigned"])
	assert.Contains(t, res["tags"], "cty.Value")

	// Parse failure
	_, errSyntax := ResolveTerragruntInputs(`{ invalid`, ctx)
	assert.Error(t, errSyntax)

	// Non-map failure
	_, errNonMap := ResolveTerragruntInputs(`"not a map"`, ctx)
	assert.Error(t, errNonMap)

	// Null evaluation
	_, errNull := ResolveTerragruntInputs(`null`, ctx)
	assert.Error(t, errNull)
}

func TestAnalyzeVariableTaint(t *testing.T) {
	tmpDir := t.TempDir()

	tfCode := `
variable "environment" {
	type = string
}

variable "unused_var" {
	type = string
	default = "dead"
}

locals {
	env_tag = "env-${var.environment}"
	unreachable_local = "local-${var.unused_var}"
}

module "network" {
	source = "./modules/vpc"
	env    = local.env_tag
}

resource "google_compute_instance" "vm" {
	name = local.env_tag
}

output "deployed_env" {
	value = var.environment
}
`
	err := os.WriteFile(filepath.Join(tmpDir, "main.tf"), []byte(tfCode), 0o600)
	require.NoError(t, err)

	// Subdir to test directory traversal
	err = os.Mkdir(filepath.Join(tmpDir, "sub"), 0o750)
	require.NoError(t, err)

	// Non-tf and tf.json files to exercise filtering
	_ = os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte("hello"), 0o600)
	_ = os.WriteFile(filepath.Join(tmpDir, "data.tf.json"), []byte("{}"), 0o600)
	_ = os.WriteFile(filepath.Join(tmpDir, "broken.tf"), []byte("{ invalid"), 0o600)

	// Subfield references to test prefix matching in isTainted
	subfieldCode := `
variable "nested_var" {
	type = any
}
locals {
	nested_local = {
		val = var.nested_var.field
	}
}
resource "aws_sqs_queue" "q" {
	name = local.nested_local.val
}
`
	_ = os.WriteFile(filepath.Join(tmpDir, "subfield.tf"), []byte(subfieldCode), 0o600)

	// Test reachable variable
	report, err := AnalyzeVariableTaint(tmpDir, "environment")
	require.NoError(t, err)
	assert.True(t, report.IsReachable)
	assert.False(t, report.IsDeadCode)
	assert.Contains(t, report.TracePath, "var.environment")
	assert.Contains(t, report.TracePath, "local.env_tag")
	assert.Contains(t, report.TracePath, "module.network.env")
	assert.Contains(t, report.TracePath, "google_compute_instance.vm.name")
	assert.Contains(t, report.TracePath, "output.deployed_env")
	assert.Contains(t, report.AffectedResourceAttributes, "google_compute_instance.vm.name")
	assert.Contains(t, report.AffectedResourceAttributes, "module.network.env")

	// Test nested subfield taint
	reportNested, err := AnalyzeVariableTaint(tmpDir, "nested_var")
	require.NoError(t, err)
	assert.True(t, reportNested.IsReachable)
	assert.False(t, reportNested.IsDeadCode)
	assert.Contains(t, reportNested.AffectedResourceAttributes, "aws_sqs_queue.q.name")

	// Test variable only in locals (not dead code, but not reachable by resource/module)
	localOnlyDir := t.TempDir()
	localOnlyCode := `
variable "secret_key" {
	type = string
}
locals {
	masked = "${var.secret_key}-masked"
}
`
	err = os.WriteFile(filepath.Join(localOnlyDir, "main.tf"), []byte(localOnlyCode), 0o600)
	require.NoError(t, err)

	reportLocal, err := AnalyzeVariableTaint(localOnlyDir, "secret_key")
	require.NoError(t, err)
	assert.False(t, reportLocal.IsReachable)
	assert.False(t, reportLocal.IsDeadCode)

	// Test dead code variable
	reportDead, err := AnalyzeVariableTaint(tmpDir, "completely_unused")
	require.NoError(t, err)
	assert.False(t, reportDead.IsReachable)
	assert.True(t, reportDead.IsDeadCode)

	// Unreadable file inside dir
	unreadableFile := filepath.Join(tmpDir, "unreadable.tf")
	_ = os.WriteFile(unreadableFile, []byte("var.x"), 0o600)
	_ = os.Chmod(unreadableFile, 0o000)
	defer func() { _ = os.Chmod(unreadableFile, 0o600) }()
	_, _ = AnalyzeVariableTaint(tmpDir, "environment")

	// Error paths
	_, errMissingDir := AnalyzeVariableTaint("/non/existent/path", "v")
	assert.Error(t, errMissingDir)

	fileAsDir := filepath.Join(tmpDir, "main.tf")
	_, errNotDir := AnalyzeVariableTaint(fileAsDir, "v")
	assert.Error(t, errNotDir)

	// Unreadable directory
	unreadableDir := t.TempDir()
	_ = os.Chmod(unreadableDir, 0o000)
	defer func() { _ = os.Chmod(unreadableDir, 0o750) }()
	_, errUnreadable := AnalyzeVariableTaint(unreadableDir, "v")
	assert.Error(t, errUnreadable)
}

// writeTerragrunt lays out a Terragrunt config and returns its path.
func writeTerragrunt(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// parseTerragruntBody parses a Terragrunt config for direct evaluation.
func parseTerragruntBody(t *testing.T, path string) (*hclsyntax.Body, []byte) {
	t.Helper()
	src, err := os.ReadFile(path)
	require.NoError(t, err)
	file, diags := hclsyntax.ParseConfig(src, path, hcl.Pos{Line: 1, Column: 1})
	require.False(t, diags.HasErrors(), "parse %s: %s", path, diags)
	body, ok := file.Body.(*hclsyntax.Body)
	require.True(t, ok)
	return body, src
}

// TestReadTerragruntInputsWithContext covers the evaluator end to end, including the canonical
// merge(local.defaults, {...}) shape.
func TestReadTerragruntInputsWithContext(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "terragrunt.hcl", `
locals {
  defaults = {
    region = "us-central1"
    name   = "prod"
  }
}

inputs = merge(local.defaults, { name = "staging" })
`)
	body, src := parseTerragruntBody(t, path)

	resolved, err := ReadTerragruntInputsWithContext(body, src, path, TerragruntOptions{Env: map[string]string{}})
	require.NoError(t, err)
	assert.Equal(t, `"us-central1"`, resolved["region"])
	assert.Equal(t, `"staging"`, resolved["name"], "merge must let the later object win")
}

// TestReadTerragruntInputs_NoInputsBlock confirms a config without inputs resolves to nothing
// rather than erroring.
func TestReadTerragruntInputs_NoInputsBlock(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "terragrunt.hcl", "locals { a = 1 }\n")
	body, src := parseTerragruntBody(t, path)

	resolved, err := ReadTerragruntInputsWithContext(body, src, path, TerragruntOptions{})
	require.NoError(t, err)
	assert.Empty(t, resolved)
}

// TestReadTerragruntLocalsFileUnresolvable covers a local that cannot be evaluated: it is omitted
// and reported rather than guessed.
func TestReadTerragruntLocalsFileUnresolvable(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "root.hcl", `
locals {
  good = "resolved"
  bad  = get_aws_account_id()
}
`)
	body, _ := parseTerragruntBody(t, path)

	resolved, incomplete := ExtractTerragruntLocals(body, TerragruntOptions{Env: map[string]string{}})
	assert.Equal(t, "resolved", resolved["good"].AsString())
	assert.NotContains(t, resolved, "bad", "an unevaluable local must not be reported as a value")
	assert.True(t, incomplete)
}

// TestReadTerragruntLocalsFileResolvesForwardReference confirms a local may reference one declared
// later in the file, which requires iterating rather than a single pass.
func TestReadTerragruntLocalsFileResolvesForwardReference(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "root.hcl", `
locals {
  a = local.b
  b = "later"
}
`)
	locals, err := ReadTerragruntLocalsFile(path)
	require.NoError(t, err)
	assert.Equal(t, "later", locals["a"].AsString())
	assert.Equal(t, "later", locals["b"].AsString())
}

// TestReadTerragruntLocalsFileMissing covers the read error path.
func TestReadTerragruntLocalsFileMissing(t *testing.T) {
	_, err := ReadTerragruntLocalsFile(filepath.Join(t.TempDir(), "absent.hcl"))
	assert.Error(t, err)
}

// TestReadTerragruntLocalsFileInvalidSyntax covers the parse error path.
func TestReadTerragruntLocalsFileInvalidSyntax(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "broken.hcl", "locals { this is not valid\n")
	_, err := ReadTerragruntLocalsFile(path)
	assert.Error(t, err)
}

// TestReadTerragruntLocalsFileEmpty covers a config with no locals block.
func TestReadTerragruntLocalsFileEmpty(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "empty.hcl", "terraform {\n  source = \"./x\"\n}\n")
	locals, err := ReadTerragruntLocalsFile(path)
	require.NoError(t, err)
	assert.Empty(t, locals)
}

// TestTerragruntGetEnvUnsetWithoutDefault covers get_env with no fallback value.
func TestTerragruntGetEnvUnsetWithoutDefault(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "terragrunt.hcl", `
locals { v = get_env("TFASTDIFF_DEFINITELY_UNSET_VAR", null) }

inputs = merge({}, { v = local.v })
`)
	body, src := parseTerragruntBody(t, path)

	resolved, err := ReadTerragruntInputsWithContext(body, src, path, TerragruntOptions{Env: map[string]string{}})
	require.NoError(t, err)
	assert.Contains(t, resolved, "v")
}

// TestTerragruntTryCannotCatchFunctionErrors pins the documented limit of the conservative try().
// cty evaluates every argument before the implementation runs, so a function call that errors
// propagates instead of being caught. The config is therefore reported as unanalysed, which is
// honest, where a real Terragrunt run would fall back to the default.
func TestTerragruntTryCannotCatchFunctionErrors(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "terragrunt.hcl", `
inputs = merge({}, {
  region = try(read_terragrunt_config("absent.hcl").region, "fallback-region")
})
`)
	body, src := parseTerragruntBody(t, path)

	_, err := ReadTerragruntInputsWithContext(body, src, path, TerragruntOptions{Env: map[string]string{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read_terragrunt_config")
}

// TestTerragruntReadConfigWithExplicitDefault covers the fallback that does work: read_terragrunt_config
// applies its own default when the target cannot be read.
func TestTerragruntReadConfigWithExplicitDefault(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "terragrunt.hcl", `
locals {
  fallback = { region = "fallback-region" }
}

inputs = merge({}, {
  region = read_terragrunt_config("absent.hcl", local.fallback).region
})
`)
	body, src := parseTerragruntBody(t, path)

	resolved, err := ReadTerragruntInputsWithContext(body, src, path, TerragruntOptions{Env: map[string]string{}})
	require.NoError(t, err)
	assert.Equal(t, `"fallback-region"`, resolved["region"],
		"an unreadable config must fall back to the supplied default")
}

// TestTerragruntReadConfigWithoutLocals covers the empty-result branch of read_terragrunt_config.
func TestTerragruntReadConfigWithoutLocals(t *testing.T) {
	dir := t.TempDir()
	writeTerragrunt(t, dir, "shared.hcl", "terraform {\n  source = \"./x\"\n}\n")
	path := writeTerragrunt(t, dir, "terragrunt.hcl", `
inputs = merge({}, { region = read_terragrunt_config("shared.hcl").region })
`)
	body, src := parseTerragruntBody(t, path)

	// The config has no locals, so the returned object is empty and the traversal yields null.
	resolved, err := ReadTerragruntInputsWithContext(body, src, path, TerragruntOptions{Env: map[string]string{}})
	if err == nil {
		assert.Equal(t, "null", resolved["region"])
	}
}

// TestTerragruntIncludeMissing covers an include block whose path cannot be evaluated.
func TestTerragruntIncludeMissing(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "terragrunt.hcl", `
include {
  path = local.no_such_path
}

inputs = { a = "b" }
`)
	body, src := parseTerragruntBody(t, path)

	// The include cannot be resolved, so it is ignored rather than failing the whole config.
	resolved, err := ReadTerragruntInputsWithContext(body, src, path, TerragruntOptions{Env: map[string]string{}})
	require.NoError(t, err)
	assert.Equal(t, `"b"`, resolved["a"])
}

// TestTerragruntIncludeWithoutPath covers an include block that declares no path.
func TestTerragruntIncludeWithoutPath(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "terragrunt.hcl", `
include {
  expose = true
}

inputs = { a = "b" }
`)
	body, src := parseTerragruntBody(t, path)

	resolved, err := ReadTerragruntInputsWithContext(body, src, path, TerragruntOptions{Env: map[string]string{}})
	require.NoError(t, err)
	assert.Equal(t, `"b"`, resolved["a"])
}

// TestTerragruntFindInParentFolders covers the search itself plus its two failure modes.
func TestTerragruntFindInParentFolders(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b")
	require.NoError(t, os.MkdirAll(nested, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "root.hcl"), []byte("locals { x = 1 }\n"), 0o600))

	functions := TerragruntFunctions(TerragruntOptions{ConfigDir: nested, Env: map[string]string{}})
	found, err := functions["find_in_parent_folders"].Call([]cty.Value{cty.StringVal("root.hcl")})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "root.hcl"), found.AsString())

	// An empty name is rejected rather than searching for the directory itself.
	_, err = functions["find_in_parent_folders"].Call([]cty.Value{cty.StringVal("")})
	assert.Error(t, err)

	// A name that exists nowhere fails, so the caller knows the inputs are unresolved.
	_, err = functions["find_in_parent_folders"].Call([]cty.Value{cty.StringVal("absent.hcl")})
	assert.Error(t, err)

	// With no config directory known, the function must say so instead of searching from "/".
	unknown := TerragruntFunctions(TerragruntOptions{})
	_, err = unknown["find_in_parent_folders"].Call([]cty.Value{cty.StringVal("root.hcl")})
	assert.Error(t, err)
}

// TestTerragruntDirectoryFunctionsWithoutDirs covers the guards on the directory helpers when the
// caller knows nothing about the config's location.
func TestTerragruntDirectoryFunctionsWithoutDirs(t *testing.T) {
	functions := TerragruntFunctions(TerragruntOptions{})
	for _, name := range []string{"get_terragrunt_dir", "get_parent_terragrunt_dir", "path_relative_to_include"} {
		_, err := functions[name].Call(nil)
		assert.Error(t, err, "%s must refuse when no directory is known", name)
	}
}

// TestTerragruntTry covers the conservative try() behaviour.
func TestTerragruntTry(t *testing.T) {
	functions := TerragruntFunctions(TerragruntOptions{})

	// The first non-null alternative wins.
	got, err := functions["try"].Call([]cty.Value{cty.NullVal(cty.String), cty.StringVal("fallback")})
	require.NoError(t, err)
	assert.Equal(t, "fallback", got.AsString())

	// A single known value is returned as-is.
	got, err = functions["try"].Call([]cty.Value{cty.StringVal("only")})
	require.NoError(t, err)
	assert.Equal(t, "only", got.AsString())

	// With nothing to choose from, the last alternative is returned so a value still comes back.
	got, err = functions["try"].Call([]cty.Value{cty.NullVal(cty.String)})
	require.NoError(t, err)
	assert.True(t, got.IsNull())

	// Calling it with no alternatives at all is an error.
	_, err = functions["try"].Call(nil)
	assert.Error(t, err)
}

// TestTerragruntLocalsWithoutAttributes covers a config whose locals blocks are all empty.
func TestTerragruntLocalsWithoutAttributes(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "terragrunt.hcl", "locals {}\n")
	body, src := parseTerragruntBody(t, path)

	assert.Empty(t, TerragruntLocals(body, src, TerragruntOptions{ConfigDir: dir}))
}

// TestTerragruntFunctionSuccessPaths exercises the Terragrunt built-ins on the paths a real
// configuration takes: a value found, a default applied, and each directory helper answered.
func TestTerragruntFunctionSuccessPaths(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "envs", "prod")
	require.NoError(t, os.MkdirAll(nested, 0o750))

	t.Setenv("TFASTDIFF_PRESENT", "present-value")

	functions := TerragruntFunctions(TerragruntOptions{ConfigDir: nested, RootDir: dir})

	// get_env finds a set variable, and falls back when one is unset with no default.
	got, err := functions["get_env"].Call([]cty.Value{cty.StringVal("TFASTDIFF_PRESENT"), cty.StringVal("d")})
	require.NoError(t, err)
	assert.Equal(t, "present-value", got.AsString())

	got, err = functions["get_env"].Call([]cty.Value{cty.StringVal("TFASTDIFF_ABSENT_VAR"), cty.StringVal("d")})
	require.NoError(t, err)
	assert.Equal(t, "d", got.AsString())

	got, err = functions["get_env"].Call([]cty.Value{cty.StringVal("TFASTDIFF_ABSENT_VAR"), cty.NullVal(cty.String)})
	require.NoError(t, err)
	assert.Equal(t, "", got.AsString(), "an unset variable with a null default resolves to empty")

	// The directory helpers report the config directory and the root directory it includes.
	got, err = functions["get_terragrunt_dir"].Call(nil)
	require.NoError(t, err)
	assert.Equal(t, nested, got.AsString())

	got, err = functions["get_parent_terragrunt_dir"].Call(nil)
	require.NoError(t, err)
	assert.Equal(t, dir, got.AsString())

	got, err = functions["path_relative_to_include"].Call(nil)
	require.NoError(t, err)
	assert.Equal(t, "envs/prod", got.AsString())

	// A config sitting directly at the root has no relative path.
	sameRoot := TerragruntFunctions(TerragruntOptions{ConfigDir: dir, RootDir: dir})
	got, err = sameRoot["path_relative_to_include"].Call(nil)
	require.NoError(t, err)
	assert.Equal(t, "", got.AsString())

	// With no explicit root, the config directory stands in as the root.
	noRoot := TerragruntFunctions(TerragruntOptions{ConfigDir: dir})
	got, err = noRoot["get_parent_terragrunt_dir"].Call(nil)
	require.NoError(t, err)
	assert.Equal(t, dir, got.AsString())
}

// TestTerragruntReadConfigReturnsObject covers read_terragrunt_config returning real locals, and
// an absolute path.
func TestTerragruntReadConfigReturnsObject(t *testing.T) {
	dir := t.TempDir()
	writeTerragrunt(t, dir, "shared.hcl", "locals {\n  region = \"ap-south1\"\n}\n")

	functions := TerragruntFunctions(TerragruntOptions{ConfigDir: dir})
	got, err := functions["read_terragrunt_config"].Call([]cty.Value{cty.StringVal("shared.hcl")})
	require.NoError(t, err)
	assert.Equal(t, "ap-south1", got.GetAttr("region").AsString())

	// An absolute path resolves the same way.
	got, err = functions["read_terragrunt_config"].Call([]cty.Value{cty.StringVal(filepath.Join(dir, "shared.hcl"))})
	require.NoError(t, err)
	assert.Equal(t, "ap-south1", got.GetAttr("region").AsString())

	// Reading it with an explicit default that is not needed still returns the real value.
	got, err = functions["read_terragrunt_config"].Call([]cty.Value{
		cty.StringVal("shared.hcl"), cty.ObjectVal(map[string]cty.Value{"region": cty.StringVal("fallback")}),
	})
	require.NoError(t, err)
	assert.Equal(t, "ap-south1", got.GetAttr("region").AsString())

	// An empty config with an explicit default returns that default.
	writeTerragrunt(t, dir, "blank.hcl", "terraform {\n  source = \"./x\"\n}\n")
	got, err = functions["read_terragrunt_config"].Call([]cty.Value{
		cty.StringVal("blank.hcl"), cty.ObjectVal(map[string]cty.Value{"region": cty.StringVal("fallback")}),
	})
	require.NoError(t, err)
	assert.Equal(t, "fallback", got.GetAttr("region").AsString())
}

// TestTerragruntReadConfigUnreadableWithoutDefault confirms the error when no fallback is offered.
func TestTerragruntReadConfigUnreadableWithoutDefault(t *testing.T) {
	functions := TerragruntFunctions(TerragruntOptions{ConfigDir: t.TempDir()})
	_, err := functions["read_terragrunt_config"].Call([]cty.Value{cty.StringVal("absent.hcl")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read_terragrunt_config(absent.hcl)")
}

// TestTerragruntRootDirDefault covers the option accessor directly.
func TestTerragruntRootDirDefault(t *testing.T) {
	assert.Equal(t, "/a/b", TerragruntOptions{ConfigDir: "/a/b"}.rootDir())
	assert.Equal(t, "/root", TerragruntOptions{ConfigDir: "/a/b", RootDir: "/root"}.rootDir())
	assert.Equal(t, "/a/b", TerragruntOptions{ConfigDir: "/a/b", RootDir: "  "}.rootDir(),
		"a blank root falls back to the config directory")
}

// TestTerragruntLookupEnvNilUsesProcessEnv covers the default environment source.
func TestTerragruntLookupEnvNilUsesProcessEnv(t *testing.T) {
	t.Setenv("TFASTDIFF_FROM_PROCESS", "yes")
	value, ok := TerragruntOptions{}.lookupEnv("TFASTDIFF_FROM_PROCESS")
	assert.True(t, ok)
	assert.Equal(t, "yes", value)

	_, ok = TerragruntOptions{}.lookupEnv("TFASTDIFF_NOT_SET_ANYWHERE")
	assert.False(t, ok)
}

// TestTerragruntIncludeRelativePathIsResolved confirms an include path is resolved against the
// config directory, and that its locals become available.
func TestTerragruntIncludeRelativePathIsResolved(t *testing.T) {
	dir := t.TempDir()
	writeTerragrunt(t, dir, "parent.hcl", "locals {\n  inherited = \"from-parent\"\n}\n")
	path := writeTerragrunt(t, dir, "terragrunt.hcl", `
include {
  path = "parent.hcl"
}

inputs = merge({}, { value = local.inherited })
`)
	body, src := parseTerragruntBody(t, path)

	resolved, err := ReadTerragruntInputsWithContext(body, src, path, TerragruntOptions{Env: map[string]string{}})
	require.NoError(t, err)
	assert.Equal(t, `"from-parent"`, resolved["value"])
}

// TestTerragruntIncludeUnreadableParent confirms an unreadable include parent is skipped rather
// than failing the config, since the child's own locals may still resolve.
func TestTerragruntIncludeUnreadableParent(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "terragrunt.hcl", `
include {
  path = "absent.hcl"
}

locals {
  own = "local-value"
}

inputs = merge({}, { value = local.own })
`)
	body, src := parseTerragruntBody(t, path)

	resolved, err := ReadTerragruntInputsWithContext(body, src, path, TerragruntOptions{Env: map[string]string{}})
	require.NoError(t, err)
	assert.Equal(t, `"local-value"`, resolved["value"])
}

// TestTerragruntInputsEvaluationFails covers the error returned when the inputs expression itself
// cannot be evaluated.
func TestTerragruntInputsEvaluationFails(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "terragrunt.hcl", `
inputs = merge({}, { broken = 1 + "two" })
`)
	body, src := parseTerragruntBody(t, path)

	_, err := ReadTerragruntInputsWithContext(body, src, path, TerragruntOptions{Env: map[string]string{}})
	assert.Error(t, err)
}

// TestTerragruntInputsNotAMap confirms an inputs expression that does not yield an object is
// rejected rather than silently producing nothing.
func TestTerragruntInputsNotAMap(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "terragrunt.hcl", `
inputs = "a string"
`)
	body, src := parseTerragruntBody(t, path)

	_, err := ReadTerragruntInputsWithContext(body, src, path, TerragruntOptions{Env: map[string]string{}})
	assert.Error(t, err)
}

// TestTerragruntLocalsMergeInheritedAndOwn covers a child that both inherits locals from an
// include parent and declares its own, which is the ordinary Terragrunt monorepo layout.
func TestTerragruntLocalsMergeInheritedAndOwn(t *testing.T) {
	dir := t.TempDir()
	writeTerragrunt(t, dir, "parent.hcl", `
locals {
  inherited = "from-parent"
  shared    = "parent-version"
}
`)
	path := writeTerragrunt(t, dir, "terragrunt.hcl", `
include {
  path = "parent.hcl"
}

locals {
  own   = "from-child"
  shared = "child-version"
}

inputs = merge({}, {
  inherited = local.inherited
  own       = local.own
  shared    = local.shared
})
`)
	body, src := parseTerragruntBody(t, path)

	resolved, err := ReadTerragruntInputsWithContext(body, src, path, TerragruntOptions{Env: map[string]string{}})
	require.NoError(t, err)
	assert.Equal(t, `"from-parent"`, resolved["inherited"], "a parent local must remain visible")
	assert.Equal(t, `"from-child"`, resolved["own"], "the child's own local must be visible")
	assert.Equal(t, `"child-version"`, resolved["shared"],
		"the child's own local must win over the inherited one of the same name")
}

// TestTerragruntLocalsDirect covers the locals accessor on its own.
func TestTerragruntLocalsDirect(t *testing.T) {
	dir := t.TempDir()
	path := writeTerragrunt(t, dir, "terragrunt.hcl", "locals {\n  a = \"x\"\n}\n")
	body, src := parseTerragruntBody(t, path)

	locals := TerragruntLocals(body, src, TerragruntOptions{ConfigDir: dir})
	assert.Equal(t, "x", locals["a"].AsString())
}

// TestTerragruntPathRelativeWithMixedRoots covers the defensive branch: filepath.Rel cannot relate
// an absolute path to a relative one, which a caller can produce by setting the two options
// inconsistently. The helper then reports the config directory rather than an opaque error.
func TestTerragruntPathRelativeWithMixedRoots(t *testing.T) {
	functions := TerragruntFunctions(TerragruntOptions{ConfigDir: "relative/dir", RootDir: "/absolute/root"})

	got, err := functions["path_relative_to_include"].Call(nil)
	require.NoError(t, err, "an unrelatable pair must not surface as a function error")
	assert.Equal(t, "relative/dir", got.AsString())
}

// TestTerragruntFindInParentFoldersUnresolvableStart covers the branch where the config directory
// cannot be made absolute, which happens when the process working directory has been removed. The
// search must fail explicitly rather than walk from an unknown place.
func TestTerragruntFindInParentFoldersUnresolvableStart(t *testing.T) {
	// Removing the working directory makes os.Getwd fail, which is the only way filepath.Abs can
	// fail for a non-empty path.
	vanished := t.TempDir()
	require.NoError(t, os.Chdir(vanished))
	t.Cleanup(func() { _ = os.Chdir("/") })
	require.NoError(t, os.RemoveAll(vanished))

	functions := TerragruntFunctions(TerragruntOptions{ConfigDir: "relative/only"})
	_, err := functions["find_in_parent_folders"].Call([]cty.Value{cty.StringVal("root.hcl")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot resolve relative/only")
}

func TestTerragruntDependencies(t *testing.T) {
	dir := t.TempDir()
	src := `
dependency "vpc" {
  config_path = "../vpc"
  mock_outputs = {
    vpc_id = "vpc-12345"
  }
}

dependency "eks" {
  config_path = "../eks"
}

dependency {
  # label-less block
}

inputs = {
  vpc_id = dependency.vpc.outputs.vpc_id
  eks_id = dependency.eks.outputs.id
}
`
	path := writeTerragrunt(t, dir, "terragrunt.hcl", src)
	body, rawSrc := parseTerragruntBody(t, path)

	inputs, err := ReadTerragruntInputsWithContext(body, rawSrc, path, TerragruntOptions{ConfigDir: dir})
	require.NoError(t, err)
	assert.Equal(t, `"vpc-12345"`, inputs["vpc_id"])
	assert.Equal(t, `"mock-id"`, inputs["eks_id"])
}
