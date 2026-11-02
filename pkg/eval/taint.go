package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// TaintReport represents the dependency and reachability analysis for a variable.
type TaintReport struct {
	TargetVariable             string   `json:"target_variable"`
	IsReachable                bool     `json:"is_reachable"`
	IsDeadCode                 bool     `json:"is_dead_code"`
	TracePath                  []string `json:"trace_path"`
	AffectedResourceAttributes []string `json:"affected_resource_attributes"`
}

// AnalyzeVariableTaint scans all .tf files in dir to trace the dependency path of targetVar.
func AnalyzeVariableTaint(dir string, targetVar string, preParsed ...map[string]*hclsyntax.Body) (*TaintReport, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to access directory: %w", err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("path %s is not a directory", dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	report := &TaintReport{
		TargetVariable: targetVar,
		IsDeadCode:     true,
		TracePath:      []string{"var." + targetVar},
	}

	taintedVars := map[string]struct{}{
		"var." + targetVar: {},
	}
	taintedLocals := make(map[string]struct{})

	// Parse all .tf files
	type fileAST struct {
		name string
		body *hclsyntax.Body
	}
	var files []fileAST
	affectedAttrsMap := make(map[string]struct{})

	if len(preParsed) > 0 && len(preParsed[0]) > 0 {
		for name, body := range preParsed[0] {
			files = append(files, fileAST{name: name, body: body})
		}
	} else {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			isJSON := strings.HasSuffix(entry.Name(), ".tf.json")
			if !strings.HasSuffix(entry.Name(), ".tf") && !isJSON {
				continue
			}
			filePath := filepath.Join(dir, entry.Name())
			// #nosec G304 -- directory entry read within inspected module
			src, readErr := os.ReadFile(filepath.Clean(filePath))
			if readErr != nil {
				continue
			}
			if isJSON {
				var raw map[string]any
				if err := json.Unmarshal(src, &raw); err == nil {
					if locals, ok := raw["locals"].(map[string]any); ok {
						for lname, lval := range locals {
							if s, ok := lval.(string); ok && (strings.Contains(s, "var."+targetVar) || strings.Contains(s, "${var."+targetVar+"}")) {
								taintedLocals["local."+lname] = struct{}{}
								report.TracePath = append(report.TracePath, "local."+lname)
							}
						}
					}
					found := false
					walkJSON(raw, "", targetVar, affectedAttrsMap, &found)
					if found {
						report.IsReachable = true
						report.IsDeadCode = false
					}
				}
				continue
			}
			parsedFile, diags := hclsyntax.ParseConfig(src, entry.Name(), hcl.Pos{Line: 1, Column: 1})
			if diags.HasErrors() || parsedFile == nil {
				continue
			}
			if body, ok := parsedFile.Body.(*hclsyntax.Body); ok {
				files = append(files, fileAST{name: entry.Name(), body: body})
			}
		}
	}

	// Step 1: Trace through locals iteratively until fixpoint
	changed := true
	for changed {
		changed = false
		for _, f := range files {
			for _, block := range f.body.Blocks {
				if block.Type != "locals" {
					continue
				}
				for localName, attr := range block.Body.Attributes {
					if _, alreadyTainted := taintedLocals["local."+localName]; alreadyTainted {
						continue
					}
					for _, v := range attr.Expr.Variables() {
						varStr := traversalToString(v)
						if isTainted(varStr, taintedVars, taintedLocals) {
							taintedLocals["local."+localName] = struct{}{}
							report.TracePath = append(report.TracePath, "local."+localName)
							changed = true
							break
						}
					}
				}
			}
		}
	}

	// Step 2: Trace through child modules, resources, data blocks recursively
	var inspectBody func(prefix string, body *hclsyntax.Body)
	inspectBody = func(prefix string, body *hclsyntax.Body) {
		for attrName, attr := range body.Attributes {
			for _, v := range attr.Expr.Variables() {
				varStr := traversalToString(v)
				if isTainted(varStr, taintedVars, taintedLocals) {
					dest := fmt.Sprintf("%s.%s", prefix, attrName)
					affectedAttrsMap[dest] = struct{}{}
					report.IsReachable = true
					report.IsDeadCode = false
					report.TracePath = append(report.TracePath, dest)
				}
			}
		}
		for _, subBlock := range body.Blocks {
			if subBlock.Type == "dynamic" && len(subBlock.Labels) > 0 {
				subPrefix := prefix + "." + subBlock.Labels[0]
				if forEach, ok := subBlock.Body.Attributes["for_each"]; ok {
					for _, v := range forEach.Expr.Variables() {
						varStr := traversalToString(v)
						if isTainted(varStr, taintedVars, taintedLocals) {
							dest := subPrefix + ".for_each"
							affectedAttrsMap[dest] = struct{}{}
							report.IsReachable = true
							report.IsDeadCode = false
							report.TracePath = append(report.TracePath, dest)
						}
					}
				}
				for _, cb := range subBlock.Body.Blocks {
					if cb.Type == "content" {
						inspectBody(subPrefix, cb.Body)
					}
				}
				continue
			}
			inspectBody(prefix+"."+subBlock.Type, subBlock.Body)
		}
	}

	for _, f := range files {
		for _, block := range f.body.Blocks {
			switch block.Type {
			case "module":
				if len(block.Labels) >= 1 {
					inspectBody("module."+block.Labels[0], block.Body)
				}
			case "resource":
				if len(block.Labels) >= 2 {
					inspectBody(block.Labels[0]+"."+block.Labels[1], block.Body)
				}
			case "data":
				if len(block.Labels) >= 2 {
					inspectBody("data."+block.Labels[0]+"."+block.Labels[1], block.Body)
				}
			case "check":
				if len(block.Labels) >= 1 {
					inspectBody("check."+block.Labels[0], block.Body)
				}
			case "output":
				if len(block.Labels) >= 1 {
					outName := block.Labels[0]
					if valAttr, ok := block.Body.Attributes["value"]; ok {
						for _, v := range valAttr.Expr.Variables() {
							varStr := traversalToString(v)
							if isTainted(varStr, taintedVars, taintedLocals) {
								dest := fmt.Sprintf("output.%s", outName)
								affectedAttrsMap[dest] = struct{}{}
								report.IsReachable = true
								report.IsDeadCode = false
								report.TracePath = append(report.TracePath, dest)
							}
						}
					}
				}
			}
		}
	}

	for attr := range affectedAttrsMap {
		report.AffectedResourceAttributes = append(report.AffectedResourceAttributes, attr)
	}
	sort.Strings(report.AffectedResourceAttributes)

	// If locals reference it but it never reaches a module, resource, or output, it's still alive in locals
	if len(taintedLocals) > 0 {
		report.IsDeadCode = false
	}

	// Deduplicate and sort trace path while keeping root first
	dedupTrace := make([]string, 0, len(report.TracePath))
	seen := make(map[string]struct{})
	for _, p := range report.TracePath {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			dedupTrace = append(dedupTrace, p)
		}
	}
	report.TracePath = dedupTrace

	return report, nil
}

