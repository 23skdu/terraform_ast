package ast

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

// InspectSource behaves like Inspect but parses an in-memory copy of one file instead of
// reading it from disk.
//
// An editor holds unsaved changes, so analysing the on-disk file describes a document the user is
// no longer looking at. Every sibling .tf file still comes from disk, because the language server
// is only handed the text of the document that changed.
//
// fileName must be the same path the file has on disk, so reported positions line up with the
// editor. The returned ModuleInfo reports the real directory in Path, and every source position
// is rewritten to point back at the real files rather than the temporary copies.
func InspectSource(dir, fileName, content string) (*ModuleInfo, error) {
	return inspectDir(dir, map[string][]byte{filepath.Clean(fileName): []byte(content)})
}

// inspectOverlay copies a module into a temporary directory with the overlay applied, inspects the
// copy, and rewrites the result so it refers to the real paths.
//
// tfconfig.LoadModule only accepts a directory, and the module cache is keyed by directory and
// content fingerprint, so an unsaved buffer cannot be represented by either. Copying is the only
// faithful option; it runs per keystroke, so only the .tf and .tf.json files are copied.
func inspectOverlay(dir string, overlay map[string][]byte) (*ModuleInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read module directory %s: %w", dir, err)
	}

	info := &ModuleInfo{
		Path:                    dir,
		Variables:               make(map[string]VariableDef),
		Outputs:                 make(map[string]OutputDef),
		ModuleCalls:             make(map[string]ModuleCallDef),
		RequiredProviders:       make(map[string]ProviderRequirement),
		WrapperOutputReferences: make(map[string][]OutputReference),
		CheckBlocks:             make(map[string]CheckBlockDef),
		Functions:               make(map[string]FunctionDef),
		ManagedResources:        make(map[string]ResourceDef),
		DataResources:           make(map[string]ResourceDef),
	}

	if err := parseAndEnrichModuleWithOverlay(dir, entries, info, true, overlay); err != nil {
		return nil, err
	}
	rebaseModulePaths(info, dir, dir)
	return info, nil
}

// rebaseModulePaths rewrites every recorded source position from the staging directory back to the
// real module directory, so diagnostics point at the user's files.
//
// The map and slice forms below assign by key and index respectively. Ranging over values and
// mutating the copy would silently discard every position held in a slice.
func rebaseModulePaths(info *ModuleInfo, from, to string) {
	for name, v := range info.Variables {
		v.Pos.Filename = rebasePath(v.Pos.Filename, from, to)
		info.Variables[name] = v
	}
	for name, o := range info.Outputs {
		o.Pos.Filename = rebasePath(o.Pos.Filename, from, to)
		info.Outputs[name] = o
	}
	for name, m := range info.ModuleCalls {
		m.Pos.Filename = rebasePath(m.Pos.Filename, from, to)
		info.ModuleCalls[name] = m
	}
	for i := range info.RemovedBlocks {
		info.RemovedBlocks[i].Pos.Filename = rebasePath(info.RemovedBlocks[i].Pos.Filename, from, to)
	}
	for name, cb := range info.CheckBlocks {
		cb.Pos.Filename = rebasePath(cb.Pos.Filename, from, to)
		info.CheckBlocks[name] = cb
	}
	for i := range info.ImportBlocks {
		info.ImportBlocks[i].Pos.Filename = rebasePath(info.ImportBlocks[i].Pos.Filename, from, to)
	}
	for name, fn := range info.Functions {
		fn.Pos.Filename = rebasePath(fn.Pos.Filename, from, to)
		info.Functions[name] = fn
	}
	for i := range info.EncryptionBlocks {
		info.EncryptionBlocks[i].Pos.Filename = rebasePath(info.EncryptionBlocks[i].Pos.Filename, from, to)
	}
	for name, r := range info.ManagedResources {
		r.Pos.Filename = rebasePath(r.Pos.Filename, from, to)
		info.ManagedResources[name] = r
	}
	for name, r := range info.DataResources {
		r.Pos.Filename = rebasePath(r.Pos.Filename, from, to)
		info.DataResources[name] = r
	}
	for i := range info.MovedBlocks {
		info.MovedBlocks[i].Pos.Filename = rebasePath(info.MovedBlocks[i].Pos.Filename, from, to)
	}
	info.Path = to
}

