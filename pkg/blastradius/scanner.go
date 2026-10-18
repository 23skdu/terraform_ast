package blastradius

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/eval"
	"github.com/23skdu/terraform_ast/pkg/recreation"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// CallSite represents a root module consumer calling the wrapper module.
type CallSite struct {
	Dir                     string            `json:"dir"`
	File                    string            `json:"file"`
	ModuleName              string            `json:"module_name"`
	Source                  string            `json:"source"`
	PassedInputs            map[string]string `json:"passed_inputs"`
	OutputsReferenced       []string          `json:"outputs_referenced"`
	InputAnalysisIncomplete bool              `json:"input_analysis_incomplete,omitempty"`
}

// RootModuleImpact captures the compatibility impact on a specific downstream root module.
type RootModuleImpact struct {
	Dir                      string   `json:"dir"`
	ModuleName               string   `json:"module_name"`
	MissingRequiredInputs    []string `json:"missing_required_inputs"`
	RemovedInputsPassed      []string `json:"removed_inputs_passed"`
	RemovedOutputsReferenced []string `json:"removed_outputs_referenced"`
	RecreationHazards        []string `json:"recreation_hazards"`
	AnalysisIncomplete       bool     `json:"analysis_incomplete,omitempty"`
	RiskScore                string   `json:"risk_score"`
	CanSafelyUpdate          bool     `json:"can_safely_update"`
}

// BlastRadiusReport summarizes the blast radius across all scanned root modules.
type BlastRadiusReport struct {
	SearchDir             string             `json:"search_dir"`
	WrapperIdentifier     string             `json:"wrapper_identifier"`
	TotalCallSitesScanned int                `json:"total_call_sites_scanned"`
	ImpactedRootModules   []RootModuleImpact `json:"impacted_root_modules"`
	HighestRisk           string             `json:"highest_risk"`
	CanSafelyUpdateAll    bool               `json:"can_safely_update_all"`
	Summary               string             `json:"summary"`
}

// ScanDirectory searches searchDir recursively for root modules calling wrapperIdentifier.
func ScanDirectory(
	searchDir string,
	wrapperIdentifier string,
	wrapperMod *ast.ModuleInfo,
	wrapperAudit *wrapper.WrapperCompatibilityReport,
) (*BlastRadiusReport, error) {
	return ScanDirectoryWithRegistry(searchDir, wrapperIdentifier, wrapperMod, wrapperAudit, nil)
}

// ScanDirectoryWithRegistry searches searchDir recursively with dynamic hazard registry support.
func ScanDirectoryWithRegistry(
	searchDir string,
	wrapperIdentifier string,
	wrapperMod *ast.ModuleInfo,
	wrapperAudit *wrapper.WrapperCompatibilityReport,
	registry *recreation.DynamicHazardRegistry,
) (*BlastRadiusReport, error) {
	return ScanDirectoryWithTaint(searchDir, wrapperIdentifier, wrapperMod, wrapperAudit, registry, nil)
}

