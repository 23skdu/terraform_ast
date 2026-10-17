package eval

import (
	"os"
	"path/filepath"
	"testing"

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