func rebasePath(filename, from, to string) string {
	if filename == "" {
		return filename
	}
	rel, err := filepath.Rel(from, filename)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filename
	}
	return filepath.Join(to, rel)
}

// Inspect parses and extracts the normalized AST for a Terraform module at the given directory.
func Inspect(dir string) (info *ModuleInfo, err error) {
	return inspectDir(dir, nil)
}

// inspectDir is the shared body of Inspect and InspectSource. overlay, when non-nil, replaces the
// on-disk content of the files it names.
func inspectDir(dir string, overlay map[string][]byte) (info *ModuleInfo, err error) {
	defer recoverParseError(dir, &err)

	fi, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to access module path %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("module path %s is not a directory", dir)
	}

	if len(overlay) > 0 {
		return inspectOverlay(dir, overlay)
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

	info = &ModuleInfo{
		Path:                    dir,
		Variables:               make(map[string]VariableDef),
		Outputs:                 make(map[string]OutputDef),
		ModuleCalls:             make(map[string]ModuleCallDef),
		RequiredProviders:       make(map[string]ProviderRequirement),
		WrapperOutputReferences: make(map[string][]OutputReference),
		CheckBlocks:             make(map[string]CheckBlockDef),
		Functions:               make(map[string]FunctionDef),
		ManagedResources:        make(map[string]ResourceDef),
		DataResources:           make(map[string]ResourceDef),
	}

	if err := parseAndEnrichModule(dir, info, true); err != nil {
		return nil, err
	}

	// The inspection cache is an optimisation, so a write failure must not invalidate the result.
	_ = cacheModule(cachePath, fingerprint, info)
	return info, nil
}