// ScanDirectoryWithTaint searches searchDir recursively with dynamic hazard registry and cross-module taint reachability.
func ScanDirectoryWithTaint(
	searchDir string,
	wrapperIdentifier string,
	wrapperMod *ast.ModuleInfo,
	wrapperAudit *wrapper.WrapperCompatibilityReport,
	registry *recreation.DynamicHazardRegistry,
	taintReport *eval.TaintReport,
) (*BlastRadiusReport, error) {
	cleanSearchDir := filepath.Clean(searchDir)
	fi, err := os.Stat(cleanSearchDir)
	if err != nil {
		return nil, fmt.Errorf("failed to access search directory: %w", err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("search path %s is not a directory", searchDir)
	}

	callSites, err := discoverCallSites(cleanSearchDir, wrapperIdentifier)
	if err != nil {
		return nil, err
	}

	totalDiscovered := len(callSites)
	if taintReport != nil {
		callSites = FilterTaintedCallSites(callSites, taintReport)
	}

	report := &BlastRadiusReport{
		SearchDir:             searchDir,
		WrapperIdentifier:     wrapperIdentifier,
		TotalCallSitesScanned: len(callSites),
		CanSafelyUpdateAll:    true,
		HighestRisk:           "NONE",
	}

	for _, cs := range callSites {
		impact := evaluateCallSiteImpact(cs, wrapperMod, wrapperAudit, registry)
		if !impact.CanSafelyUpdate {
			report.CanSafelyUpdateAll = false
		}
		if riskRank(impact.RiskScore) > riskRank(report.HighestRisk) {
			report.HighestRisk = impact.RiskScore
		}
		report.ImpactedRootModules = append(report.ImpactedRootModules, impact)
	}

	if len(report.ImpactedRootModules) == 0 {
		if taintReport != nil && totalDiscovered > 0 {
			report.Summary = fmt.Sprintf("Scanned %d downstream call sites; 0 consumers are reachable from tainted variable var.%s in %s", totalDiscovered, taintReport.TargetVariable, searchDir)
		} else {
			report.Summary = fmt.Sprintf("No downstream consumer modules found calling '%s' in %s", wrapperIdentifier, searchDir)
		}
	} else if report.CanSafelyUpdateAll {
		if taintReport != nil {
			report.Summary = fmt.Sprintf("Scanned %d downstream call sites (filtered to %d tainted by var.%s); all tainted consumers can safely update", totalDiscovered, len(callSites), taintReport.TargetVariable)
		} else {
			report.Summary = fmt.Sprintf("Scanned %d downstream call sites; all consumer modules can safely update without breaking changes", len(callSites))
		}
	} else {
		if taintReport != nil {
			report.Summary = fmt.Sprintf("Scanned %d downstream call sites (filtered to %d tainted by var.%s); detected breaking impacts with highest risk %s", totalDiscovered, len(callSites), taintReport.TargetVariable, report.HighestRisk)
		} else {
			report.Summary = fmt.Sprintf("Scanned %d downstream call sites; detected breaking impacts with highest risk %s", len(callSites), report.HighestRisk)
		}
	}

	return report, nil
}

// FilterTaintedCallSites filters call sites to only those downstream consumers that
// either pass the tainted input variable or reference an output tainted by it.
func FilterTaintedCallSites(callSites []CallSite, taintReport *eval.TaintReport) []CallSite {
	if taintReport == nil {
		return callSites
	}

	taintedOutputs := make(map[string]struct{})
	for _, attr := range taintReport.AffectedResourceAttributes {
		if strings.HasPrefix(attr, "output.") {
			taintedOutputs[strings.TrimPrefix(attr, "output.")] = struct{}{}
		}
	}
	for _, path := range taintReport.TracePath {
		if strings.HasPrefix(path, "output.") {
			taintedOutputs[strings.TrimPrefix(path, "output.")] = struct{}{}
		}
	}

	var filtered []CallSite
	for _, cs := range callSites {
		isTainted := false

		// 1. Consumer explicitly passes the tainted input
		if _, passed := cs.PassedInputs[taintReport.TargetVariable]; passed {
			isTainted = true
		}

		// 2. Consumer references an output tainted by the target variable
		if !isTainted {
			for _, ref := range cs.OutputsReferenced {
				if _, ok := taintedOutputs[ref]; ok {
					isTainted = true
					break
				}
			}
		}

		// 3. Consumer's passed input expression references a tainted output (e.g. module.wrapper.tainted_output)
		if !isTainted {
			for _, expr := range cs.PassedInputs {
				for outName := range taintedOutputs {
					if strings.Contains(expr, "."+outName) {
						isTainted = true
						break
					}
				}
				if isTainted {
					break
				}
			}
		}

		if isTainted {
			filtered = append(filtered, cs)
		}
	}

	return filtered
}

func discoverCallSites(searchDir, wrapperIdentifier string) ([]CallSite, error) {
	var callSites []CallSite
	normTarget := filepath.Clean(wrapperIdentifier)

	err := filepath.WalkDir(searchDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if path == searchDir {
				return err
			}
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == ".terraform" {
				return filepath.SkipDir
			}
			return nil
		}
		isTerragrunt := d.Name() == "terragrunt.hcl"
		isJSON := strings.HasSuffix(d.Name(), ".tf.json")
		if !isTerragrunt && !isJSON && !strings.HasSuffix(d.Name(), ".tf") {
			return nil
		}

		// #nosec G122, G304 -- directory entries traversed within validated search directory
		src, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return nil
		}

		if isTerragrunt {
			if callSite, ok := parseTerragruntCallSite(src, path, normTarget); ok {
				callSites = append(callSites, callSite)
			}
			return nil
		}
		if isJSON {
			callSites = append(callSites, discoverJSONCallSites(src, path, normTarget)...)
			return nil
		}

		file, diags := hclsyntax.ParseConfig(src, path, hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() || file == nil {
			return nil
		}

		if body, ok := file.Body.(*hclsyntax.Body); ok {
			// Extract all module.<name>.<output> references in this file
			refs := extractModuleReferences(src)

			for _, block := range body.Blocks {
				if block.Type != "module" || len(block.Labels) == 0 {
					continue
				}
				modName := block.Labels[0]

				srcAttr, exists := block.Body.Attributes["source"]
				if !exists {
					continue
				}

				sourceVal := extractSourceVal(src, srcAttr.Expr)
				if !isMatchingSource(path, sourceVal, normTarget) {
					continue
				}

				passedInputs := make(map[string]string)
				for attrName, attr := range block.Body.Attributes {
					if attrName == "source" || attrName == "version" || attrName == "count" || attrName == "for_each" || attrName == "providers" || attrName == "depends_on" {
						continue
					}
					passedInputs[attrName] = string(src[attr.Expr.Range().Start.Byte:attr.Expr.Range().End.Byte])
				}

				cs := CallSite{
					Dir:               filepath.Dir(path),
					File:              path,
					ModuleName:        modName,
					Source:            sourceVal,
					PassedInputs:      passedInputs,
					OutputsReferenced: refs[modName],
				}
				callSites = append(callSites, cs)
			}
		}

		return nil
	})

	return callSites, err
}

