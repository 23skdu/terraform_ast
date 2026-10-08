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
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/gitops"
	"github.com/23skdu/terraform_ast/pkg/recreation"
	"github.com/23skdu/terraform_ast/pkg/registry"
	"github.com/23skdu/terraform_ast/pkg/report"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
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

	if err := fs.Parse(args); err != nil {
		return 2
	}

	format = strings.ToLower(strings.TrimSpace(format))

	// Mode 1: Wrapper Audit Mode
	if wrapperPath != "" {
		return runWrapperMode(wrapperPath, upstreamPath, oldVersion, newVersion, format, outputFile, blastRadiusDir, checkRecreation, enforceExit1, stdout, stderr)
	}

	// Mode 2: Direct Upstream Comparison Mode
	if oldPath != "" || newPath != "" {
		return runCompareMode(oldPath, newPath, oldVersion, newVersion, format, outputFile, checkRecreation, enforceExit1, stdout, stderr)
	}

	fmt.Fprintln(stderr, "Error: Must specify either --wrapper <dir> OR --old <path> and --new <path>")
	fs.Usage()
	return 2
}

func runWrapperMode(wrapperPath, upstreamPath, oldVersion, newVersion, format, outputFile, blastRadiusDir string, checkRecreation, enforceExit1 bool, stdout, stderr io.Writer) int {
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

	if enforceExit1 && !canSafelyUpdate {
		return 1
	}
	return 0
}

func runCompareMode(oldPath, newPath, oldVersion, newVersion, format, outputFile string, checkRecreation, enforceExit1 bool, stdout, stderr io.Writer) int {
	if oldPath == "" || newPath == "" {
		fmt.Fprintln(stderr, "Error: Both --old and --new must be specified for module comparison")
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

	oldInfo, err := ast.Inspect(actualOld)
	if err != nil {
		fmt.Fprintf(stderr, "Error inspecting old module %s: %v\n", actualOld, err)
		return 1
	}

	newInfo, err := ast.Inspect(actualNew)
	if err != nil {
		fmt.Fprintf(stderr, "Error inspecting new module %s: %v\n", actualNew, err)
		return 1
	}

	diffReport := diff.Compare(oldInfo, newInfo, oldVersion, newVersion)

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

	if enforceExit1 && !diffReport.CanSafelyUpdate {
		return 1
	}
	return 0
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
