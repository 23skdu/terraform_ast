package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/blastradius"
	"github.com/23skdu/terraform_ast/pkg/codemod"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/gitops"
	"github.com/23skdu/terraform_ast/pkg/planner"
	"github.com/23skdu/terraform_ast/pkg/policy"
	"github.com/23skdu/terraform_ast/pkg/recreation"
	"github.com/23skdu/terraform_ast/pkg/registry"
	"github.com/23skdu/terraform_ast/pkg/report"
	"github.com/23skdu/terraform_ast/pkg/semverutil"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
	"golang.org/x/sync/errgroup"
)

var (
	osExit            = os.Exit
	mkdirTemp         = os.MkdirTemp
	newRegistryClient = func() *registry.Client {
		return registry.NewClient(nil)
	}
)

func main() {
	exitCode := Execute(os.Args[1:], os.Stdout, os.Stderr)
	osExit(exitCode)
}

// Execute runs the CLI application with given arguments and streams.
func Execute(args []string, stdout, stderr io.Writer) int {
	// Repositories and extracted versions are owned by the shared gitops cache; release them
	// when the run finishes so temporary directories never outlive the process.
	defer gitops.GlobalCache.Clear()

	fs := flag.NewFlagSet("tfastdiff", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		wrapperPath        string
		upstreamPath       string
		oldPath            string
		newPath            string
		oldVersion         string
		newVersion         string
		format             string
		outputFile         string
		blastRadiusDir     string
		checkRecreation    bool
		enforceExit1       bool
		policyPath         string
		fixPreview         bool
		fixMode            bool
		upgradePath        bool
		providerSchemaPath string
		cacheDir           string
	)

	fs.StringVar(&wrapperPath, "wrapper", "", "Path to the Terraform module wrapper directory")
	fs.StringVar(&upstreamPath, "upstream", "", "Path, Git URL, or Registry identifier of the upstream Terraform module")
	fs.StringVar(&oldPath, "old", "", "Path, Git URL, or Registry identifier to the current/old upstream module")
	fs.StringVar(&newPath, "new", "", "Path, Git URL, or Registry identifier to the target/new upstream module")
	fs.StringVar(&oldVersion, "old-version", "", "Version or Git ref for the old upstream module")
	fs.StringVar(&newVersion, "new-version", "", "Version or Git ref for the new upstream module")
	fs.StringVar(&format, "format", "text", "Output format: text, markdown, json, sarif")
	fs.StringVar(&outputFile, "output", "", "Optional file path to write the output report")
	fs.StringVar(&blastRadiusDir, "blast-radius", "", "Optional root directory to scan for downstream module consumers")
	fs.BoolVar(&checkRecreation, "check-recreation", true, "Check for resource recreation & state destruction hazards")
	fs.BoolVar(&enforceExit1, "exit-code", false, "Exit with code 1 if breaking changes are detected")
	fs.StringVar(&policyPath, "policy", "", "Optional JSON policy file for CI enforcement")
	fs.BoolVar(&fixPreview, "fix-preview", false, "Preview a patch removing wrapper arguments no longer accepted upstream (never edits files)")
	fs.BoolVar(&fixMode, "fix", false, "Apply fixes in-place to remove wrapper arguments no longer accepted upstream")
	fs.BoolVar(&upgradePath, "upgrade-path", false, "Analyze each intermediate semantic version between --old-version and --new-version")
	fs.StringVar(&providerSchemaPath, "provider-schema", "", "Optional path to terraform providers schema -json file for dynamic ForceNew recreation detection")
	fs.StringVar(&cacheDir, "cache-dir", "", "Optional path to directory for persistent L2 AST caching")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if fixMode && fixPreview {
		_, _ = fmt.Fprintln(stderr, "Error: Cannot specify both --fix and --fix-preview")
		return 2
	}

	if cacheDir != "" {
		ast.SetPersistentCacheDir(cacheDir)
	}

	var dynamicRegistry *recreation.DynamicHazardRegistry
	if providerSchemaPath != "" {
		ps, err := recreation.LoadProviderSchema(providerSchemaPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error loading provider schema %s: %v\n", providerSchemaPath, err)
			return 2
		}
		dynamicRegistry = recreation.NewDynamicHazardRegistry(ps)
	}

	format = strings.ToLower(strings.TrimSpace(format))
	var policyConfig *policy.Policy
	if policyPath != "" {
		var err error
		policyConfig, err = policy.Load(policyPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error loading policy %s: %v\n", policyPath, err)
			return 2
		}
	}

	// Mode 1: Wrapper Audit Mode
	if wrapperPath != "" {
		if upgradePath {
			_, _ = fmt.Fprintln(stderr, "Error: --upgrade-path is only available in direct comparison mode")
			return 2
		}
		return runWrapperMode(wrapperPath, upstreamPath, oldVersion, newVersion, format, outputFile, blastRadiusDir, checkRecreation, enforceExit1, fixPreview, fixMode, policyConfig, dynamicRegistry, stdout, stderr)
	}

	// Mode 2: Direct Upstream Comparison Mode
	if oldPath != "" || newPath != "" {
		if fixPreview {
			_, _ = fmt.Fprintln(stderr, "Error: --fix-preview is only available in wrapper audit mode")
			return 2
		}
		if fixMode {
			_, _ = fmt.Fprintln(stderr, "Error: --fix is only available in wrapper audit mode")
			return 2
		}
		return runCompareMode(oldPath, newPath, oldVersion, newVersion, format, outputFile, checkRecreation, enforceExit1, upgradePath, policyConfig, dynamicRegistry, stdout, stderr)
	}

	_, _ = fmt.Fprintln(stderr, "Error: Must specify either --wrapper <dir> OR --old <path> and --new <path>")
	fs.Usage()
	return 2
}

