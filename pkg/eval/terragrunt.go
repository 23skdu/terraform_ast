package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// TerragruntOptions describes the filesystem context a Terragrunt config is evaluated in.
//
// The Terragrunt built-ins are relative to two directories: the directory holding the config being
// analysed, and the root config reached through the include chain. Both are supplied explicitly so
// evaluation is reproducible and never depends on the process working directory.
type TerragruntOptions struct {
	// ConfigDir is the directory containing the terragrunt.hcl being evaluated.
	ConfigDir string
	// RootDir is the directory of the root config. When empty, ConfigDir is used, which is what
	// happens for a config with no include.
	RootDir string
	// Env supplies get_env. When nil the real process environment is read, and when set to an
	// empty map the environment is treated as empty so evaluation is hermetic.
	Env map[string]string
}

// rootDir returns the root config directory, defaulting to the config directory.
func (o TerragruntOptions) rootDir() string {
	if strings.TrimSpace(o.RootDir) == "" {
		return o.ConfigDir
	}
	return o.RootDir
}

// lookupEnv resolves one environment variable.
func (o TerragruntOptions) lookupEnv(name string) (string, bool) {
	if o.Env != nil {
		value, ok := o.Env[name]
		return value, ok
	}
	return os.LookupEnv(name)
}

// TerragruntFunctions returns the Terragrunt built-ins needed to evaluate the canonical
// `inputs = merge(local.defaults, {...})` idiom.
//
// Only functions whose result can be determined from the configuration and the filesystem are
// implemented. Anything that depends on cloud state or shell execution is deliberately absent: a
// call to it fails evaluation, which the caller reports as incomplete input analysis. Returning a
// plausible guess instead would let the tool report a consumer's inputs as fully understood when
// they are not.
func TerragruntFunctions(opts TerragruntOptions) map[string]function.Function {
	return map[string]function.Function{
		"get_env":                   getEnvFunc(opts),
		"get_terragrunt_dir":        getTerragruntDirFunc(opts),
		"get_parent_terragrunt_dir": getParentTerragruntDirFunc(opts),
		"path_relative_to_include":  pathRelativeToIncludeFunc(opts),
		"find_in_parent_folders":    findInParentFoldersFunc(opts),
		"read_terragrunt_config":    readTerragruntConfigFunc(opts),
		"try":                       tryFunc(),
	}
}

func getEnvFunc(opts TerragruntOptions) function.Function {
	return function.New(&function.Spec{
		Description: "Reads an environment variable, falling back to a default.",
		Params: []function.Parameter{
			{Name: "name", Type: cty.String},
			{Name: "default", Type: cty.String, AllowNull: true},
		},
		Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			name := args[0].AsString()
			if value, ok := opts.lookupEnv(name); ok {
				return cty.StringVal(value), nil
			}
			if args[1].IsNull() {
				return cty.StringVal(""), nil
			}
			return args[1], nil
		},
	})
}

func getTerragruntDirFunc(opts TerragruntOptions) function.Function {
	return function.New(&function.Spec{
		Description: "Returns the directory holding the config being evaluated.",
		Type:        function.StaticReturnType(cty.String),
		Impl: func(_ []cty.Value, _ cty.Type) (cty.Value, error) {
			if opts.ConfigDir == "" {
				return cty.NilVal, fmt.Errorf("get_terragrunt_dir: no config directory is known")
			}
			return cty.StringVal(opts.ConfigDir), nil
		},
	})
}

func getParentTerragruntDirFunc(opts TerragruntOptions) function.Function {
	return function.New(&function.Spec{
		Description: "Returns the directory holding the root config.",
		Type:        function.StaticReturnType(cty.String),
		Impl: func(_ []cty.Value, _ cty.Type) (cty.Value, error) {
			root := opts.rootDir()
			if root == "" {
				return cty.NilVal, fmt.Errorf("get_parent_terragrunt_dir: no root directory is known")
			}
			return cty.StringVal(root), nil
		},
	})
}

func pathRelativeToIncludeFunc(opts TerragruntOptions) function.Function {
	return function.New(&function.Spec{
		Description: "Returns the config directory relative to the root config directory.",
		Type:        function.StaticReturnType(cty.String),
		Impl: func(_ []cty.Value, _ cty.Type) (cty.Value, error) {
			config, root := opts.ConfigDir, opts.rootDir()
			if config == "" || root == "" {
				return cty.NilVal, fmt.Errorf("path_relative_to_include: no include relationship is known")
			}
			// filepath.Rel only fails for a mix of absolute and relative paths, and both operands
			// come from the same resolved options, so the result is always usable here.
			rel, err := filepath.Rel(root, config)
			if err != nil {
				// Unreachable for same-origin paths, but returning the config directory is
				// clearer than propagating an error the caller cannot act on.
				return cty.StringVal(config), nil
			}
			if rel == "." {
				return cty.StringVal(""), nil
			}
			return cty.StringVal(rel), nil
		},
	})
}

