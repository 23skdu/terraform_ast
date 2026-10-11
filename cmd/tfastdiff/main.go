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
	fs := flag.NewFlagSet("tfastdiff", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		wrapperPath     string
		upstreamPath    string
		oldPath         string
		newPath         string
		oldVersion      string
		newVersion      string
		format          string
		outputFile      string
		blastRadiusDir  string
		checkRecreation bool
		enforceExit1    bool
		policyPath      string
		fixPreview      bool
		upgradePath     bool
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
	fs.BoolVar(&upgradePath, "upgrade-path", false, "Analyze each intermediate semantic version between --old-version and --new-version")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	format = strings.ToLower(strings.TrimSpace(format))
	var policyConfig *policy.Policy
	if policyPath != "" {
		var err error
		policyConfig, err = policy.Load(policyPath)
		if err != nil {
			fmt.Fprintf(stderr, "Error loading policy %s: %v\n", policyPath, err)
			return 2
		}
	}

	// Mode 1: Wrapper Audit Mode
	if wrapperPath != "" {
		if upgradePath {
			fmt.Fprintln(stderr, "Error: --upgrade-path is only available in direct comparison mode")
			return 2
		}
		return runWrapperMode(wrapperPath, upstreamPath, oldVersion, newVersion, format, outputFile, blastRadiusDir, checkRecreation, enforceExit1, fixPreview, policyConfig, stdout, stderr)
	}

	// Mode 2: Direct Upstream Comparison Mode
	if oldPath != "" || newPath != "" {
		if fixPreview {
			fmt.Fprintln(stderr, "Error: --fix-preview is only available in wrapper audit mode")
			return 2
		}
		return runCompareMode(oldPath, newPath, oldVersion, newVersion, format, outputFile, checkRecreation, enforceExit1, upgradePath, policyConfig, stdout, stderr)
	}

	fmt.Fprintln(stderr, "Error: Must specify either --wrapper <dir> OR --old <path> and --new <path>")
	fs.Usage()
	return 2
}

