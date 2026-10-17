package ast

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

// Inspect parses and extracts the normalized AST for a Terraform module at the given directory.
func Inspect(dir string) (info *ModuleInfo, err error) {
	defer recoverParseError(dir, &err)

	fi, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to access module path %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("module path %s is not a directory", dir)
	}

	// The directory has already been stat'ed successfully, so cleaning the path is enough to key
	// the inspection cache without depending on the process working directory.
	cachePath := filepath.Clean(dir)
	fingerprint, err := moduleFingerprint(dir)
	if err != nil {
		return nil, err
	}
	cached, found, cacheErr := cachedModule(cachePath, fingerprint)
	if cacheErr != nil {
		// A corrupted cache entry must never break the analysis: drop it and inspect from source.
		forgetModuleCacheEntry(cachePath)
	}
	if found {
		cached.Path = dir
		return cached, nil
	}

	tfMod, diags := tfconfig.LoadModule(dir)
	if diags.HasErrors() {
		return nil, fmt.Errorf("diagnostics error loading module %s: %s", dir, diags.Error())
	}

	info = &ModuleInfo{
		Path:                    dir,
		Variables:               make(map[string]VariableDef),
		Outputs:                 make(map[string]OutputDef),
		ModuleCalls:             make(map[string]ModuleCallDef),
		RequiredProviders:       make(map[string]ProviderRequirement),
		WrapperOutputReferences: make(map[string]OutputReference),
		CheckBlocks:             make(map[string]CheckBlockDef),
		Functions:               make(map[string]FunctionDef),
	}

	if tfMod != nil {
		info.RequiredCore = tfMod.RequiredCore

		// Variables
		for name, v := range tfMod.Variables {
			tStr := v.Type
			if strings.TrimSpace(tStr) == "" {
				tStr = "any"
			}
			info.Variables[name] = VariableDef{
				Name:        v.Name,
				Type:        tStr,
				Description: v.Description,
				Required:    v.Required,
				Default:     v.Default,
				HasDefault:  v.Default != nil || !v.Required,
				Sensitive:   v.Sensitive,
				Deprecated:  v.Deprecated,
				Pos:         v.Pos,
			}
		}

		// Outputs
		for name, o := range tfMod.Outputs {
			info.Outputs[name] = OutputDef{
				Name:        o.Name,
				Description: o.Description,
				Sensitive:   o.Sensitive,
				Type:        o.Type,
				Deprecated:  o.Deprecated,
				Pos:         o.Pos,
			}
		}

		// Providers
		for name, req := range tfMod.RequiredProviders {
			var aliases []string
			for _, alias := range req.ConfigurationAliases {
				if alias.Alias != "" {
					aliases = append(aliases, alias.Name+"."+alias.Alias)
				}
			}
			sort.Strings(aliases)
			info.RequiredProviders[name] = ProviderRequirement{
				Name:                 name,
				Source:               req.Source,
				VersionConstraints:   req.VersionConstraints,
				ConfigurationAliases: aliases,
			}
		}

		// Base Module Calls
		for name, mc := range tfMod.ModuleCalls {
			info.ModuleCalls[name] = ModuleCallDef{
				Name:             mc.Name,
				Source:           mc.Source,
				Version:          mc.Version,
				PassedInputs:     []string{},
				InputExpressions: make(map[string]string),
				Pos:              mc.Pos,
			}
		}
	}

	// Deep HCL parsing to extract passed arguments to child modules and wrapper output delegations
	enrichFromHCLFiles(dir, info)

	// The inspection cache is an optimisation, so a write failure must not invalidate the result.
	_ = cacheModule(cachePath, fingerprint, info)
	return info, nil
}

// recoverParseError converts a panic raised while parsing untrusted Terraform source into a normal
// error so a malformed module can never crash the CLI.
func recoverParseError(dir string, err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("panic during AST parsing of module %s: %v", dir, r)
	}
}