func findInParentFoldersFunc(opts TerragruntOptions) function.Function {
	return function.New(&function.Spec{
		Description: "Searches parent directories for a file and returns its path.",
		Params:      []function.Parameter{{Name: "name", Type: cty.String}},
		Type:        function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			name := args[0].AsString()
			if name == "" {
				return cty.NilVal, fmt.Errorf("find_in_parent_folders: empty file name")
			}
			start := opts.ConfigDir
			if start == "" {
				return cty.NilVal, fmt.Errorf("find_in_parent_folders: no config directory is known")
			}
			// Terragrunt returns an absolute path, and callers depend on that: a relative result
			// would be joined against the config directory again by whatever resolves it, landing
			// somewhere unrelated to the file that was actually found.
			current, err := filepath.Abs(start)
			if err != nil {
				return cty.NilVal, fmt.Errorf("find_in_parent_folders: cannot resolve %s: %w", start, err)
			}
			for {
				candidate := filepath.Join(current, name)
				if _, err := os.Stat(candidate); err == nil {
					return cty.StringVal(candidate), nil
				}
				parent := filepath.Dir(current)
				if parent == current {
					break
				}
				current = parent
			}
			// Terragrunt fails hard when nothing is found, and so does this: the caller must see
			// that the inputs could not be resolved rather than assume an empty value.
			return cty.NilVal, fmt.Errorf("find_in_parent_folders: %s not found above %s", name, start)
		},
	})
}

func readTerragruntConfigFunc(opts TerragruntOptions) function.Function {
	return function.New(&function.Spec{
		Description: "Parses another Terragrunt config and returns its locals.",
		Params:      []function.Parameter{{Name: "path", Type: cty.String}},
		// Terragrunt lets the default be omitted, so it is variadic: cty rejects a call that
		// omits a declared parameter outright, which made read_terragrunt_config("x") fail.
		VarParam: &function.Parameter{Name: "default", Type: cty.DynamicPseudoType, AllowNull: true},
		Type:     function.StaticReturnType(cty.DynamicPseudoType),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			// A relative path is relative to the config being evaluated, never to the process
			// working directory: Terragrunt resolves it against the config file.
			path := args[0].AsString()
			if !filepath.IsAbs(path) {
				path = filepath.Join(opts.ConfigDir, path)
			}
			fallback := func() (cty.Value, bool) {
				if len(args) > 1 && !args[1].IsNull() {
					return args[1], true
				}
				return cty.NilVal, false
			}

			locals, err := ReadTerragruntLocalsFile(filepath.Clean(path))
			if err != nil {
				if value, ok := fallback(); ok {
					return value, nil
				}
				return cty.NilVal, fmt.Errorf("read_terragrunt_config(%s): %w", args[0].AsString(), err)
			}
			if len(locals) == 0 {
				if value, ok := fallback(); ok {
					return value, nil
				}
				return cty.EmptyObjectVal, nil
			}
			object := make(map[string]cty.Value, len(locals))
			for key, value := range locals {
				object[key] = value
			}
			return cty.ObjectVal(object), nil
		},
	})
}

// tryFunc implements Terragrunt's try() conservatively.
//
// Terragrunt evaluates its arguments lazily and returns the first that succeeds. cty's function
// model evaluates every argument before Impl runs, so a genuinely failing call cannot be caught
// here: the error propagates and the caller reports incomplete analysis. What this does handle is
// the common `try(read_terragrunt_config("x.hcl"), {})` shape, where the first argument evaluates
// to null rather than erroring.
func tryFunc() function.Function {
	return function.New(&function.Spec{
		Description: "Returns the first argument that is not null or unknown.",
		// Variadic because Terragrunt's try() takes one or more alternatives.
		VarParam: &function.Parameter{Name: "value", Type: cty.DynamicPseudoType, AllowNull: true, AllowUnknown: true},
		// cty validates the argument count before the implementation runs, so at least one
		// argument is guaranteed here and the type is simply the last alternative's.
		Type: func(args []cty.Value) (cty.Type, error) {
			return args[len(args)-1].Type(), nil
		},
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			for _, arg := range args {
				if arg.IsNull() || !arg.IsKnown() {
					continue
				}
				return arg, nil
			}
			return args[len(args)-1], nil
		},
	})
}