func runWrapperMode(wrapperPath, upstreamPath, oldVersion, newVersion, format, outputFile, blastRadiusDir string, checkRecreation, enforceExit1, fixPreview, fixMode bool, policyConfig *policy.Policy, dynamicRegistry *recreation.DynamicHazardRegistry, stdout, stderr io.Writer) int {
	wInfo, err := ast.Inspect(wrapperPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error inspecting wrapper module %s: %v\n", wrapperPath, err)
		return 1
	}

	// If upstream path not explicitly passed, attempt to deduce from wrapper module call
	if upstreamPath == "" {
		for _, mc := range wInfo.ModuleCalls {
			if mc.Source != "" {
				upstreamPath = mc.Source
				break
			}
		}
	}

	if upstreamPath == "" {
		_, _ = fmt.Fprintln(stderr, "Error: upstream module path not specified and could not be deduced from wrapper")
		return 1
	}

	targetDir := upstreamPath
	var cleanupDirs []string
	defer func() {
		for _, d := range cleanupDirs {
			_ = os.RemoveAll(d)
		}
	}()

	if registry.IsRegistrySource(upstreamPath) {
		tmpTarget, err := mkdirTemp("", "tf_registry_*")
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error creating temp dir: %v\n", err)
			return 1
		}
		cleanupDirs = append(cleanupDirs, tmpTarget)
		client := newRegistryClient()
		ver := newVersion
		if ver == "" {
			ver, err = client.GetLatestVersion(context.Background(), upstreamPath)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "Error checking out target upstream version: %v\n", err)
				return 1
			}
		}
		if err := client.DownloadModule(context.Background(), upstreamPath, ver, tmpTarget); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error checking out target upstream version: %v\n", err)
			return 1
		}
		targetDir = tmpTarget
	} else if isGitURL(upstreamPath) || (newVersion != "" && isGitRepo(upstreamPath)) {
		tmpTarget, err := gitops.GlobalCache.GetOrExtract(upstreamPath, newVersion, newCachedDir("tf_upstream_target_*"))
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error checking out target upstream version: %v\n", err)
			return 1
		}
		targetDir = tmpTarget
	}

	targetInfo, err := ast.Inspect(targetDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error inspecting target upstream module %s: %v\n", targetDir, err)
		return 1
	}

	var pinnedInfo *ast.ModuleInfo
	if oldVersion != "" {
		pinnedDir := upstreamPath
		if registry.IsRegistrySource(upstreamPath) {
			tmpPinned, err := mkdirTemp("", "tf_registry_pinned_*")
			if err == nil {
				cleanupDirs = append(cleanupDirs, tmpPinned)
				client := newRegistryClient()
				if err := client.DownloadModule(context.Background(), upstreamPath, oldVersion, tmpPinned); err == nil {
					pinnedDir = tmpPinned
				}
			}
		} else if isGitURL(upstreamPath) || isGitRepo(upstreamPath) {
			tmpPinned, err := gitops.GlobalCache.GetOrExtract(upstreamPath, oldVersion, newCachedDir("tf_upstream_pinned_*"))
			if err == nil {
				pinnedDir = tmpPinned
			}
		}
		pinnedInfo, _ = ast.Inspect(pinnedDir)
	}

	res := wrapper.AnalyzeWrapper(wInfo, targetInfo, pinnedInfo, newVersion, oldVersion)
	res.WrapperPath = wrapperPath
	if policyConfig != nil {
		policyInput := policy.Input{
			BreakingChanges: res.TotalBreaking,
			Warnings:        res.TotalWarnings,
		}
		if res.UpstreamDiff != nil {
			policyInput.BreakingChanges += res.UpstreamDiff.TotalBreaking
			policyInput.Warnings += res.UpstreamDiff.TotalWarnings
			policyInput.IsDowngrade = res.UpstreamDiff.IsDowngrade
			policyInput.BumpType = string(res.UpstreamDiff.BumpType)
		}
		res.PolicyViolations = policy.Evaluate(policyConfig, policyInput)
	}
	if fixPreview {
		var removedInputs []string
		for _, issue := range res.Issues {
			if issue.Category == "PassedRemovedInput" {
				removedInputs = append(removedInputs, issue.Target)
			}
		}
		preview, err := codemod.PreviewRemovedInputs(wrapperPath, res.TargetModuleName, removedInputs)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error generating fix preview: %v\n", err)
			return 1
		}
		if preview == "" {
			preview = "No automatic patch can be generated for the detected findings.\n" +
				"Only removed arguments in the wrapper's Terraform module block are currently patchable; no files were changed.\n"
		}
		if err := writeOutput(outputFile, preview, stdout); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error writing fix preview: %v\n", err)
			return 1
		}
		if enforceExit1 && !res.CanSafelyUpdate {
			return 1
		}
		if len(res.PolicyViolations) > 0 {
			return 1
		}
		return 0
	}
	if fixMode {
		var removedInputs []string
		for _, issue := range res.Issues {
			if issue.Category == "PassedRemovedInput" {
				removedInputs = append(removedInputs, issue.Target)
			}
		}
		modified, err := codemod.ApplyRemovedInputs(wrapperPath, res.TargetModuleName, removedInputs)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error applying fixes: %v\n", err)
			return 1
		}
		var msg string
		if len(modified) == 0 {
			msg = "No fixes were applied; no patchable arguments found.\n"
		} else {
			msg = fmt.Sprintf("Applied fixes to %d file(s):\n  %s\n", len(modified), strings.Join(modified, "\n  "))
		}
		if err := writeOutput(outputFile, msg, stdout); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error writing fix output: %v\n", err)
			return 1
		}
		if enforceExit1 && !res.CanSafelyUpdate {
			return 1
		}
		if len(res.PolicyViolations) > 0 {
			return 1
		}
		return 0
	}

	canSafelyUpdate := res.CanSafelyUpdate

	findings := report.Findings{}
	if checkRecreation {
		findings.RecreationHazards = recreation.AnalyzeWrapperHazardsWithRegistry(res, dynamicRegistry)
	}
	if blastRadiusDir != "" {
		blastReport, err := blastradius.ScanDirectory(blastRadiusDir, wrapperPath, wInfo, res)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error scanning blast radius: %v\n", err)
			return 1
		}
		if !blastReport.CanSafelyUpdateAll {
			canSafelyUpdate = false
		}
		findings.BlastRadius = blastReport
	}

	outContent := renderWrapperOutput(format, res, findings)

	if err := writeOutput(outputFile, outContent, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error writing output: %v\n", err)
		return 1
	}

	if (enforceExit1 && !canSafelyUpdate) || len(res.PolicyViolations) > 0 {
		return 1
	}
	return 0
}