// discoverJSONCallSites extracts wrapper call sites from Terraform JSON syntax. Files that cannot
// be parsed or do not match the expected block schema simply contribute no call sites.
func discoverJSONCallSites(src []byte, path, targetIdentifier string) []CallSite {
	file, diags := hclparse.NewParser().ParseJSON(src, path)
	if diags.HasErrors() || file == nil {
		return nil
	}
	schema := &hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "module", LabelNames: []string{"name"}},
			{Type: "output", LabelNames: []string{"name"}},
			{Type: "resource", LabelNames: []string{"type", "name"}},
			{Type: "data", LabelNames: []string{"type", "name"}},
			{Type: "locals"},
		},
	}
	content, _, diags := file.Body.PartialContent(schema)
	if diags.HasErrors() || content == nil {
		return nil
	}
	refs := make(map[string][]string)
	for _, block := range content.Blocks {
		attrs, _ := block.Body.JustAttributes()
		for _, attr := range attrs {
			addModuleReferences(refs, attr.Expr.Variables())
		}
	}

	var callSites []CallSite
	for _, block := range content.Blocks {
		if block.Type != "module" || len(block.Labels) == 0 {
			continue
		}
		attrs, _ := block.Body.JustAttributes()
		sourceAttr, exists := attrs["source"]
		if !exists {
			continue
		}
		source := extractSourceVal(src, sourceAttr.Expr)
		if !isMatchingSource(path, source, targetIdentifier) {
			continue
		}

		passedInputs := make(map[string]string)
		for name, attr := range attrs {
			switch name {
			case "source", "version", "count", "for_each", "providers", "depends_on":
				continue
			default:
				passedInputs[name] = strings.TrimSpace(string(src[attr.Expr.Range().Start.Byte:attr.Expr.Range().End.Byte]))
			}
		}
		callSites = append(callSites, CallSite{
			Dir:               filepath.Dir(path),
			File:              path,
			ModuleName:        block.Labels[0],
			Source:            source,
			PassedInputs:      passedInputs,
			OutputsReferenced: refs[block.Labels[0]],
		})
	}
	return callSites
}

// terragruntEnv is the environment the Terragrunt built-ins read. It defaults to the process
// environment and can be replaced so tests are hermetic.
var terragruntEnv map[string]string