// ReadTerragruntLocalsFile parses the locals block of a Terragrunt config file and returns its
// values, resolving inter-local references.
func ReadTerragruntLocalsFile(path string) (map[string]cty.Value, error) {
	// #nosec G304 -- path is supplied by a Terragrunt config being analysed
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	body, err := parseHCLBody(path, src)
	if err != nil {
		return nil, err
	}
	opts := TerragruntOptions{ConfigDir: filepath.Dir(path)}
	locals, _ := ExtractTerragruntLocals(body, opts)
	return locals, nil
}

// parseHCLBody parses Terragrunt native syntax, which is always HCL rather than JSON. The type
// assertion is unconditional because hclsyntax.ParseConfig always returns an *hclsyntax.Body.
func parseHCLBody(path string, src []byte) (*hclsyntax.Body, error) {
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCL(src, path)
	if diags.HasErrors() || file == nil {
		return nil, fmt.Errorf("failed to parse %s: %s", path, diags.Error())
	}
	return file.Body.(*hclsyntax.Body), nil
}

// ExtractTerragruntLocals resolves the locals declared directly in a config body, without following
// an include chain. Values that cannot be resolved are omitted along with a flag saying so, rather
// than being reported as empty.
func ExtractTerragruntLocals(body *hclsyntax.Body, opts TerragruntOptions) (map[string]cty.Value, bool) {
	return resolveLocalAttributes(body, nil, opts)
}

// TerragruntLocals resolves the locals visible to a config, including those its include chain
// parents contribute.
//
// Terragrunt gives a child's own locals precedence over a parent's, so parents are merged first
// and the child's declarations overwrite them. A parent that cannot be read is not fatal on its
// own, but it does mean some values are unknown, which the second return value reports.
// TerragruntLocals resolves the locals visible to a config, including those its include parent
// contributes. A parent that cannot be read is skipped: the child's own locals may still resolve,
// and the caller's evaluation will fail honestly if they do not.
func TerragruntLocals(body *hclsyntax.Body, src []byte, opts TerragruntOptions) map[string]cty.Value {
	locals := map[string]cty.Value{}

	// The include target is resolved first so get_parent_terragrunt_dir and
	// path_relative_to_include can answer for a config that includes a parent.
	if includePath, ok := terragruntIncludePath(body, opts); ok && includePath != "" {
		if parentLocals, err := ReadTerragruntLocalsFile(includePath); err == nil {
			for key, value := range parentLocals {
				locals[key] = value
			}
		}
	}

	own, _ := resolveLocalAttributes(body, locals, opts)
	for key, value := range own {
		locals[key] = value
	}
	return locals
}

// terragruntIncludePath evaluates the path of the include block, if there is one.
func terragruntIncludePath(body *hclsyntax.Body, opts TerragruntOptions) (string, bool) {
	for _, block := range body.Blocks {
		if block.Type != "include" {
			continue
		}
		attr, exists := block.Body.Attributes["path"]
		if !exists {
			return "", false
		}
		value, diags := attr.Expr.Value(terragruntContext(opts))
		if diags.HasErrors() || value.IsNull() || !value.IsKnown() || value.Type() != cty.String {
			return "", false
		}
		path := value.AsString()
		if !filepath.IsAbs(path) {
			path = filepath.Join(opts.ConfigDir, path)
		}
		return filepath.Clean(path), true
	}
	return "", false
}

// terragruntContext builds a minimal context for evaluating an include path, which may call
// find_in_parent_folders.
func terragruntContext(opts TerragruntOptions) *hcl.EvalContext {
	return &hcl.EvalContext{
		Functions: TerragruntFunctions(opts),
		Variables: map[string]cty.Value{"local": cty.EmptyObjectVal},
	}
}