func runCompareMode(oldPath, newPath, oldVersion, newVersion, format, outputFile string, checkRecreation, enforceExit1, analyzeUpgradePath bool, policyConfig *policy.Policy, dynamicRegistry *recreation.DynamicHazardRegistry, stdout, stderr io.Writer) int {
	if oldPath == "" || newPath == "" {
		_, _ = fmt.Fprintln(stderr, "Error: Both --old and --new must be specified for module comparison")
		return 2
	}
	if analyzeUpgradePath && (oldVersion == "" || newVersion == "") {
		_, _ = fmt.Fprintln(stderr, "Error: --upgrade-path requires --old-version and --new-version")
		return 2
	}
	if analyzeUpgradePath && semverutil.Compare(oldVersion, newVersion) >= 0 {
		_, _ = fmt.Fprintln(stderr, "Error: --upgrade-path requires --new-version to be greater than --old-version")
		return 2
	}
	if analyzeUpgradePath && (format == "sarif") {
		_, _ = fmt.Fprintln(stderr, "Error: --upgrade-path supports text, markdown, and json formats")
		return 2
	}

	var cleanupDirs []string
	defer func() {
		for _, d := range cleanupDirs {
			_ = os.RemoveAll(d)
		}
	}()

	actualOld := oldPath
	if registry.IsRegistrySource(oldPath) {
		tmpOld, err := mkdirTemp("", "tf_registry_old_*")
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error creating temp dir: %v\n", err)
			return 1
		}
		cleanupDirs = append(cleanupDirs, tmpOld)
		client := newRegistryClient()
		ver := oldVersion
		if ver == "" {
			ver, _ = client.GetLatestVersion(context.Background(), oldPath)
		}
		if err := client.DownloadModule(context.Background(), oldPath, ver, tmpOld); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error checking out old version: %v\n", err)
			return 1
		}
		actualOld = tmpOld
	} else if isGitURL(oldPath) || (oldVersion != "" && isGitRepo(oldPath)) {
		tmpOld, err := gitops.GlobalCache.GetOrExtract(oldPath, oldVersion, newCachedDir("tf_old_*"))
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error checking out old version: %v\n", err)
			return 1
		}
		actualOld = tmpOld
	}

	actualNew := newPath
	if registry.IsRegistrySource(newPath) {
		tmpNew, err := mkdirTemp("", "tf_registry_new_*")
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error creating temp dir: %v\n", err)
			return 1
		}
		cleanupDirs = append(cleanupDirs, tmpNew)
		client := newRegistryClient()
		ver := newVersion
		if ver == "" {
			ver, _ = client.GetLatestVersion(context.Background(), newPath)
		}
		if err := client.DownloadModule(context.Background(), newPath, ver, tmpNew); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error checking out new version: %v\n", err)
			return 1
		}
		actualNew = tmpNew
	} else if isGitURL(newPath) || (newVersion != "" && isGitRepo(newPath)) {
		tmpNew, err := gitops.GlobalCache.GetOrExtract(newPath, newVersion, newCachedDir("tf_new_*"))
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error checking out new version: %v\n", err)
			return 1
		}
		actualNew = tmpNew
	}

	var oldInfo, newInfo *ast.ModuleInfo
	g, _ := errgroup.WithContext(context.Background())

	g.Go(func() error {
		var err error
		oldInfo, err = ast.Inspect(actualOld)
		if err != nil {
			return fmt.Errorf("inspecting old module %s: %w", actualOld, err)
		}
		return nil
	})

	g.Go(func() error {
		var err error
		newInfo, err = ast.Inspect(actualNew)
		if err != nil {
			return fmt.Errorf("inspecting new module %s: %w", actualNew, err)
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error comparing modules: %v\n", err)
		return 1
	}

	diffReport := diff.Compare(oldInfo, newInfo, oldVersion, newVersion)
	// Identify the compared module by the source the user asked for, so reports and SARIF
	// artifact locations name the module instead of a temporary extraction directory.
	comparedSource := comparisonSource(newPath, oldPath)
	if comparedSource != "" {
		diffReport.ModuleName = comparedSource
	}
	if analyzeUpgradePath {
		pathReport, err := analyzeUpgradePathVersions(oldPath, newPath, oldVersion, newVersion, oldInfo, newInfo)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error analyzing upgrade path: %v\n", err)
			return 1
		}
		if comparedSource != "" {
			for _, step := range pathReport.Steps {
				if step.Diff != nil {
					step.Diff.ModuleName = comparedSource
				}
			}
		}
		if policyConfig != nil {
			for _, step := range pathReport.Steps {
				pathReport.PolicyViolations = append(pathReport.PolicyViolations, policy.Evaluate(policyConfig, policy.Input{
					BreakingChanges: step.Diff.TotalBreaking,
					Warnings:        step.Diff.TotalWarnings,
					IsDowngrade:     step.Diff.IsDowngrade,
					BumpType:        string(step.Diff.BumpType),
				})...)
			}
		}
		var content string
		switch format {
		case "json":
			content = string(pathReport.RenderJSON())
		case "markdown":
			content = pathReport.RenderMarkdown()
		default:
			content = pathReport.RenderText()
		}
		if err := writeOutput(outputFile, content, stdout); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error writing output: %v\n", err)
			return 1
		}
		if (enforceExit1 && !pathReport.CanSafelyUpdateAll) || len(pathReport.PolicyViolations) > 0 {
			return 1
		}
		return 0
	}
	if policyConfig != nil {
		diffReport.PolicyViolations = policy.Evaluate(policyConfig, policy.Input{
			BreakingChanges: diffReport.TotalBreaking,
			Warnings:        diffReport.TotalWarnings,
			IsDowngrade:     diffReport.IsDowngrade,
			BumpType:        string(diffReport.BumpType),
		})
	}

	findings := report.Findings{}
	if checkRecreation {
		findings.RecreationHazards = recreation.AnalyzeHazardsWithRegistry(diffReport, dynamicRegistry)
	}
	outContent := renderDiffOutput(format, diffReport, findings)

	if err := writeOutput(outputFile, outContent, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error writing output: %v\n", err)
		return 1
	}

	if (enforceExit1 && !diffReport.CanSafelyUpdate) || len(diffReport.PolicyViolations) > 0 {
		return 1
	}
	return 0
}