func walkJSON(v any, path, targetVar string, affected map[string]struct{}, found *bool) {
	switch val := v.(type) {
	case map[string]any:
		for k, vv := range val {
			newPath := k
			if path != "" {
				newPath = path + "." + k
			}
			walkJSON(vv, newPath, targetVar, affected, found)
		}
	case []any:
		for i, vv := range val {
			newPath := fmt.Sprintf("%s[%d]", path, i)
			walkJSON(vv, newPath, targetVar, affected, found)
		}
	case string:
		if strings.Contains(val, "var."+targetVar) || strings.Contains(val, "${var."+targetVar+"}") {
			*found = true
			p := path
			if strings.HasPrefix(p, "resource.") {
				p = strings.TrimPrefix(p, "resource.")
			}
			if p != "" {
				affected[p] = struct{}{}
			}
		}
	}
}

func traversalToString(t hcl.Traversal) string {
	var parts []string
	for _, step := range t {
		switch s := step.(type) {
		case hcl.TraverseRoot:
			parts = append(parts, s.Name)
		case hcl.TraverseAttr:
			parts = append(parts, s.Name)
		}
	}
	return strings.Join(parts, ".")
}

func isTainted(ref string, vars, locals map[string]struct{}) bool {
	if _, ok := vars[ref]; ok {
		return true
	}
	for v := range vars {
		if strings.HasPrefix(ref, v+".") {
			return true
		}
	}
	if _, ok := locals[ref]; ok {
		return true
	}
	for l := range locals {
		if strings.HasPrefix(ref, l+".") {
			return true
		}
	}
	return false
}