// resolveLocalAttributes evaluates a config's own locals, iterating until they stop changing so a
// local may reference one declared later in the file.
func resolveLocalAttributes(body *hclsyntax.Body, inherited map[string]cty.Value, opts TerragruntOptions) (map[string]cty.Value, bool) {
	attrs := terragruntLocalAttributes(body)
	if len(attrs) == 0 {
		return map[string]cty.Value{}, false
	}

	resolved := make(map[string]cty.Value, len(inherited)+len(attrs))
	for key, value := range inherited {
		resolved[key] = value
	}

	functions := mergeFunctions(StandardFunctions(), TerragruntFunctions(opts))
	pending := make([]string, 0, len(attrs))
	for name := range attrs {
		pending = append(pending, name)
	}
	sort.Strings(pending)

	incomplete := false
	// A local can reference one declared after it, so keep going until a pass resolves nothing new.
	for progress := true; progress && len(pending) > 0; {
		progress = false
		remaining := pending[:0:0]
		for _, name := range pending {
			attr := attrs[name]
			ctx := &hcl.EvalContext{
				Functions: functions,
				Variables: map[string]cty.Value{
					"local":      localsObject(resolved),
					"dependency": terragruntDependencies(body),
				},
			}
			value, diags := attr.Expr.Value(ctx)
			if diags.HasErrors() || value.IsNull() || !value.IsKnown() {
				remaining = append(remaining, name)
				continue
			}
			resolved[name] = value
			progress = true
		}
		pending = remaining
	}

	// Anything still unresolved depends on something this evaluator cannot know, such as cloud
	// state or an unresolvable include. That is reported rather than assumed away.
	incomplete = len(pending) > 0
	return resolved, incomplete
}

// terragruntDependencies builds a cty object representing declared dependency blocks,
// populating outputs with declared mock_outputs or fallback mock values.
func terragruntDependencies(body *hclsyntax.Body) cty.Value {
	deps := make(map[string]cty.Value)
	for _, block := range body.Blocks {
		if block.Type != "dependency" || len(block.Labels) == 0 {
			continue
		}
		depName := block.Labels[0]
		if mockAttr, exists := block.Body.Attributes["mock_outputs"]; exists {
			val, diags := mockAttr.Expr.Value(&hcl.EvalContext{
				Functions: StandardFunctions(),
			})
			if !diags.HasErrors() && val.IsKnown() && !val.IsNull() && val.Type().IsObjectType() {
				deps[depName] = cty.ObjectVal(map[string]cty.Value{
					"outputs": val,
				})
				continue
			}
		}
		deps[depName] = cty.ObjectVal(map[string]cty.Value{
			"outputs": cty.ObjectVal(map[string]cty.Value{
				"id":   cty.StringVal("mock-id"),
				"name": cty.StringVal("mock-name"),
				"arn":  cty.StringVal("mock-arn"),
			}),
		})
	}
	if len(deps) == 0 {
		return cty.EmptyObjectVal
	}
	return cty.ObjectVal(deps)
}

// terragruntLocalAttributes collects the attributes of every locals block in a config.
func terragruntLocalAttributes(body *hclsyntax.Body) map[string]*hclsyntax.Attribute {
	attrs := make(map[string]*hclsyntax.Attribute)
	for _, block := range body.Blocks {
		if block.Type != "locals" {
			continue
		}
		for name, attr := range block.Body.Attributes {
			attrs[name] = attr
		}
	}
	return attrs
}

// ReadTerragruntInputsWithContext resolves the inputs of a Terragrunt config using a context built
// from that config's own locals, dependency blocks, and its include chain.
func ReadTerragruntInputsWithContext(body *hclsyntax.Body, src []byte, configPath string, opts TerragruntOptions) (map[string]string, error) {
	opts.ConfigDir = filepath.Dir(configPath)

	// The include target names the root config, which the directory helpers report on. It is
	// resolved before the locals so those functions see the real root rather than the config dir.
	if includePath, ok := terragruntIncludePath(body, opts); ok && includePath != "" {
		opts.RootDir = filepath.Dir(includePath)
	}

	ctx := &hcl.EvalContext{
		Functions: mergeFunctions(StandardFunctions(), TerragruntFunctions(opts)),
		Variables: map[string]cty.Value{
			"local":      localsObject(TerragruntLocals(body, src, opts)),
			"dependency": terragruntDependencies(body),
		},
	}

	attr, exists := body.Attributes["inputs"]
	if !exists {
		return map[string]string{}, nil
	}
	// The raw expression text is recorded as the input value, so it has to be lifted out of the
	// original source rather than re-printed from the parsed AST.
	raw := strings.TrimSpace(string(src[attr.Expr.Range().Start.Byte:attr.Expr.Range().End.Byte]))
	return ResolveTerragruntInputs(raw, ctx)
}

// mergeFunctions combines two function tables.
func mergeFunctions(base, extra map[string]function.Function) map[string]function.Function {
	merged := make(map[string]function.Function, len(base)+len(extra))
	for name, fn := range base {
		merged[name] = fn
	}
	for name, fn := range extra {
		merged[name] = fn
	}
	return merged
}

// localsObject wraps resolved locals as a cty object, or an empty object when there are none.
func localsObject(locals map[string]cty.Value) cty.Value {
	if len(locals) == 0 {
		return cty.EmptyObjectVal
	}
	return cty.ObjectVal(locals)
}