// renderWrapperOutput renders a wrapper audit, including the optional recreation hazards and
// downstream consumer scan, in the requested format. Machine-readable formats stay single valid
// documents instead of concatenating a second report onto the first.
func renderWrapperOutput(format string, res *wrapper.WrapperCompatibilityReport, findings report.Findings) string {
	switch format {
	case "json":
		return string(report.RenderWrapperJSON(res, findings))
	case "sarif":
		return string(report.RenderWrapperSARIF(res, findings))
	case "markdown":
		out := report.RenderWrapperMarkdown(res)
		if len(findings.RecreationHazards) > 0 {
			out += recreation.FormatHazardsMarkdown(findings.RecreationHazards)
		}
		if findings.BlastRadius != nil {
			out += "\n" + findings.BlastRadius.RenderMarkdown()
		}
		return out
	default:
		return report.RenderWrapperText(res, findings)
	}
}

// renderDiffOutput renders a direct comparison in the requested format.
func renderDiffOutput(format string, diffReport *diff.DiffReport, findings report.Findings) string {
	switch format {
	case "json":
		return string(report.RenderJSON(diffReport, findings))
	case "sarif":
		return string(report.RenderSARIF(diffReport, findings))
	case "markdown":
		out := report.RenderMarkdown(diffReport)
		if len(findings.RecreationHazards) > 0 {
			out += recreation.FormatHazardsMarkdown(findings.RecreationHazards)
		}
		return out
	default:
		return report.RenderText(diffReport, findings)
	}
}