func parseTerragruntCallSite(src []byte, path, targetIdentifier string) (CallSite, bool) {
	file, diags := hclsyntax.ParseConfig(src, path, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() || file == nil {
		return CallSite{}, false
	}
	// hclsyntax.ParseConfig always returns an *hclsyntax.Body for native syntax.
	body := file.Body.(*hclsyntax.Body)

	var source string
	for _, block := range body.Blocks {
		if block.Type != "terraform" {
			continue
		}
		if sourceAttr, exists := block.Body.Attributes["source"]; exists {
			source = extractSourceVal(src, sourceAttr.Expr)
			break
		}
	}
	if source == "" || !isMatchingSource(path, source, targetIdentifier) {
		return CallSite{}, false
	}

	passedInputs := make(map[string]string)
	inputAnalysisIncomplete := false
	if inputsAttr, exists := body.Attributes["inputs"]; exists {
		if inputs, ok := inputsAttr.Expr.(*hclsyntax.ObjectConsExpr); ok {
			for _, item := range inputs.Items {
				key, ok := terragruntInputKey(item.KeyExpr)
				if !ok {
					inputAnalysisIncomplete = true
					continue
				}
				if key != "" {
					passedInputs[key] = strings.TrimSpace(string(src[item.ValueExpr.Range().Start.Byte:item.ValueExpr.Range().End.Byte]))
				}
			}
		} else {
			// A dynamic `inputs` expression is evaluated against the config's own locals and the
			// Terragrunt built-ins, following its include chain. This used to be evaluated against
			// BuildEvalContext(nil, nil), which left `local` empty and implemented none of the
			// Terragrunt functions, so the canonical `merge(local.defaults, {...})` idiom always
			// failed and every realistic Terragrunt consumer was reported as fully unanalysed.
			resolved, err := eval.ReadTerragruntInputsWithContext(body, src, path, eval.TerragruntOptions{
				Env: terragruntEnv,
			})
			if err != nil {
				inputAnalysisIncomplete = true
			}
			for k, v := range resolved {
				passedInputs[k] = v
			}
		}
	}

	return CallSite{
		Dir:                     filepath.Dir(path),
		File:                    path,
		ModuleName:              filepath.Base(filepath.Dir(path)),
		Source:                  source,
		PassedInputs:            passedInputs,
		OutputsReferenced:       extractModuleReferences(src)[filepath.Base(filepath.Dir(path))],
		InputAnalysisIncomplete: inputAnalysisIncomplete,
	}, true
}

// terragruntInputKey resolves a Terragrunt `inputs` object key to a static input name.
//
// HCL always wraps object keys in an ObjectConsKeyExpr; bare identifiers parse as a scope
// traversal, while quoted keys and parenthesised expressions are reported as unresolvable so the
// caller can flag the inputs as dynamically composed.
func terragruntInputKey(expr hclsyntax.Expression) (string, bool) {
	switch keyExpr := expr.(type) {
	case *hclsyntax.ObjectConsKeyExpr:
		if keyExpr.ForceNonLiteral {
			return "", false
		}
		return terragruntInputKey(keyExpr.Wrapped)
	case *hclsyntax.ScopeTraversalExpr:
		if len(keyExpr.Traversal) == 1 {
			if root, ok := keyExpr.Traversal[0].(hcl.TraverseRoot); ok {
				return root.Name, true
			}
		}
	}
	return "", false
}

func isMatchingSource(callerFilePath, sourceVal, targetIdentifier string) bool {
	cleanSource := filepath.Clean(sourceVal)
	cleanTarget := filepath.Clean(targetIdentifier)
	if cleanSource == cleanTarget || sourceVal == targetIdentifier {
		return true
	}
	targetHasSlash := strings.Contains(targetIdentifier, "/") || strings.Contains(targetIdentifier, string(filepath.Separator))
	sourceHasSlash := strings.Contains(sourceVal, "/") || strings.Contains(sourceVal, string(filepath.Separator))

	// If target has no path separators (a bare module name like "vpc" or "gke"), allow matching against source basename.
	if !targetHasSlash {
		return filepath.Base(cleanSource) == cleanTarget
	}

	// If target has slashes, require resolved path equality.
	callerDir := filepath.Dir(callerFilePath)
	resolved := filepath.Clean(filepath.Join(callerDir, sourceVal))

	if resolved == cleanTarget {
		return true
	}
	if filepath.IsAbs(cleanTarget) {
		absResolved, err := filepath.Abs(resolved)
		if err == nil && absResolved == cleanTarget {
			return true
		}
	}
	if filepath.IsAbs(callerDir) && !filepath.IsAbs(cleanTarget) {
		absTarget, err := filepath.Abs(cleanTarget)
		if err == nil && resolved == absTarget {
			return true
		}
	}
	if sourceHasSlash && strings.HasSuffix(resolved, cleanTarget) {
		return true
	}
	return false
}

func extractSourceVal(src []byte, expr hcl.Expression) string {
	raw := string(src[expr.Range().Start.Byte:expr.Range().End.Byte])
	return strings.Trim(strings.TrimSpace(raw), `"'`)
}

func extractModuleReferences(src []byte) map[string][]string {
	res := make(map[string][]string)
	file, diags := hclsyntax.ParseConfig(src, "references.tf", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() || file == nil {
		return res
	}

	seen := make(map[string]map[string]struct{})
	var visit func(*hclsyntax.Body)
	visit = func(body *hclsyntax.Body) {
		for _, attr := range body.Attributes {
			addModuleReferencesWithSeen(res, seen, attr.Expr.Variables())
		}
		for _, block := range body.Blocks {
			visit(block.Body)
		}
	}
	visit(file.Body.(*hclsyntax.Body))
	for moduleName := range res {
		sort.Strings(res[moduleName])
	}
	return res
}

func addModuleReferences(refs map[string][]string, traversals []hcl.Traversal) {
	seen := make(map[string]map[string]struct{})
	for name, outputs := range refs {
		if seen[name] == nil {
			seen[name] = make(map[string]struct{})
		}
		for _, output := range outputs {
			seen[name][output] = struct{}{}
		}
	}
	addModuleReferencesWithSeen(refs, seen, traversals)
}

func addModuleReferencesWithSeen(refs map[string][]string, seen map[string]map[string]struct{}, traversals []hcl.Traversal) {
	for _, traversal := range traversals {
		if len(traversal) < 3 {
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
			if seen[module.Name] == nil {
				seen[module.Name] = make(map[string]struct{})
			}
			if _, exists := seen[module.Name][output.Name]; !exists {
				seen[module.Name][output.Name] = struct{}{}
				refs[module.Name] = append(refs[module.Name], output.Name)
			}
			break
		}
	}
}

func evaluateCallSiteImpact(
	cs CallSite,
	wrapperMod *ast.ModuleInfo,
	audit *wrapper.WrapperCompatibilityReport,
	registry *recreation.DynamicHazardRegistry,
) RootModuleImpact {
	impact := RootModuleImpact{
		Dir:                cs.Dir,
		ModuleName:         cs.ModuleName,
		RiskScore:          "LOW",
		CanSafelyUpdate:    true,
		AnalysisIncomplete: cs.InputAnalysisIncomplete,
	}
	if cs.InputAnalysisIncomplete {
		impact.CanSafelyUpdate = false
	}

	if audit == nil {
		if cs.InputAnalysisIncomplete {
			impact.RiskScore = "MEDIUM"
		}
		return impact
	}

	// 1. Check for removed inputs passed by this caller
	for _, issue := range audit.Issues {
		if issue.Category == "PassedRemovedInput" || (strings.HasPrefix(issue.Category, "Variable") && strings.Contains(issue.Description, "removed")) {
			if _, passed := cs.PassedInputs[issue.Target]; passed {
				impact.RemovedInputsPassed = append(impact.RemovedInputsPassed, issue.Target)
				impact.CanSafelyUpdate = false
			}
		}
		// 2. Check for missing required inputs
		if issue.Category == "MissingRequiredInput" || (strings.HasPrefix(issue.Category, "Variable") && strings.Contains(issue.Description, "required")) {
			if _, passed := cs.PassedInputs[issue.Target]; !passed {
				impact.MissingRequiredInputs = append(impact.MissingRequiredInputs, issue.Target)
				impact.CanSafelyUpdate = false
			}
		}
		// 3. Check for removed outputs referenced by this caller from wrapper issues
		if issue.Category == "BrokenOutputDelegation" || strings.HasPrefix(issue.Category, "Output") {
			for _, ref := range cs.OutputsReferenced {
				if ref == issue.Target {
					impact.RemovedOutputsReferenced = append(impact.RemovedOutputsReferenced, issue.Target)
					impact.CanSafelyUpdate = false
				}
			}
		}
		// 4. Check for other breaking modifications on passed inputs (e.g. type changes or nullability)
		if (issue.Category == "TypeMismatch" || issue.Category == "VariableNullability" || (strings.HasPrefix(issue.Category, "Variable") && (strings.Contains(issue.Description, "type") || strings.Contains(issue.Description, "incompatible")))) && issue.Severity == diff.SeverityBreaking {
			if _, passed := cs.PassedInputs[issue.Target]; passed {
				impact.CanSafelyUpdate = false
			}
		}
	}

	// Check if caller references outputs that the wrapper itself no longer declares
	if wrapperMod != nil && len(wrapperMod.Outputs) > 0 {
		for _, ref := range cs.OutputsReferenced {
			if _, exists := wrapperMod.Outputs[ref]; !exists {
				impact.RemovedOutputsReferenced = append(impact.RemovedOutputsReferenced, ref)
				impact.CanSafelyUpdate = false
			}
		}
	}

	// 5. Check for state recreation hazards in passed inputs in sorted order
	var passedNames []string
	for inputName := range cs.PassedInputs {
		passedNames = append(passedNames, inputName)
	}
	sort.Strings(passedNames)

	lookup := func(name string) (recreation.HazardDescriptor, bool) {
		if registry != nil {
			if desc, ok := registry.Lookup(name); ok {
				return desc, true
			}
		}
		return recreation.LookupHazard(audit.TargetModuleName, name)
	}

	for _, inputName := range passedNames {
		if desc, ok := lookup(inputName); ok {
			impact.RecreationHazards = append(impact.RecreationHazards, fmt.Sprintf("%s (%s)", inputName, desc.Impact))
			impact.CanSafelyUpdate = false
		}
	}
	impact.RecreationHazards = dedupeSorted(impact.RecreationHazards)
	impact.RemovedOutputsReferenced = dedupeSorted(impact.RemovedOutputsReferenced)
	impact.MissingRequiredInputs = dedupeSorted(impact.MissingRequiredInputs)
	impact.RemovedInputsPassed = dedupeSorted(impact.RemovedInputsPassed)

	// Compute RiskScore
	if len(impact.RecreationHazards) > 0 || len(impact.RemovedOutputsReferenced) > 0 {
		impact.RiskScore = "CRITICAL"
	} else if len(impact.MissingRequiredInputs) > 0 || len(impact.RemovedInputsPassed) > 0 {
		impact.RiskScore = "HIGH"
	} else if !impact.CanSafelyUpdate {
		impact.RiskScore = "MEDIUM"
	} else {
		// Incomplete analysis clears CanSafelyUpdate above, so it is scored here as MEDIUM.
		impact.RiskScore = "LOW"
	}

	return impact
}

func dedupeSorted(s []string) []string {
	if len(s) == 0 {
		return s
	}
	sort.Strings(s)
	res := make([]string, 0, len(s))
	for _, v := range s {
		if len(res) == 0 || res[len(res)-1] != v {
			res = append(res, v)
		}
	}
	return res
}

func riskRank(r string) int {
	switch r {
	case "CRITICAL":
		return 4
	case "HIGH":
		return 3
	case "MEDIUM":
		return 2
	case "LOW":
		return 1
	default:
		return 0
	}
}

// RenderText formats the blast radius report for terminal output.
func (r *BlastRadiusReport) RenderText() string {
	if r == nil {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "BLAST-RADIUS DOWNSTREAM CONSUMER ANALYSIS\n")
	fmt.Fprintf(&sb, "  Search Directory:  %s\n", r.SearchDir)
	fmt.Fprintf(&sb, "  Wrapper:           %s\n", r.WrapperIdentifier)
	fmt.Fprintf(&sb, "  Call Sites Scanned: %d\n", r.TotalCallSitesScanned)
	fmt.Fprintf(&sb, "  Highest Risk:      %s\n", r.HighestRisk)
	fmt.Fprintf(&sb, "  Can Safely Update: %t\n", r.CanSafelyUpdateAll)
	fmt.Fprintf(&sb, "  %s\n", r.Summary)
	for _, m := range r.ImpactedRootModules {
		fmt.Fprintf(&sb, "  • [%s] %s (%s) safe_to_update=%t", m.RiskScore, m.ModuleName, m.Dir, m.CanSafelyUpdate)
		var issues []string
		if len(m.RemovedInputsPassed) > 0 {
			issues = append(issues, fmt.Sprintf("removed inputs passed: %v", m.RemovedInputsPassed))
		}
		if len(m.MissingRequiredInputs) > 0 {
			issues = append(issues, fmt.Sprintf("missing required inputs: %v", m.MissingRequiredInputs))
		}
		if len(m.RemovedOutputsReferenced) > 0 {
			issues = append(issues, fmt.Sprintf("removed outputs used: %v", m.RemovedOutputsReferenced))
		}
		if len(m.RecreationHazards) > 0 {
			issues = append(issues, fmt.Sprintf("recreation hazards: %v", m.RecreationHazards))
		}
		if m.AnalysisIncomplete {
			issues = append(issues, "some dynamic inputs could not be analyzed")
		}
		if len(issues) > 0 {
			sb.WriteString(": ")
			sb.WriteString(strings.Join(issues, "; "))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

// RenderJSON serializes the blast radius report to formatted JSON bytes. Every field of the
// report graph is a JSON-safe primitive or slice, so encoding cannot fail.
func (r *BlastRadiusReport) RenderJSON() []byte {
	encoded, _ := json.MarshalIndent(r, "", "  ")
	return encoded
}

// RenderMarkdown formats the blast radius report into human-readable Markdown.
func (r *BlastRadiusReport) RenderMarkdown() string {
	var sb strings.Builder
	sb.WriteString("# Blast-Radius Downstream Consumer Analysis\n\n")
	fmt.Fprintf(&sb, "- **Search Directory**: `%s`\n", r.SearchDir)
	fmt.Fprintf(&sb, "- **Wrapper Identifier**: `%s`\n", r.WrapperIdentifier)
	fmt.Fprintf(&sb, "- **Total Call Sites Scanned**: `%d`\n", r.TotalCallSitesScanned)
	fmt.Fprintf(&sb, "- **Highest Risk Detected**: `%s`\n", r.HighestRisk)
	fmt.Fprintf(&sb, "- **Can Safely Update All**: `%t`\n\n", r.CanSafelyUpdateAll)
	fmt.Fprintf(&sb, "> %s\n\n", r.Summary)

	if len(r.ImpactedRootModules) > 0 {
		sb.WriteString("### Consumer Root Modules Impact Breakdown\n\n")
		sb.WriteString("| Directory | Module Call | Risk Score | Safe to Update | Issues Detected |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
		for _, m := range r.ImpactedRootModules {
			var issues []string
			if len(m.RemovedInputsPassed) > 0 {
				issues = append(issues, fmt.Sprintf("Removed inputs passed: %v", m.RemovedInputsPassed))
			}
			if len(m.MissingRequiredInputs) > 0 {
				issues = append(issues, fmt.Sprintf("Missing required inputs: %v", m.MissingRequiredInputs))
			}
			if len(m.RemovedOutputsReferenced) > 0 {
				issues = append(issues, fmt.Sprintf("Removed outputs used: %v", m.RemovedOutputsReferenced))
			}
			if len(m.RecreationHazards) > 0 {
				issues = append(issues, fmt.Sprintf("Recreation hazards: %v", m.RecreationHazards))
			}
			if m.AnalysisIncomplete {
				issues = append(issues, "Some dynamic inputs could not be analyzed")
			}
			if len(issues) == 0 {
				issues = append(issues, "None")
			}
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | `%s` | `%t` | %s |\n",
				m.Dir, m.ModuleName, m.RiskScore, m.CanSafelyUpdate, strings.Join(issues, "; ")))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