func runWrapperMode(wrapperPath, upstreamPath, oldVersion, newVersion, format, outputFile, blastRadiusDir string, checkRecreation, enforceExit1, fixPreview bool, policyConfig *policy.Policy, stdout, stderr io.Writer) int {
	wInfo, err := ast.Inspect(wrapperPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error inspecting wrapper module %s: %v\n", wrapperPath, err)
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
		fmt.Fprintln(stderr, "Error: upstream module path not specified and could not be deduced from wrapper")
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
			fmt.Fprintf(stderr, "Error creating temp dir: %v\n", err)
			return 1
		}
		cleanupDirs = append(cleanupDirs, tmpTarget)
		client := newRegistryClient()
		ver := newVersion
		if ver == "" {
			ver, err = client.GetLatestVersion(context.Background(), upstreamPath)
			if err != nil {
				fmt.Fprintf(stderr, "Error checking out target upstream version: %v\n", err)
				return 1
			}
		}
		if err := client.DownloadModule(context.Background(), upstreamPath, ver, tmpTarget); err != nil {
			fmt.Fprintf(stderr, "Error checking out target upstream version: %v\n", err)
			return 1
		}
		targetDir = tmpTarget
	} else if isGitURL(upstreamPath) || (newVersion != "" && isGitRepo(upstreamPath)) {
		tmpTarget, err := mkdirTemp("", "tf_upstream_target_*")
		if err != nil {
			fmt.Fprintf(stderr, "Error creating temp dir: %v\n", err)
			return 1
		}
		cleanupDirs = append(cleanupDirs, tmpTarget)
		if err := gitops.ExtractVersionToDirShallow(upstreamPath, newVersion, tmpTarget); err != nil {
			fmt.Fprintf(stderr, "Error checking out target upstream version: %v\n", err)
			return 1
		}
		targetDir = tmpTarget
	}

	targetInfo, err := ast.Inspect(targetDir)
	if err != nil {
		fmt.Fprintf(stderr, "Error inspecting target upstream module %s: %v\n", targetDir, err)
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
			tmpPinned, err := mkdirTemp("", "tf_upstream_pinned_*")
			if err != nil {
				fmt.Fprintf(stderr, "Error creating temp dir: %v\n", err)
				return 1
			}
			cleanupDirs = append(cleanupDirs, tmpPinned)
			if err := gitops.ExtractVersionToDirShallow(upstreamPath, oldVersion, tmpPinned); err == nil {
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
			fmt.Fprintf(stderr, "Error generating fix preview: %v\n", err)
			return 1
		}
		if preview == "" {
			preview = "No automatic patch can be generated for the detected findings.\n" +
				"Only removed arguments in the wrapper's Terraform module block are currently patchable; no files were changed.\n"
		}
		if err := writeOutput(outputFile, preview, stdout); err != nil {
			fmt.Fprintf(stderr, "Error writing fix preview: %v\n", err)
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

	var outContent string
	switch format {
	case "json":
		b, _ := report.RenderWrapperJSON(res)
		outContent = string(b)
	case "sarif":
		b, _ := report.RenderWrapperSARIF(res)
		outContent = string(b)
	case "markdown":
		outContent = report.RenderWrapperMarkdown(res)
		if checkRecreation {
			hazards := recreation.AnalyzeWrapperHazards(res)
			if len(hazards) > 0 {
				outContent += recreation.FormatHazardsMarkdown(hazards)
			}
		}
	default:
		outContent = report.RenderWrapperText(res)
	}

	canSafelyUpdate := res.CanSafelyUpdate

	if blastRadiusDir != "" {
		blastReport, err := blastradius.ScanDirectory(blastRadiusDir, wrapperPath, wInfo, res)
		if err != nil {
			fmt.Fprintf(stderr, "Error scanning blast radius: %v\n", err)
			return 1
		}
		if !blastReport.CanSafelyUpdateAll {
			canSafelyUpdate = false
		}
		switch format {
		case "json":
			bj, _ := blastReport.RenderJSON()
			outContent += "\n" + string(bj)
		default:
			outContent += "\n\n" + blastReport.RenderMarkdown()
		}
	}

	if err := writeOutput(outputFile, outContent, stdout); err != nil {
		fmt.Fprintf(stderr, "Error writing output: %v\n", err)
		return 1
	}

	if (enforceExit1 && !canSafelyUpdate) || len(res.PolicyViolations) > 0 {
		return 1
	}
	return 0
}

func runCompareMode(oldPath, newPath, oldVersion, newVersion, format, outputFile string, checkRecreation, enforceExit1, analyzeUpgradePath bool, policyConfig *policy.Policy, stdout, stderr io.Writer) int {
	if oldPath == "" || newPath == "" {
		fmt.Fprintln(stderr, "Error: Both --old and --new must be specified for module comparison")
		return 2
	}
	if analyzeUpgradePath && (oldVersion == "" || newVersion == "") {
		fmt.Fprintln(stderr, "Error: --upgrade-path requires --old-version and --new-version")
		return 2
	}
	if analyzeUpgradePath && semverutil.Compare(oldVersion, newVersion) >= 0 {
		fmt.Fprintln(stderr, "Error: --upgrade-path requires --new-version to be greater than --old-version")
		return 2
	}
	if analyzeUpgradePath && (format == "sarif") {
		fmt.Fprintln(stderr, "Error: --upgrade-path supports text, markdown, and json formats")
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
			fmt.Fprintf(stderr, "Error creating temp dir: %v\n", err)
			return 1
		}
		cleanupDirs = append(cleanupDirs, tmpOld)
		client := newRegistryClient()
		ver := oldVersion
		if ver == "" {
			ver, _ = client.GetLatestVersion(context.Background(), oldPath)
		}
		if err := client.DownloadModule(context.Background(), oldPath, ver, tmpOld); err != nil {
			fmt.Fprintf(stderr, "Error checking out old version: %v\n", err)
			return 1
		}
		actualOld = tmpOld
	} else if isGitURL(oldPath) || (oldVersion != "" && isGitRepo(oldPath)) {
		tmpOld, err := mkdirTemp("", "tf_old_*")
		if err != nil {
			fmt.Fprintf(stderr, "Error creating temp dir: %v\n", err)
			return 1
		}
		cleanupDirs = append(cleanupDirs, tmpOld)
		if err := gitops.ExtractVersionToDirShallow(oldPath, oldVersion, tmpOld); err != nil {
			fmt.Fprintf(stderr, "Error checking out old version: %v\n", err)
			return 1
		}
		actualOld = tmpOld
	}

	actualNew := newPath
	if registry.IsRegistrySource(newPath) {
		tmpNew, err := mkdirTemp("", "tf_registry_new_*")
		if err != nil {
			fmt.Fprintf(stderr, "Error creating temp dir: %v\n", err)
			return 1
		}
		cleanupDirs = append(cleanupDirs, tmpNew)
		client := newRegistryClient()
		ver := newVersion
		if ver == "" {
			ver, _ = client.GetLatestVersion(context.Background(), newPath)
		}
		if err := client.DownloadModule(context.Background(), newPath, ver, tmpNew); err != nil {
			fmt.Fprintf(stderr, "Error checking out new version: %v\n", err)
			return 1
		}
		actualNew = tmpNew
	} else if isGitURL(newPath) || (newVersion != "" && isGitRepo(newPath)) {
		tmpNew, err := mkdirTemp("", "tf_new_*")
		if err != nil {
			fmt.Fprintf(stderr, "Error creating temp dir: %v\n", err)
			return 1
		}
		cleanupDirs = append(cleanupDirs, tmpNew)
		if err := gitops.ExtractVersionToDirShallow(newPath, newVersion, tmpNew); err != nil {
			fmt.Fprintf(stderr, "Error checking out new version: %v\n", err)
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
			return fmt.Errorf("Error inspecting old module %s: %v", actualOld, err)
		}
		return nil
	})

	g.Go(func() error {
		var err error
		newInfo, err = ast.Inspect(actualNew)
		if err != nil {
			return fmt.Errorf("Error inspecting new module %s: %v", actualNew, err)
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	diffReport := diff.Compare(oldInfo, newInfo, oldVersion, newVersion)
	if analyzeUpgradePath {
		pathReport, err := analyzeUpgradePathVersions(oldPath, newPath, oldVersion, newVersion, oldInfo, newInfo)
		if err != nil {
			fmt.Fprintf(stderr, "Error analyzing upgrade path: %v\n", err)
			return 1
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
			data, err := pathReport.RenderJSON()
			if err != nil {
				fmt.Fprintf(stderr, "Error encoding upgrade path report: %v\n", err)
				return 1
			}
			content = string(data)
		case "markdown":
			content = pathReport.RenderMarkdown()
		default:
			content = pathReport.RenderText()
		}
		if err := writeOutput(outputFile, content, stdout); err != nil {
			fmt.Fprintf(stderr, "Error writing output: %v\n", err)
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

	var outContent string
	switch format {
	case "json":
		b, _ := report.RenderJSON(diffReport)
		outContent = string(b)
	case "sarif":
		b, _ := report.RenderSARIF(diffReport)
		outContent = string(b)
	case "markdown":
		outContent = report.RenderMarkdown(diffReport)
		if checkRecreation {
			hazards := recreation.AnalyzeHazards(diffReport)
			if len(hazards) > 0 {
				outContent += recreation.FormatHazardsMarkdown(hazards)
			}
		}
	default:
		outContent = report.RenderText(diffReport)
	}

	if err := writeOutput(outputFile, outContent, stdout); err != nil {
		fmt.Fprintf(stderr, "Error writing output: %v\n", err)
		return 1
	}

	if (enforceExit1 && !diffReport.CanSafelyUpdate) || len(diffReport.PolicyViolations) > 0 {
		return 1
	}
	return 0
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
	directory, err := mkdirTemp("", "tfastdiff_upgrade_step_*")
	if err != nil {
		return nil, fmt.Errorf("create temporary module directory: %w", err)
	}
	defer os.RemoveAll(directory)

	if registry.IsRegistrySource(source) {
		if err := newRegistryClient().DownloadModule(context.Background(), source, version, directory); err != nil {
			return nil, fmt.Errorf("download registry module %s@%s: %w", source, version, err)
		}
	} else if isGitURL(source) || isGitRepo(source) {
		if err := gitops.ExtractVersionToDirShallow(source, version, directory); err != nil {
			return nil, fmt.Errorf("checkout repository version %s: %w", version, err)
		}
	} else {
		return nil, fmt.Errorf("cannot retrieve intermediate version %s from a non-versioned local directory", version)
	}
	return ast.Inspect(directory)
}

func isGitURL(path string) bool {
	return strings.HasPrefix(path, "http://") ||
		strings.HasPrefix(path, "https://") ||
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