// comparisonSource returns the user facing identifier of the compared module source.
func comparisonSource(newPath, oldPath string) string {
	if trimmed := strings.TrimSpace(newPath); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(oldPath)
}

func analyzeUpgradePathVersions(source, newSource, oldVersion, newVersion string, oldInfo, newInfo *ast.ModuleInfo) (*planner.UpgradePathReport, error) {
	if (registry.IsRegistrySource(source) || isGitURL(source) || isGitRepo(source)) && source != newSource {
		return nil, fmt.Errorf("intermediate version analysis requires --old and --new to use the same source")
	}
	versions, err := planner.NewUpgradePlanner(newRegistryClient()).PlanUpgrade(context.Background(), source, oldVersion, newVersion)
	if err != nil {
		return nil, err
	}
	snapshots := make([]planner.VersionSnapshot, 0, len(versions))
	for _, version := range versions {
		switch {
		case semverutil.Compare(version, oldVersion) == 0:
			snapshots = append(snapshots, planner.VersionSnapshot{Version: version, Module: oldInfo})
		case semverutil.Compare(version, newVersion) == 0:
			snapshots = append(snapshots, planner.VersionSnapshot{Version: version, Module: newInfo})
		default:
			module, err := inspectModuleVersion(source, version)
			if err != nil {
				return nil, fmt.Errorf("inspect intermediate version %s: %w", version, err)
			}
			snapshots = append(snapshots, planner.VersionSnapshot{Version: version, Module: module})
		}
	}
	return planner.AnalyzeUpgradePath(snapshots)
}