func populateFromTFModule(tfMod *tfconfig.Module, info *ModuleInfo) {
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

	// Managed Resources
	for name, res := range tfMod.ManagedResources {
		prov := res.Provider.Name
		if res.Provider.Alias != "" {
			prov = prov + "." + res.Provider.Alias
		}
		info.ManagedResources[name] = ResourceDef{
			Mode:     "managed",
			Type:     res.Type,
			Name:     res.Name,
			Provider: prov,
			Pos:      res.Pos,
		}
	}

	// Data Resources
	for name, res := range tfMod.DataResources {
		prov := res.Provider.Name
		if res.Provider.Alias != "" {
			prov = prov + "." + res.Provider.Alias
		}
		info.DataResources[name] = ResourceDef{
			Mode:     "data",
			Type:     res.Type,
			Name:     res.Name,
			Provider: prov,
			Pos:      res.Pos,
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

// recoverParseError converts a panic raised while parsing untrusted Terraform source into a normal
// error so a malformed module can never crash the CLI.
func recoverParseError(dir string, err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("panic during AST parsing of module %s: %v", dir, r)
	}
}

type parsedHCLFile struct {
	filePath string
	src      []byte
	file     *hcl.File
}

// enrichFromHCLFiles scans top-level Terraform files for module inputs, nullable settings,
// and output delegations that are not exposed by terraform-config-inspect.
func enrichFromHCLFiles(dir string, info *ModuleInfo) {
	_ = parseAndEnrichModule(dir, info, false)
}

func splitHCLListExpr(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "[")
	trimmed = strings.TrimSuffix(trimmed, "]")
	var items []string
	for _, part := range strings.Split(trimmed, ",") {
		if p := strings.TrimSpace(part); p != "" {
			items = append(items, p)
		}
	}
	return items
}

func parseAndEnrichModule(dir string, info *ModuleInfo, loadTFConfig bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	return parseAndEnrichModuleWithOverlay(dir, entries, info, loadTFConfig, nil)
}

func parseAndEnrichModuleWithOverlay(dir string, entries []os.DirEntry, info *ModuleInfo, loadTFConfig bool, overlay map[string][]byte) error {
	parser := hclparse.NewParser()
	var tfMod *tfconfig.Module
	if loadTFConfig {
		tfMod = tfconfig.NewModule(dir)
	}
	schema := &hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "variable", LabelNames: []string{"name"}},
			{Type: "module", LabelNames: []string{"name"}},
			{Type: "output", LabelNames: []string{"name"}},
			{Type: "removed"},
			{Type: "moved"},
			{Type: "check", LabelNames: []string{"name"}},
			{Type: "import"},
			{Type: "function", LabelNames: []string{"name"}},
			{Type: "encryption"},
			{Type: "resource", LabelNames: []string{"type", "name"}},
			{Type: "data", LabelNames: []string{"type", "name"}},
			{Type: "terraform"},
			{Type: "provider", LabelNames: []string{"name"}},
			{Type: "locals"},
		},
	}

	var parsedFiles []parsedHCLFile
	seenPaths := make(map[string]bool)
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
		cleanPath := filepath.Clean(filePath)
		seenPaths[cleanPath] = true

		var src []byte
		replacement, replaced := overlay[cleanPath]
		if replaced {
			src = replacement
		} else {
			// #nosec G304 -- directory entry read within inspected module
			var readErr error
			src, readErr = os.ReadFile(cleanPath)
			if readErr != nil {
				continue
			}
		}

		var file *hcl.File
		var diags hcl.Diagnostics
		if isJSON {
			file, diags = parser.ParseJSON(src, filePath)
		} else {
			file, diags = parser.ParseHCL(src, filePath)
		}
		if !diags.HasErrors() && loadTFConfig {
			diags = append(diags, tfconfig.LoadModuleFromFile(file, tfMod)...)
		}
		if diags.HasErrors() {
			if loadTFConfig {
				return fmt.Errorf("diagnostics error loading module %s: %s", dir, diags.Error())
			}
			continue
		}
		parsedFiles = append(parsedFiles, parsedHCLFile{filePath: filePath, src: src, file: file})
	}

	for ovPath, ovSrc := range overlay {
		cleanOv := filepath.Clean(ovPath)
		if seenPaths[cleanOv] {
			continue
		}
		if filepath.Dir(cleanOv) != filepath.Clean(dir) {
			continue
		}
		name := filepath.Base(cleanOv)
		isJSON := strings.HasSuffix(name, ".tf.json")
		if !isJSON && !strings.HasSuffix(name, ".tf") {
			continue
		}
		var file *hcl.File
		var diags hcl.Diagnostics
		if isJSON {
			file, diags = parser.ParseJSON(ovSrc, cleanOv)
		} else {
			file, diags = parser.ParseHCL(ovSrc, cleanOv)
		}
		if !diags.HasErrors() {
			diags = append(diags, tfconfig.LoadModuleFromFile(file, tfMod)...)
		}
		if diags.HasErrors() {
			return fmt.Errorf("diagnostics error loading module %s: %s", dir, diags.Error())
		}
		parsedFiles = append(parsedFiles, parsedHCLFile{filePath: cleanOv, src: ovSrc, file: file})
	}

	if loadTFConfig {
		populateFromTFModule(tfMod, info)
	}

	for _, pf := range parsedFiles {
		filePath, src, file := pf.filePath, pf.src, pf.file
		if syntaxBody, ok := file.Body.(*hclsyntax.Body); ok {
			if info.ParsedBodies == nil {
				info.ParsedBodies = make(map[string]*hclsyntax.Body)
			}
			info.ParsedBodies[filePath] = syntaxBody
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
					case "count":
						mc.Count = exprStr
						continue
					case "for_each":
						mc.ForEach = exprStr
						continue
					case "providers", "depends_on":
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
				if info.Outputs == nil {
					info.Outputs = make(map[string]OutputDef)
				}
				outDef := info.Outputs[outName]
				for _, condType := range []string{"precondition", "postcondition"} {
					condContent, _, _ := block.Body.PartialContent(&hcl.BodySchema{
						Blocks: []hcl.BlockHeaderSchema{{Type: condType}},
					})
					if condContent == nil {
						continue
					}
					for _, cc := range condContent.Blocks {
						ccAttrs, _ := cc.Body.JustAttributes()
						condStr := ""
						if cAttr, ok := ccAttrs["condition"]; ok {
							condStr = strings.TrimSpace(string(src[cAttr.Expr.Range().Start.Byte:cAttr.Expr.Range().End.Byte]))
						}
						if condType == "precondition" {
							outDef.Preconditions = append(outDef.Preconditions, condStr)
						} else {
							outDef.Postconditions = append(outDef.Postconditions, condStr)
						}
					}
				}
				if len(outDef.Preconditions) > 0 || len(outDef.Postconditions) > 0 {
					outDef.Name = outName
					info.Outputs[outName] = outDef
				}
				if valAttr, ok := attrs["value"]; ok {
					valueExpr := strings.TrimSpace(string(src[valAttr.Expr.Range().Start.Byte:valAttr.Expr.Range().End.Byte]))
					if out, exists := info.Outputs[outName]; exists {
						out.Value = valueExpr
						info.Outputs[outName] = out
					} else {
						info.Outputs[outName] = OutputDef{Name: outName, Value: valueExpr}
					}
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
							info.WrapperOutputReferences[outName] = append(info.WrapperOutputReferences[outName], OutputReference{
								OutputName:   outName,
								TargetModule: module.Name,
								TargetOutput: output.Name,
							})
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

			case "moved":
				from := ""
				if fromAttr, ok := attrs["from"]; ok {
					from = strings.TrimSpace(string(src[fromAttr.Expr.Range().Start.Byte:fromAttr.Expr.Range().End.Byte]))
				}
				to := ""
				if toAttr, ok := attrs["to"]; ok {
					to = strings.TrimSpace(string(src[toAttr.Expr.Range().Start.Byte:toAttr.Expr.Range().End.Byte]))
				}
				info.MovedBlocks = append(info.MovedBlocks, MovedBlockDef{
					From: from,
					To:   to,
					Pos:  tfconfig.SourcePos{Filename: filePath, Line: block.DefRange.Start.Line},
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
				provider := ""
				if pAttr, ok := attrs["provider"]; ok {
					provider = strings.TrimSpace(string(src[pAttr.Expr.Range().Start.Byte:pAttr.Expr.Range().End.Byte]))
				}
				info.ImportBlocks = append(info.ImportBlocks, ImportBlockDef{
					To:       to,
					ID:       id,
					Provider: provider,
					Pos:      tfconfig.SourcePos{Filename: filePath, Line: block.DefRange.Start.Line},
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

			case "resource", "data":
				// Capture lifecycle flags and provisioner presence for the resource.
				mode := "managed"
				if block.Type == "data" {
					mode = "data"
				}
				key := block.Labels[0] + "." + block.Labels[1]
				if mode == "data" {
					key = "data." + key
				}
				res, exists := info.ManagedResources[key]
				if mode == "data" {
					res, exists = info.DataResources[key]
				}
				if !exists {
					res = ResourceDef{Mode: mode, Type: block.Labels[0], Name: block.Labels[1]}
				}
				// capture count/for_each
				rAttrs, _ := block.Body.JustAttributes()
				if cAttr, ok := rAttrs["count"]; ok {
					res.Count = strings.TrimSpace(string(src[cAttr.Expr.Range().Start.Byte:cAttr.Expr.Range().End.Byte]))
				}
				if fAttr, ok := rAttrs["for_each"]; ok {
					res.ForEach = strings.TrimSpace(string(src[fAttr.Expr.Range().Start.Byte:fAttr.Expr.Range().End.Byte]))
				}
				// lifecycle block
				lcContent, _, _ := block.Body.PartialContent(&hcl.BodySchema{
					Blocks: []hcl.BlockHeaderSchema{{Type: "lifecycle"}},
				})
				if lcContent != nil {
					for _, lc := range lcContent.Blocks {
						lcAttrs, _ := lc.Body.JustAttributes()
						if v, ok := lcAttrs["prevent_destroy"]; ok {
							val := strings.TrimSpace(string(src[v.Expr.Range().Start.Byte:v.Expr.Range().End.Byte]))
							res.Lifecycle.PreventDestroy = (val == "true")
						}
						if v, ok := lcAttrs["create_before_destroy"]; ok {
							val := strings.TrimSpace(string(src[v.Expr.Range().Start.Byte:v.Expr.Range().End.Byte]))
							res.Lifecycle.CreateBeforeDestroy = (val == "true")
						}
						if v, ok := lcAttrs["replace_triggered_by"]; ok {
							res.Lifecycle.ReplaceTriggeredBy = append(res.Lifecycle.ReplaceTriggeredBy, splitHCLListExpr(string(src[v.Expr.Range().Start.Byte:v.Expr.Range().End.Byte]))...)
						}
						if v, ok := lcAttrs["ignore_changes"]; ok {
							res.Lifecycle.IgnoreChanges = append(res.Lifecycle.IgnoreChanges, splitHCLListExpr(string(src[v.Expr.Range().Start.Byte:v.Expr.Range().End.Byte]))...)
						}
					}
				}
				// provisioner blocks
				provContent, _, _ := block.Body.PartialContent(&hcl.BodySchema{
					Blocks: []hcl.BlockHeaderSchema{{Type: "provisioner", LabelNames: []string{"type"}}},
				})
				if provContent != nil {
					for _, pc := range provContent.Blocks {
						when := "apply"
						pcAttrs, _ := pc.Body.JustAttributes()
						if wAttr, ok := pcAttrs["when"]; ok {
							when = strings.Trim(strings.TrimSpace(string(src[wAttr.Expr.Range().Start.Byte:wAttr.Expr.Range().End.Byte])), `"`)
						}
						res.Provisioners = append(res.Provisioners, when)
					}
				}
				// precondition / postcondition blocks (#140)
				for _, condType := range []string{"precondition", "postcondition"} {
					condContent, _, _ := block.Body.PartialContent(&hcl.BodySchema{
						Blocks: []hcl.BlockHeaderSchema{{Type: condType}},
					})
					if condContent == nil {
						continue
					}
					for _, cc := range condContent.Blocks {
						ccAttrs, _ := cc.Body.JustAttributes()
						condStr := ""
						if cAttr, ok := ccAttrs["condition"]; ok {
							condStr = strings.TrimSpace(string(src[cAttr.Expr.Range().Start.Byte:cAttr.Expr.Range().End.Byte]))
						}
						if condType == "precondition" {
							res.Preconditions = append(res.Preconditions, condStr)
						} else {
							res.Postconditions = append(res.Postconditions, condStr)
						}
					}
				}

				// triggers and resource attributes
				blockAttrs, _ := block.Body.JustAttributes()
				for attrName, attr := range blockAttrs {
					valStr := strings.TrimSpace(string(src[attr.Expr.Range().Start.Byte:attr.Expr.Range().End.Byte]))
					switch attrName {
					case "triggers", "triggers_replace", "keepers":
						if res.Triggers == nil {
							res.Triggers = make(map[string]string)
						}
						res.Triggers[attrName] = valStr
					case "count", "for_each", "provider", "depends_on":
					default:
						if res.Attributes == nil {
							res.Attributes = make(map[string]string)
						}
						res.Attributes[attrName] = valStr
					}
				}
				if mode == "data" {
					if info.DataResources == nil {
						info.DataResources = make(map[string]ResourceDef)
					}
					info.DataResources[key] = res
				} else {
					if info.ManagedResources == nil {
						info.ManagedResources = make(map[string]ResourceDef)
					}
					info.ManagedResources[key] = res
				}

			case "terraform":
				bcContent, _, _ := block.Body.PartialContent(&hcl.BodySchema{
					Blocks: []hcl.BlockHeaderSchema{
						{Type: "backend", LabelNames: []string{"type"}},
						{Type: "cloud"},
					},
				})
				if bcContent != nil {
					for _, bc := range bcContent.Blocks {
						bType := bc.Type
						if len(bc.Labels) > 0 {
							bType = bc.Labels[0]
						}
						bcfg := &BackendConfig{Type: bType, Attributes: make(map[string]string)}
						bAttrs, _ := bc.Body.JustAttributes()
						for an, av := range bAttrs {
							bcfg.Attributes[an] = strings.TrimSpace(string(src[av.Expr.Range().Start.Byte:av.Expr.Range().End.Byte]))
						}
						if bc.Type == "cloud" {
							info.Cloud = bcfg
						} else {
							info.Backend = bcfg
						}
					}
				}

			case "provider":
				pName := block.Labels[0]
				if info.ProviderConfigs == nil {
					info.ProviderConfigs = make(map[string]map[string]string)
				}
				pAttrs := make(map[string]string)
				for an, av := range attrs {
					pAttrs[an] = strings.TrimSpace(string(src[av.Expr.Range().Start.Byte:av.Expr.Range().End.Byte]))
				}
				if alias, ok := pAttrs["alias"]; ok {
					pName = pName + "." + strings.Trim(alias, `"'`)
				}
				info.ProviderConfigs[pName] = pAttrs

			case "locals":
				if info.Locals == nil {
					info.Locals = make(map[string]string)
				}
				for ln, la := range attrs {
					info.Locals[ln] = strings.TrimSpace(string(src[la.Expr.Range().Start.Byte:la.Expr.Range().End.Byte]))
				}
			}
		}
	}
	return nil
}