// enrichFromHCLFiles scans top-level Terraform files for module inputs, nullable settings,
// and output delegations that are not exposed by terraform-config-inspect.
func enrichFromHCLFiles(dir string, info *ModuleInfo) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	parser := hclparse.NewParser()
	schema := &hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "variable", LabelNames: []string{"name"}},
			{Type: "module", LabelNames: []string{"name"}},
			{Type: "output", LabelNames: []string{"name"}},
			{Type: "removed"},
			{Type: "check", LabelNames: []string{"name"}},
			{Type: "import"},
			{Type: "function", LabelNames: []string{"name"}},
			{Type: "encryption"},
		},
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		isJSON := strings.HasSuffix(name, ".tf.json")
		if !isJSON && !strings.HasSuffix(name, ".tf") {
			continue
		}

		filePath := filepath.Join(dir, name)
		// #nosec G304 -- directory entry read within inspected module
		src, err := os.ReadFile(filepath.Clean(filePath))
		if err != nil {
			continue
		}

		var file *hcl.File
		var diags hcl.Diagnostics
		if isJSON {
			file, diags = parser.ParseJSON(src, filePath)
		} else {
			file, diags = parser.ParseHCL(src, filePath)
		}
		if diags.HasErrors() || file == nil {
			continue
		}

		content, _, diags := file.Body.PartialContent(schema)
		if diags.HasErrors() || content == nil {
			continue
		}
		for _, block := range content.Blocks {
			attrs, _ := block.Body.JustAttributes()
			switch block.Type {
			case "variable":
				variable := info.Variables[block.Labels[0]]
				attr, exists := attrs["nullable"]
				if exists {
					value := strings.TrimSpace(string(src[attr.Expr.Range().Start.Byte:attr.Expr.Range().End.Byte]))
					if value == "true" || value == "false" {
						nullable := value == "true"
						variable.Nullable = &nullable
					}
				}

				validationContent, _, validationDiags := block.Body.PartialContent(&hcl.BodySchema{
					Blocks: []hcl.BlockHeaderSchema{{Type: "validation"}},
				})
				if !validationDiags.HasErrors() && validationContent != nil {
					for _, validation := range validationContent.Blocks {
						validationAttrs, _ := validation.Body.JustAttributes()
						condition, hasCondition := validationAttrs["condition"]
						message, hasMessage := validationAttrs["error_message"]
						if hasCondition {
							rule := strings.TrimSpace(string(src[condition.Expr.Range().Start.Byte:condition.Expr.Range().End.Byte]))
							if hasMessage {
								rule += " => " + strings.TrimSpace(string(src[message.Expr.Range().Start.Byte:message.Expr.Range().End.Byte]))
							}
							variable.ValidationRules = append(variable.ValidationRules, rule)
						}
					}
					sort.Strings(variable.ValidationRules)
				}
				if variable.Nullable != nil || len(variable.ValidationRules) > 0 {
					info.Variables[block.Labels[0]] = variable
				}
			case "module":
				modName := block.Labels[0]
				mc, exists := info.ModuleCalls[modName]
				if !exists {
					mc = ModuleCallDef{
						Name:             modName,
						InputExpressions: make(map[string]string),
					}
				}

				var passed []string
				for attrName, attr := range attrs {
					exprBytes := src[attr.Expr.Range().Start.Byte:attr.Expr.Range().End.Byte]
					exprStr := strings.TrimSpace(string(exprBytes))

					switch attrName {
					case "source":
						mc.Source = strings.Trim(exprStr, `"'`)
					case "version":
						mc.Version = strings.Trim(exprStr, `"'`)
					case "count", "for_each", "providers", "depends_on":
						continue
					default:
						passed = append(passed, attrName)
						mc.InputExpressions[attrName] = exprStr
					}
				}
				sort.Strings(passed)
				mc.PassedInputs = passed
				info.ModuleCalls[modName] = mc

			case "output":
				outName := block.Labels[0]
				if valAttr, ok := attrs["value"]; ok {
					for _, traversal := range valAttr.Expr.Variables() {
						if len(traversal) < 2 {
							continue
						}
						root, okRoot := traversal[0].(hcl.TraverseRoot)
						module, okModule := traversal[1].(hcl.TraverseAttr)
						if !okRoot || root.Name != "module" || !okModule {
							continue
						}
						for _, step := range traversal[2:] {
							output, okOutput := step.(hcl.TraverseAttr)
							if !okOutput {
								continue
							}
							info.WrapperOutputReferences[outName] = OutputReference{
								OutputName:   outName,
								TargetModule: module.Name,
								TargetOutput: output.Name,
							}
							break
						}
					}
				}

			case "removed":
				from := ""
				if fromAttr, ok := attrs["from"]; ok {
					from = strings.TrimSpace(string(src[fromAttr.Expr.Range().Start.Byte:fromAttr.Expr.Range().End.Byte]))
				}
				destroy := true
				lifecycleContent, _, _ := block.Body.PartialContent(&hcl.BodySchema{
					Blocks: []hcl.BlockHeaderSchema{{Type: "lifecycle"}},
				})
				if lifecycleContent != nil {
					for _, lc := range lifecycleContent.Blocks {
						lcAttrs, _ := lc.Body.JustAttributes()
						if dAttr, ok := lcAttrs["destroy"]; ok {
							val := strings.TrimSpace(string(src[dAttr.Expr.Range().Start.Byte:dAttr.Expr.Range().End.Byte]))
							if val == "false" {
								destroy = false
							}
						}
					}
				}
				info.RemovedBlocks = append(info.RemovedBlocks, RemovedBlockDef{
					From:    from,
					Destroy: destroy,
					Pos:     tfconfig.SourcePos{Filename: filePath, Line: block.DefRange.Start.Line},
				})

			case "check":
				checkName := block.Labels[0]
				var asserts []string
				assertContent, _, _ := block.Body.PartialContent(&hcl.BodySchema{
					Blocks: []hcl.BlockHeaderSchema{{Type: "assert"}},
				})
				if assertContent != nil {
					for _, ass := range assertContent.Blocks {
						assAttrs, _ := ass.Body.JustAttributes()
						if cond, ok := assAttrs["condition"]; ok {
							condStr := strings.TrimSpace(string(src[cond.Expr.Range().Start.Byte:cond.Expr.Range().End.Byte]))
							asserts = append(asserts, condStr)
						}
					}
				}
				info.CheckBlocks[checkName] = CheckBlockDef{
					Name:        checkName,
					AssertRules: asserts,
					Pos:         tfconfig.SourcePos{Filename: filePath, Line: block.DefRange.Start.Line},
				}

			case "import":
				to := ""
				if toAttr, ok := attrs["to"]; ok {
					to = strings.TrimSpace(string(src[toAttr.Expr.Range().Start.Byte:toAttr.Expr.Range().End.Byte]))
				}
				id := ""
				if idAttr, ok := attrs["id"]; ok {
					id = strings.Trim(strings.TrimSpace(string(src[idAttr.Expr.Range().Start.Byte:idAttr.Expr.Range().End.Byte])), `"'`)
				}
				info.ImportBlocks = append(info.ImportBlocks, ImportBlockDef{
					To:  to,
					ID:  id,
					Pos: tfconfig.SourcePos{Filename: filePath, Line: block.DefRange.Start.Line},
				})

			case "function":
				fnName := block.Labels[0]
				summary := ""
				if sAttr, ok := attrs["summary"]; ok {
					summary = strings.Trim(strings.TrimSpace(string(src[sAttr.Expr.Range().Start.Byte:sAttr.Expr.Range().End.Byte])), `"'`)
				}
				retType := ""
				if rAttr, ok := attrs["return"]; ok {
					retType = strings.Trim(strings.TrimSpace(string(src[rAttr.Expr.Range().Start.Byte:rAttr.Expr.Range().End.Byte])), `"'`)
				}
				info.Functions[fnName] = FunctionDef{
					Name:       fnName,
					Summary:    summary,
					ReturnType: retType,
					Pos:        tfconfig.SourcePos{Filename: filePath, Line: block.DefRange.Start.Line},
				}

			case "encryption":
				provider := ""
				kpContent, _, _ := block.Body.PartialContent(&hcl.BodySchema{
					Blocks: []hcl.BlockHeaderSchema{{Type: "key_provider", LabelNames: []string{"type", "name"}}},
				})
				if kpContent != nil && len(kpContent.Blocks) > 0 {
					provider = kpContent.Blocks[0].Labels[0]
				}
				info.EncryptionBlocks = append(info.EncryptionBlocks, EncryptionBlockDef{
					KeyProvider: provider,
					Pos:         tfconfig.SourcePos{Filename: filePath, Line: block.DefRange.Start.Line},
				})
			}
		}
	}
}
