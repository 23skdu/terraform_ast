package blastradius

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/eval"
	"github.com/23skdu/terraform_ast/pkg/recreation"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
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
	RiskDistribution      map[string]int     `json:"risk_distribution,omitempty"`
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
		RiskDistribution:      make(map[string]int),
	}

	idx := buildAuditIndex(wrapperAudit)
	for _, cs := range callSites {
		impact := evaluateCallSiteImpactIndexed(cs, wrapperMod, wrapperAudit, registry, idx)
		if !impact.CanSafelyUpdate {
			report.CanSafelyUpdateAll = false
		}
		if riskRank(impact.RiskScore) > riskRank(report.HighestRisk) {
			report.HighestRisk = impact.RiskScore
		}
		report.RiskDistribution[impact.RiskScore]++
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
	// Pre-compile output reference regexes once per tainted output.
	outRegexes := make(map[string]*regexp.Regexp)
	for outName := range taintedOutputs {
		outRegexes[outName] = regexp.MustCompile(`\bmodule\.[a-zA-Z0-9_-]+\.` + regexp.QuoteMeta(outName) + `\b`)
	}
	targetVar := taintReport.TargetVariable
	for _, cs := range callSites {
		isTainted := false

		// 1. Consumer explicitly passes the tainted input (exact name or expression referencing var.<target>)
		for inputName, expr := range cs.PassedInputs {
			if inputName == targetVar {
				isTainted = true
				break
			}
			if regexp.MustCompile(`\bvar\.` + regexp.QuoteMeta(targetVar) + `\b`).MatchString(expr) {
				isTainted = true
				break
			}
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
				for outName, re := range outRegexes {
					_ = outName
					if re.MatchString(expr) {
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
	normTarget := filepath.Clean(wrapperIdentifier)
	var candidates []string

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
		name := d.Name()
		if name == "terragrunt.hcl" || strings.HasSuffix(name, ".tf.json") || strings.HasSuffix(name, ".tf") {
			candidates = append(candidates, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	perFile := make([][]CallSite, len(candidates))
	workers := min(8, runtime.GOMAXPROCS(0))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, path := range candidates {
		wg.Add(1)
		go func(idx int, p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			perFile[idx] = discoverFileCallSites(p, normTarget)
		}(i, path)
	}
	wg.Wait()

	var callSites []CallSite
	for _, sites := range perFile {
		callSites = append(callSites, sites...)
	}
	return callSites, nil
}

func discoverFileCallSites(path, normTarget string) []CallSite {
	// #nosec G122, G304 -- directory entries traversed within validated search directory
	src, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil
	}

	base := filepath.Base(path)
	if base == "terragrunt.hcl" {
		if callSite, ok := parseTerragruntCallSite(src, path, normTarget); ok {
			return []CallSite{callSite}
		}
		return nil
	}
	if strings.HasSuffix(base, ".tf.json") {
		return discoverJSONCallSites(src, path, normTarget)
	}

	if !bytes.Contains(src, []byte("module")) {
		return nil
	}

	file, diags := hclsyntax.ParseConfig(src, path, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() || file == nil {
		return nil
	}

	body := file.Body.(*hclsyntax.Body)
	var refs map[string][]string
	var callSites []CallSite

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

		if refs == nil {
			refs = extractModuleReferencesFromBody(body)
		}

		passedInputs := make(map[string]string)
		for attrName, attr := range block.Body.Attributes {
			if attrName == "source" || attrName == "version" || attrName == "count" || attrName == "for_each" || attrName == "providers" || attrName == "depends_on" {
				continue
			}
			passedInputs[attrName] = string(src[attr.Expr.Range().Start.Byte:attr.Expr.Range().End.Byte])
		}

		callSites = append(callSites, CallSite{
			Dir:               filepath.Dir(path),
			File:              path,
			ModuleName:        modName,
			Source:            sourceVal,
			PassedInputs:      passedInputs,
			OutputsReferenced: refs[modName],
		})
	}

	return callSites
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

	var source, blockLabel string
	for _, block := range body.Blocks {
		if block.Type != "terraform" {
			continue
		}
		if len(block.Labels) > 0 && block.Labels[0] != "" {
			blockLabel = block.Labels[0]
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

	dirBase := filepath.Base(filepath.Dir(path))
	modName := dirBase
	if blockLabel != "" {
		modName = blockLabel
	}
	allRefs := extractModuleReferencesFromBody(body)
	var outputs []string
	outputs = append(outputs, allRefs[modName]...)
	outputs = append(outputs, allRefs[dirBase]...)
	outputs = append(outputs, allRefs[filepath.Base(filepath.Clean(source))]...)
	outputs = dedupeSorted(outputs)

	return CallSite{
		Dir:                     filepath.Dir(path),
		File:                    path,
		ModuleName:              modName,
		Source:                  source,
		PassedInputs:            passedInputs,
		OutputsReferenced:       outputs,
		InputAnalysisIncomplete: inputAnalysisIncomplete,
	}, true
}

// terragruntInputKey resolves a Terragrunt `inputs` object key to a static input name.
// It supports bare identifiers, quoted string literals, and template expressions.
func terragruntInputKey(expr hclsyntax.Expression) (string, bool) {
	switch keyExpr := expr.(type) {
	case *hclsyntax.ObjectConsKeyExpr:
		if keyExpr.ForceNonLiteral {
			return "", false
		}
		return terragruntInputKey(keyExpr.Wrapped)
	case *hclsyntax.TemplateWrapExpr:
		return terragruntInputKey(keyExpr.Wrapped)
	case *hclsyntax.TemplateExpr:
		if keyExpr.IsStringLiteral() {
			val, diags := keyExpr.Value(nil)
			if !diags.HasErrors() && val.Type() == cty.String {
				return val.AsString(), true
			}
		}
	case *hclsyntax.LiteralValueExpr:
		if keyExpr.Val.Type() == cty.String {
			return keyExpr.Val.AsString(), true
		}
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
	if sourceHasSlash {
		sepTarget := string(filepath.Separator) + strings.TrimPrefix(cleanTarget, string(filepath.Separator))
		if strings.HasSuffix(resolved, sepTarget) {
			return true
		}
	}
	return false
}

func extractSourceVal(src []byte, expr hcl.Expression) string {
	raw := string(src[expr.Range().Start.Byte:expr.Range().End.Byte])
	return strings.Trim(strings.TrimSpace(raw), `"'`)
}

func extractModuleReferencesFromBody(body *hclsyntax.Body) map[string][]string {
	res := make(map[string][]string)
	seen := make(map[string]map[string]struct{})
	var visit func(*hclsyntax.Body)
	visit = func(b *hclsyntax.Body) {
		for _, attr := range b.Attributes {
			addModuleReferencesWithSeen(res, seen, attr.Expr.Variables())
		}
		for _, block := range b.Blocks {
			visit(block.Body)
		}
	}
	visit(body)
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

type auditIndex struct {
	removedInputs   []string
	missingRequired []string
	removedOutputs  []string
	breakingInputs  map[string]struct{}
	changedAttrs    map[string]struct{}
}

func buildAuditIndex(audit *wrapper.WrapperCompatibilityReport) *auditIndex {
	if audit == nil {
		return nil
	}
	idx := &auditIndex{
		breakingInputs: make(map[string]struct{}),
		changedAttrs:   make(map[string]struct{}),
	}
	for _, issue := range audit.Issues {
		idx.changedAttrs[issue.Target] = struct{}{}
		if issue.Category == "PassedRemovedInput" || (strings.HasPrefix(issue.Category, "Variable") && strings.Contains(issue.Description, "removed")) {
			idx.removedInputs = append(idx.removedInputs, issue.Target)
		}
		if issue.Category == "MissingRequiredInput" || (strings.HasPrefix(issue.Category, "Variable") && strings.Contains(issue.Description, "required")) {
			idx.missingRequired = append(idx.missingRequired, issue.Target)
		}
		if issue.Category == "BrokenOutputDelegation" || strings.HasPrefix(issue.Category, "Output") {
			idx.removedOutputs = append(idx.removedOutputs, issue.Target)
		}
		if (issue.Category == "TypeMismatch" || issue.Category == "VariableNullability" || (strings.HasPrefix(issue.Category, "Variable") && (strings.Contains(issue.Description, "type") || strings.Contains(issue.Description, "incompatible")))) && issue.Severity == diff.SeverityBreaking {
			idx.breakingInputs[issue.Target] = struct{}{}
		}
	}
	if audit.UpstreamDiff != nil {
		for _, bc := range audit.UpstreamDiff.BreakingChanges {
			idx.changedAttrs[bc.Name] = struct{}{}
		}
		for _, w := range audit.UpstreamDiff.Warnings {
			idx.changedAttrs[w.Name] = struct{}{}
		}
		for _, nf := range audit.UpstreamDiff.NewFeatures {
			idx.changedAttrs[nf.Name] = struct{}{}
		}
	}
	return idx
}

func evaluateCallSiteImpactIndexed(
	cs CallSite,
	wrapperMod *ast.ModuleInfo,
	audit *wrapper.WrapperCompatibilityReport,
	registry *recreation.DynamicHazardRegistry,
	idx *auditIndex,
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
	for _, target := range idx.removedInputs {
		if _, passed := cs.PassedInputs[target]; passed {
			impact.RemovedInputsPassed = append(impact.RemovedInputsPassed, target)
			impact.CanSafelyUpdate = false
		}
	}
	// 2. Check for missing required inputs. Attribute to this consumer only when the
	// wrapper actually exposes the input (otherwise the wrapper, not the consumer, is at fault).
	for _, target := range idx.missingRequired {
		if wrapperMod != nil {
			if _, exposed := wrapperMod.Variables[target]; !exposed {
				continue
			}
		}
		if _, passed := cs.PassedInputs[target]; !passed {
			impact.MissingRequiredInputs = append(impact.MissingRequiredInputs, target)
			impact.CanSafelyUpdate = false
		}
	}
	// 3. Check for removed outputs referenced by this caller from wrapper issues
	for _, target := range idx.removedOutputs {
		for _, ref := range cs.OutputsReferenced {
			if ref == target {
				impact.RemovedOutputsReferenced = append(impact.RemovedOutputsReferenced, target)
				impact.CanSafelyUpdate = false
			}
		}
	}
	// 4. Check for other breaking modifications on passed inputs (e.g. type changes or nullability)
	for target := range idx.breakingInputs {
		if _, passed := cs.PassedInputs[target]; passed {
			impact.CanSafelyUpdate = false
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

	lookup := func(name string) (recreation.HazardDescriptor, bool, bool) {
		if registry != nil {
			if desc, ok := registry.Lookup(name); ok {
				return desc, true, true
			}
		}
		if desc, ok := recreation.LookupHazard(audit.TargetModuleName, name); ok {
			return desc, true, false
		}
		return recreation.HazardDescriptor{}, false, false
	}

	for _, inputName := range passedNames {
		desc, ok, fromRegistry := lookup(inputName)
		if !ok {
			continue
		}
		// Evidence gate: only fire a recreation hazard when the attribute is actually part
		// of the upstream diff or confirmed by the dynamic hazard registry. A bare name
		// match with no changed input is downgraded (no hazard, LOW risk).
		_, changed := idx.changedAttrs[inputName]
		evidenceConfirmed := fromRegistry || changed
		if !evidenceConfirmed {
			continue
		}
		impact.RecreationHazards = append(impact.RecreationHazards, fmt.Sprintf("%s (%s)", inputName, desc.Impact))
		impact.CanSafelyUpdate = false
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
