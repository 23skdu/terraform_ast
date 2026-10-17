package eval

import (
	"encoding/json"
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

// StandardFunctions returns pure implementations of common Terraform built-in functions.
func StandardFunctions() map[string]function.Function {
	return map[string]function.Function{
		"concat":     stdlib.ConcatFunc,
		"merge":      stdlib.MergeFunc,
		"lookup":     stdlib.LookupFunc,
		"coalesce":   stdlib.CoalesceFunc,
		"element":    stdlib.ElementFunc,
		"lower":      stdlib.LowerFunc,
		"upper":      stdlib.UpperFunc,
		"trimspace":  stdlib.TrimSpaceFunc,
		"jsondecode": makeJSONDecodeFunc(),
	}
}

func makeJSONDecodeFunc() function.Function {
	return function.New(&function.Spec{
		Description: "Decodes a JSON string into an HCL value.",
		Params:      []function.Parameter{{Name: "str", Type: cty.String}},
		Type: func(args []cty.Value) (cty.Type, error) {
			return cty.DynamicPseudoType, nil
		},
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			var parsed interface{}
			if err := json.Unmarshal([]byte(args[0].AsString()), &parsed); err != nil {
				return cty.NilVal, fmt.Errorf("invalid json in jsondecode: %w", err)
			}
			return toCtyValue(parsed), nil
		},
	})
}

func toCtyValue(v interface{}) cty.Value {
	switch val := v.(type) {
	case nil:
		return cty.NilVal
	case bool:
		return cty.BoolVal(val)
	case float64:
		return cty.NumberFloatVal(val)
	case string:
		return cty.StringVal(val)
	case []interface{}:
		var list []cty.Value
		for _, item := range val {
			list = append(list, toCtyValue(item))
		}
		if len(list) == 0 {
			return cty.ListValEmpty(cty.DynamicPseudoType)
		}
		return cty.TupleVal(list)
	case map[string]interface{}:
		obj := make(map[string]cty.Value)
		for k, item := range val {
			obj[k] = toCtyValue(item)
		}
		if len(obj) == 0 {
			return cty.EmptyObjectVal
		}
		return cty.ObjectVal(obj)
	default:
		return cty.StringVal(fmt.Sprintf("%v", val))
	}
}

// BuildEvalContext creates an HCL evaluation context loaded with standard functions and variables.
func BuildEvalContext(vars map[string]cty.Value, locals map[string]cty.Value) *hcl.EvalContext {
	ctx := &hcl.EvalContext{
		Functions: StandardFunctions(),
		Variables: make(map[string]cty.Value),
	}

	if len(vars) > 0 {
		ctx.Variables["var"] = cty.ObjectVal(vars)
	} else {
		ctx.Variables["var"] = cty.EmptyObjectVal
	}

	if len(locals) > 0 {
		ctx.Variables["local"] = cty.ObjectVal(locals)
	} else {
		ctx.Variables["local"] = cty.EmptyObjectVal
	}

	return ctx
}

// EvaluateExpression parses and evaluates an HCL expression string against an EvalContext.
func EvaluateExpression(exprStr string, ctx *hcl.EvalContext) (cty.Value, error) {
	expr, diags := hclsyntax.ParseExpression([]byte(exprStr), "eval.tf", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return cty.NilVal, fmt.Errorf("failed to parse expression: %s", diags.Error())
	}

	val, evalDiags := expr.Value(ctx)
	if evalDiags.HasErrors() {
		return cty.NilVal, fmt.Errorf("failed to evaluate expression: %s", evalDiags.Error())
	}
	return val, nil
}

// ResolveTerragruntInputs evaluates a dynamic Terragrunt inputs expression (e.g. merge(local.defaults, {...}))
// and returns a static map of input key-value representations.
func ResolveTerragruntInputs(inputsExprStr string, ctx *hcl.EvalContext) (map[string]string, error) {
	val, err := EvaluateExpression(inputsExprStr, ctx)
	if err != nil {
		return nil, err
	}
	if !val.IsKnown() || val.IsNull() {
		return nil, fmt.Errorf("evaluated inputs is null or unknown")
	}

	if !val.Type().IsMapType() && !val.Type().IsObjectType() {
		return nil, fmt.Errorf("evaluated inputs is not a map or object (got %s)", val.Type().FriendlyName())
	}

	res := make(map[string]string)
	it := val.ElementIterator()
	for it.Next() {
		k, v := it.Element()
		keyStr := k.AsString()
		if !v.IsKnown() || v.IsNull() {
			res[keyStr] = "null"
			continue
		}
		switch v.Type() {
		case cty.String:
			res[keyStr] = fmt.Sprintf("%q", v.AsString())
		case cty.Bool:
			res[keyStr] = fmt.Sprintf("%t", v.True())
		case cty.Number:
			bf := v.AsBigFloat()
			res[keyStr] = bf.String()
		default:
			res[keyStr] = fmt.Sprintf("%#v", v)
		}
	}
	return res, nil
}