func inspectModuleVersion(source, version string) (*ast.ModuleInfo, error) {
	if registry.IsRegistrySource(source) {
		directory, err := mkdirTemp("", "tfastdiff_upgrade_step_*")
		if err != nil {
			return nil, fmt.Errorf("create temporary module directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(directory) }()
		if err := newRegistryClient().DownloadModule(context.Background(), source, version, directory); err != nil {
			return nil, fmt.Errorf("download registry module %s@%s: %w", source, version, err)
		}
		return ast.Inspect(directory)
	}
	if isGitURL(source) || isGitRepo(source) {
		directory, err := gitops.GlobalCache.GetOrExtract(source, version, newCachedDir("tfastdiff_upgrade_step_*"))
		if err != nil {
			return nil, fmt.Errorf("checkout repository version %s: %w", version, err)
		}
		return ast.Inspect(directory)
	}
	return nil, fmt.Errorf("cannot retrieve intermediate version %s from a non-versioned local directory", version)
}

// newCachedDir builds the directory factory handed to the shared repository cache so that
// temporary directory creation stays under the CLI's own (injectable) mkdirTemp seam.
func newCachedDir(pattern string) gitops.DirFactory {
	return func() (string, error) {
		return mkdirTemp("", pattern)
	}
}

func isGitURL(path string) bool {
	return strings.HasPrefix(path, "http://") ||
		strings.HasPrefix(path, "https://") ||
		strings.HasPrefix(path, "file://") ||
		strings.HasPrefix(path, "git@") ||
		strings.HasPrefix(path, "git::")
}

func isGitRepo(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() {
		return false
	}
	_, err = os.Stat(path + "/.git")
	return err == nil
}

func writeOutput(outputFile, content string, stdout io.Writer) error {
	if outputFile != "" {
		return os.WriteFile(filepath.Clean(outputFile), []byte(content), 0600)
	}
	_, err := fmt.Fprint(stdout, content)
	return err
}
