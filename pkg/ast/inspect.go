package ast

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

var moduleOutputRefRegex = regexp.MustCompile(`module\.([a-zA-Z0-9_-]+)\.([a-zA-Z0-9_-]+)`)

// Inspect parses and extracts the normalized AST for a Terraform module at the given directory.
func Inspect(dir string) (*ModuleInfo, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to access module path %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("module path %s is not a directory", dir)
	}

	tfMod, diags := tfconfig.LoadModule(dir)
	if diags.HasErrors() {
		return nil, fmt.Errorf("diagnostics error loading module %s: %s", dir, diags.Error())
	}

	info := &ModuleInfo{
		Path:                    dir,
		Variables:               make(map[string]VariableDef),
		Outputs:                 make(map[string]OutputDef),
		ModuleCalls:             make(map[string]ModuleCallDef),
		RequiredProviders:       make(map[string]ProviderRequirement),
		WrapperOutputReferences: make(map[string]OutputReference),
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
			info.RequiredProviders[name] = ProviderRequirement{
				Name:               name,
				Source:             req.Source,
				VersionConstraints: req.VersionConstraints,
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

	return info, nil
}

// enrichFromHCLFiles scans top-level .tf and .tf.json files in dir to extract passed inputs and output delegations.
func enrichFromHCLFiles(dir string, info *ModuleInfo) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".tf") {
			continue
		}

		filePath := filepath.Join(dir, name)
		// #nosec G304 -- directory entry read within inspected module
		src, err := os.ReadFile(filepath.Clean(filePath))
		if err != nil {
			continue
		}

		file, diags := hclsyntax.ParseConfig(src, filePath, hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() || file == nil {
			continue
		}

		body := file.Body.(*hclsyntax.Body)

		for _, block := range body.Blocks {
			switch block.Type {
			case "module":
				if len(block.Labels) == 0 {
					continue
				}
				modName := block.Labels[0]
				mc, exists := info.ModuleCalls[modName]
				if !exists {
					mc = ModuleCallDef{
						Name:             modName,
						InputExpressions: make(map[string]string),
					}
				}

				var passed []string
				for attrName, attr := range block.Body.Attributes {
					exprBytes := src[attr.Expr.Range().Start.Byte:attr.Expr.Range().End.Byte]
					exprStr := strings.TrimSpace(string(exprBytes))

					switch attrName {
					case "source":
						mc.Source = strings.Trim(exprStr, `"'`)
					case "version":
						mc.Version = strings.Trim(exprStr, `"'`)
					default:
						passed = append(passed, attrName)
						mc.InputExpressions[attrName] = exprStr
					}
				}
				sort.Strings(passed)
				mc.PassedInputs = passed
				info.ModuleCalls[modName] = mc

			case "output":
				if len(block.Labels) == 0 {
					continue
				}
				outName := block.Labels[0]
				if valAttr, ok := block.Body.Attributes["value"]; ok {
					valBytes := src[valAttr.Expr.Range().Start.Byte:valAttr.Expr.Range().End.Byte]
					valStr := string(valBytes)
					matches := moduleOutputRefRegex.FindStringSubmatch(valStr)
					if len(matches) == 3 {
						info.WrapperOutputReferences[outName] = OutputReference{
							OutputName:   outName,
							TargetModule: matches[1],
							TargetOutput: matches[2],
						}
					}
				}
			}
		}
	}
}
