package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/blastradius"
	"github.com/23skdu/terraform_ast/pkg/codemod"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/eval"
	"github.com/23skdu/terraform_ast/pkg/gitops"
	"github.com/23skdu/terraform_ast/pkg/lsp"
	"github.com/23skdu/terraform_ast/pkg/planner"
	"github.com/23skdu/terraform_ast/pkg/policy"
	"github.com/23skdu/terraform_ast/pkg/recreation"
	"github.com/23skdu/terraform_ast/pkg/registry"
	"github.com/23skdu/terraform_ast/pkg/report"
	"github.com/23skdu/terraform_ast/pkg/semverutil"
	"github.com/23skdu/terraform_ast/pkg/visualize"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
)

var (
	version           = "0.9.0"
	osExit            = os.Exit
	mkdirTemp         = os.MkdirTemp
	newRegistryClient = func() *registry.Client {
		return registry.NewClient(nil)
	}
	analyzeVariableTaint   = eval.AnalyzeVariableTaint
	inspectModulesParallel = ast.InspectModulesParallel
)

// defaultRegistryTimeout bounds how long the CLI will keep retrying and waiting on the Terraform
// Registry before giving up on a request.
const defaultRegistryTimeout = 2 * time.Minute

func main() {
	exitCode := Execute(os.Args[1:], os.Stdout, os.Stderr)
	osExit(exitCode)
}

// Execute runs the CLI application with given arguments and streams.
func Execute(args []string, stdout, stderr io.Writer) int {
	// Repositories and extracted versions are owned by the shared gitops cache; release them
	// when the run finishes so temporary directories never outlive the process.
	defer gitops.GlobalCache.Clear()

	// Ctrl-C (or a CI cancellation signal) cancels every in-flight registry request, and the
	// deferred cache clear then removes the temporary directories. Without this a hung registry
	// kept the process and its temp directories alive for the full retry budget.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(args) > 0 && (args[0] == "lsp" || args[0] == "daemon") {
		_ = lsp.RunDaemonStreams(os.Stdin, stdout)
		return 0
	}

	fs := flag.NewFlagSet("tfastdiff", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		showVersion        bool
		verbose            bool
		wrapperPath        string
		upstreamPath       string
		oldPath            string
		newPath            string
		oldVersion         string
		newVersion         string
		format             string
		outputFile         string
		blastRadiusDir     string
		goldenPlanPath     string
		checkRecreation    bool
		enforceExit1       bool
		policyPath         string
		regoPolicyPath     string
		taintVar           string
		fixPreview         bool
		fixMode            bool
		upgradePath        bool
		providerSchemaPath string
		cacheDir           string
		clearCache         bool
		lspDaemon          bool
		registryTimeout    time.Duration
		svgBefore          string
		svgAfter           string
		svgTransition      string
	)

	fs.BoolVar(&showVersion, "version", false, "Print version and exit")
	fs.BoolVar(&verbose, "verbose", false, "Enable verbose logging to stderr")
	fs.BoolVar(&verbose, "v", false, "Enable verbose logging to stderr (shorthand)")
	fs.StringVar(&wrapperPath, "wrapper", "", "Path to the Terraform module wrapper directory")
	fs.StringVar(&upstreamPath, "upstream", "", "Path, Git URL, or Registry identifier of the upstream Terraform module")
	fs.StringVar(&oldPath, "old", "", "Path, Git URL, or Registry identifier to the current/old upstream module")
	fs.StringVar(&newPath, "new", "", "Path, Git URL, or Registry identifier to the target/new upstream module")
	fs.StringVar(&oldVersion, "old-version", "", "Version or Git ref for the old upstream module")
	fs.StringVar(&newVersion, "new-version", "", "Version or Git ref for the new upstream module")
	fs.StringVar(&format, "format", "text", "Output format: text, markdown, json, sarif")
	fs.StringVar(&outputFile, "output", "", "Optional file path to write the output report")
	fs.StringVar(&goldenPlanPath, "golden-plan", "", "Optional path to Terraform golden plan JSON to verify for recreation hazards upon version upgrade (autodiscovered if omitted in wrapper mode)")
	// --blast-radius is optional and is frequently passed by wrapper manifests (including
	// action.yml) with an unset value. An empty value means "not requested", and a value that
	// looks like another flag is rejected with an actionable message instead of being treated as
	// a directory path.
	fs.Func("blast-radius", "Optional root directory to scan for downstream module consumers", func(value string) error {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			blastRadiusDir = ""
			return nil
		}
		if strings.HasPrefix(trimmed, "-") {
			return fmt.Errorf("--blast-radius requires a directory path, but received %q", value)
		}
		blastRadiusDir = trimmed
		return nil
	})
	fs.BoolVar(&checkRecreation, "check-recreation", true, "Check for resource recreation & state destruction hazards")
	fs.BoolVar(&enforceExit1, "exit-code", false, "Exit with code 1 if breaking changes are detected")
	fs.StringVar(&policyPath, "policy", "", "Optional JSON policy file for CI enforcement")
	fs.StringVar(&regoPolicyPath, "rego-policy", "", "Optional Rego (.rego) policy file for policy enforcement")
	fs.StringVar(&taintVar, "taint", "", "Optional input variable name to perform taint/propagation analysis on (wrapper mode)")
	fs.BoolVar(&fixPreview, "fix-preview", false, "Preview a patch removing wrapper arguments no longer accepted upstream (never edits files)")
	fs.BoolVar(&fixMode, "fix", false, "Apply fixes in-place to remove wrapper arguments no longer accepted upstream")
	fs.BoolVar(&upgradePath, "upgrade-path", false, "Analyze each intermediate semantic version between --old-version and --new-version")
	fs.StringVar(&providerSchemaPath, "provider-schema", "", "Optional path to terraform providers schema -json file for dynamic ForceNew recreation detection")
	fs.StringVar(&cacheDir, "cache-dir", "", "Optional path to directory for persistent L2 AST caching")
	fs.BoolVar(&clearCache, "clear-cache", false, "Remove cached module inspections from --cache-dir and exit")
	fs.BoolVar(&lspDaemon, "lsp", false, "Start Language Server Protocol (LSP) daemon for IDE integration")
	fs.DurationVar(&registryTimeout, "registry-timeout", defaultRegistryTimeout, "Maximum time to spend retrying and waiting on a single Terraform Registry request")
	fs.StringVar(&svgBefore, "svg-before", "", "Write an SVG rendering of the before state graph to this path")
	fs.StringVar(&svgAfter, "svg-after", "", "Write an SVG rendering of the after state graph to this path")
	fs.StringVar(&svgTransition, "svg-transition", "", "Write an SVG rendering of the before/after transition graph to this path")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if showVersion {
		_, _ = fmt.Fprintf(stdout, "tfastdiff %s\n", version)
		return 0
	}

	// Record which flags the caller actually supplied, so a default value is never mistaken for
	// a deliberate request. --check-recreation defaults to true, so only an explicit
	// --check-recreation=false is a request the CLI cannot honour.
	supplied := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { supplied[f.Name] = true })

	if lspDaemon {
		_ = lsp.RunDaemonStreams(os.Stdin, stdout)
		return 0
	}

	if registryTimeout <= 0 {
		_, _ = fmt.Fprintf(stderr, "Error: --registry-timeout must be positive, got %s\n", registryTimeout)
		return 2
	}

	if fixMode && fixPreview {
		_, _ = fmt.Fprintln(stderr, "Error: Cannot specify both --fix and --fix-preview")
		return 2
	}

	if fixMode || fixPreview {
		// The codemod paths return before the findings block, so any analysis flag passed with
		// them would be dropped without a trace. Refuse rather than imply it was honoured.
		if blastRadiusDir != "" {
			_, _ = fmt.Fprintln(stderr, "Error: --blast-radius is not supported with --fix or --fix-preview; run the audit separately")
			return 2
		}
		if goldenPlanPath != "" {
			_, _ = fmt.Fprintln(stderr, "Error: --golden-plan is not supported with --fix or --fix-preview; run the audit separately")
			return 2
		}
		if providerSchemaPath != "" {
			_, _ = fmt.Fprintln(stderr, "Error: --provider-schema is not supported with --fix or --fix-preview; run the audit separately")
			return 2
		}
		if supplied["check-recreation"] {
			_, _ = fmt.Fprintln(stderr, "Error: --check-recreation is not supported with --fix or --fix-preview; run the audit separately")
			return 2
		}
		if taintVar != "" {
			_, _ = fmt.Fprintln(stderr, "Error: --taint is not supported with --fix or --fix-preview; run the audit separately")
			return 2
		}
		// The codemod emits a plain patch, so a machine-readable format could never be honoured.
		if format != "text" {
			_, _ = fmt.Fprintf(stderr, "Error: --format %s is not supported with --fix or --fix-preview, which always emits a plain patch\n", format)
			return 2
		}
	}

	if cacheDir != "" {
		ast.SetPersistentCacheDir(cacheDir)
	}

	if clearCache {
		if cacheDir == "" {
			_, _ = fmt.Fprintln(stderr, "Error: --clear-cache requires --cache-dir")
			return 2
		}
		if err := ast.ClearPersistentCache(); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error clearing cache %s: %v\n", cacheDir, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "Cleared cached module inspections from %s\n", cacheDir)
		return 0
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
	switch format {
	case "text", "markdown", "json", "sarif":
		format = strings.ToLower(format)
	default:
		_, _ = fmt.Fprintf(stderr, "Error: invalid --format %q: expected one of text, markdown, json, sarif\n", format)
		return 2
	}

	var policyConfig *policy.Policy
	if policyPath != "" {
		var err error
		policyConfig, err = policy.Load(policyPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error loading policy %s: %v\n", policyPath, err)
			return 2
		}
	}

	var regoPolicy *policy.RegoPolicy
	if regoPolicyPath != "" {
		var err error
		regoPolicy, err = policy.LoadRego(regoPolicyPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error loading Rego policy %s: %v\n", regoPolicyPath, err)
			return 2
		}
	}

	// Mode 1: Wrapper Audit Mode
	if wrapperPath != "" {
		if upgradePath {
			_, _ = fmt.Fprintln(stderr, "Error: --upgrade-path is only available in direct comparison mode")
			return 2
		}
		return runWrapperMode(ctx, registryTimeout, wrapperPath, upstreamPath, oldVersion, newVersion, format, outputFile, blastRadiusDir, goldenPlanPath, checkRecreation, enforceExit1, fixPreview, fixMode, policyConfig, regoPolicy, taintVar, verbose, dynamicRegistry, stdout, stderr)
	}

	// Mode 2: Direct Upstream Comparison Mode
	if oldPath != "" || newPath != "" {
		if goldenPlanPath != "" {
			_, _ = fmt.Fprintln(stderr, "Error: --golden-plan is only available in wrapper audit mode")
			return 2
		}
		if taintVar != "" {
			_, _ = fmt.Fprintln(stderr, "Error: --taint is only available in wrapper audit mode")
			return 2
		}
		if fixPreview {
			_, _ = fmt.Fprintln(stderr, "Error: --fix-preview is only available in wrapper audit mode")
			return 2
		}
		if fixMode {
			_, _ = fmt.Fprintln(stderr, "Error: --fix is only available in wrapper audit mode")
			return 2
		}
		if blastRadiusDir != "" {
			// Previously accepted and silently discarded: runCompareMode never received the
			// flag, so the user believed consumer impact had been analysed.
			_, _ = fmt.Fprintln(stderr, "Error: --blast-radius is only available in wrapper audit mode")
			return 2
		}
		return runCompareMode(ctx, registryTimeout, oldPath, newPath, oldVersion, newVersion, format, outputFile, checkRecreation, enforceExit1, upgradePath, policyConfig, regoPolicy, verbose, dynamicRegistry, svgBefore, svgAfter, svgTransition, stdout, stderr)
	}

	_, _ = fmt.Fprintln(stderr, "Error: Must specify either --wrapper <dir> OR --old <path> and --new <path>")
	fs.Usage()
	return 2
}

// registryClient returns a registry client whose retry budget is the operator supplied timeout.
func registryClient(timeout time.Duration) *registry.Client {
	client := newRegistryClient()
	if client.RetryMaxElapsedTime <= 0 {
		client.RetryMaxElapsedTime = timeout
	}
	return client
}

// registryContext bounds a whole registry operation, retry window included.
func registryContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, timeout)
}

func runWrapperMode(ctx context.Context, registryTimeout time.Duration, wrapperPath, upstreamPath, oldVersion, newVersion, format, outputFile, blastRadiusDir, goldenPlanPath string, checkRecreation, enforceExit1, fixPreview, fixMode bool, policyConfig *policy.Policy, regoPolicy *policy.RegoPolicy, taintVar string, verbose bool, dynamicRegistry *recreation.DynamicHazardRegistry, stdout, stderr io.Writer) int {
	vlog := func(format string, a ...any) {
		if verbose {
			_, _ = fmt.Fprintf(stderr, "[verbose] "+format+"\n", a...)
		}
	}
	vlog("Inspecting wrapper module %s", wrapperPath)
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
		client := registryClient(registryTimeout)
		ver := newVersion
		if ver == "" {
			regCtx, cancel := registryContext(ctx, registryTimeout)
			ver, err = client.GetLatestVersion(regCtx, upstreamPath)
			cancel()
			if err != nil {
				// Previously ignored, so a failed lookup silently downloaded an unspecified
				// version and reported the result as if it were the requested one.
				_, _ = fmt.Fprintf(stderr, "Error resolving the latest version of %s: %v\n", upstreamPath, err)
				return 1
			}
		}
		vlog("Downloading target upstream module %s@%s", upstreamPath, ver)
		regCtx, cancel := registryContext(ctx, registryTimeout)
		err = client.DownloadModule(regCtx, upstreamPath, ver, tmpTarget)
		cancel()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error checking out target upstream version: %v\n", err)
			return 1
		}
		targetDir = tmpTarget
		if sub := registry.SubmodulePath(upstreamPath); sub != "" {
			targetDir = filepath.Join(tmpTarget, sub)
		}
	} else if isGitURL(upstreamPath) || (newVersion != "" && isGitRepo(upstreamPath)) {
		vlog("Checking out target upstream version %s from %s", newVersion, upstreamPath)
		tmpTarget, err := gitops.GlobalCache.GetOrExtract(upstreamPath, newVersion, newCachedDir("tf_upstream_target_*"))
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error checking out target upstream version: %v\n", err)
			return 1
		}
		targetDir = tmpTarget
	}

	var (
		targetInfo *ast.ModuleInfo
		targetErr  error
		pinnedInfo *ast.ModuleInfo
		pinnedErr  error
		pinnedDir  string
		inspectWG  sync.WaitGroup
	)
	defer inspectWG.Wait()

	vlog("Inspecting target upstream module %s", targetDir)
	inspectWG.Add(1)
	go func() {
		defer inspectWG.Done()
		targetInfo, targetErr = ast.Inspect(targetDir)
	}()

	if oldVersion != "" {
		pinnedDir = upstreamPath
		if registry.IsRegistrySource(upstreamPath) {
			tmpPinned, err := mkdirTemp("", "tf_registry_pinned_*")
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "Error creating temp dir: %v\n", err)
				return 1
			}
			cleanupDirs = append(cleanupDirs, tmpPinned)
			vlog("Downloading pinned upstream module %s@%s", upstreamPath, oldVersion)
			regCtx, cancel := registryContext(ctx, registryTimeout)
			err = registryClient(registryTimeout).DownloadModule(regCtx, upstreamPath, oldVersion, tmpPinned)
			cancel()
			if err != nil {
				// Previously swallowed, which left pinnedInfo nil: the upstream-to-upstream diff
				// was skipped entirely, so downgrade detection and the safe-pin recommendation
				// disappeared while the report still claimed the target was safe to pin.
				_, _ = fmt.Fprintf(stderr, "Error checking out pinned upstream version %s of %s: %v\n", oldVersion, upstreamPath, err)
				return 1
			}
			pinnedDir = tmpPinned
			if sub := registry.SubmodulePath(upstreamPath); sub != "" {
				pinnedDir = filepath.Join(tmpPinned, sub)
			}
		} else if isGitURL(upstreamPath) || isGitRepo(upstreamPath) {
			vlog("Checking out pinned upstream version %s from %s", oldVersion, upstreamPath)
			tmpPinned, err := gitops.GlobalCache.GetOrExtract(upstreamPath, oldVersion, newCachedDir("tf_upstream_pinned_*"))
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "Error checking out pinned upstream version %s of %s: %v\n", oldVersion, upstreamPath, err)
				return 1
			}
			pinnedDir = tmpPinned
		}
		vlog("Inspecting pinned upstream module %s", pinnedDir)
		pinnedInfo, pinnedErr = ast.Inspect(pinnedDir)
	}

	inspectWG.Wait()
	if targetErr != nil {
		_, _ = fmt.Fprintf(stderr, "Error inspecting target upstream module %s: %v\n", targetDir, targetErr)
		return 1
	}
	if pinnedErr != nil {
		_, _ = fmt.Fprintf(stderr, "Error inspecting pinned upstream module %s: %v\n", pinnedDir, pinnedErr)
		return 1
	}

	if checkRecreation && dynamicRegistry == nil {
		dynamicRegistry, _ = recreation.AutoDiscoverSchemas(wInfo, ast.GetPersistentCacheDir())
	}

	vlog("Analyzing wrapper compatibility against target upstream")
	res := wrapper.AnalyzeWrapper(wInfo, targetInfo, pinnedInfo, newVersion, oldVersion, dynamicRegistry)
	res.WrapperPath = wrapperPath
	if policyConfig != nil || regoPolicy != nil {
		policyInput := policy.Input{
			BreakingChanges: res.TotalBreaking,
			Warnings:        res.TotalWarnings,
			Report:          res,
		}
		if res.UpstreamDiff != nil {
			policyInput.BreakingChanges += res.UpstreamDiff.TotalBreaking
			policyInput.Warnings += res.UpstreamDiff.TotalWarnings
			policyInput.IsDowngrade = res.UpstreamDiff.IsDowngrade
			policyInput.BumpType = string(res.UpstreamDiff.BumpType)
		}
		if policyConfig != nil {
			res.PolicyViolations = append(res.PolicyViolations, policy.Evaluate(policyConfig, policyInput)...)
		}
		if regoPolicy != nil {
			res.PolicyViolations = append(res.PolicyViolations, policy.EvaluateRego(regoPolicy, policyInput)...)
		}
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

		// (Missing-input injection is performed by --fix; preview lists them as a note.)
		var missingNames []string
		for _, issue := range res.Issues {
			if issue.Category == "MissingRequiredInput" {
				missingNames = append(missingNames, issue.Target)
			}
		}
		if len(missingNames) > 0 && preview == "" {
			preview = "No automatic patch can be generated for removed arguments; --fix would inject missing required inputs: " + strings.Join(missingNames, ", ") + "\n"
		}
		var movedBlocksPreview string
		if pinnedInfo != nil && targetInfo != nil {
			var shifts []ast.ResourceAddressShift
			if res.UpstreamDiff != nil {
				shifts = res.UpstreamDiff.AddressShifts
			}
			if len(shifts) > 0 {
				var allMoved []ast.MovedBlockDef
				if targetInfo != nil {
					allMoved = append(allMoved, targetInfo.MovedBlocks...)
				}
				if wInfo != nil {
					allMoved = append(allMoved, wInfo.MovedBlocks...)
				}
				covered := codemod.CoveredMovedAddresses(allMoved)
				blocks := codemod.SynthesizeMovedBlocksForShifts(shifts, covered)
				if len(blocks) > 0 {
					movedTf := filepath.Join(wrapperPath, "moved.tf")
					movedBlocksPreview = fmt.Sprintf("--- %s\n+++ %s\n%s",
						movedTf, movedTf, codemod.FormatMovedBlocks(blocks))
				}
			}
		}
		if preview != "" && movedBlocksPreview != "" {
			preview = preview + "\n" + movedBlocksPreview
		} else if preview == "" && movedBlocksPreview != "" {
			preview = movedBlocksPreview
		} else if preview == "" {
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

		missingInputs := make(map[string]string)
		for _, issue := range res.Issues {
			if issue.Category != "MissingRequiredInput" {
				continue
			}
			varName := issue.Target
			placeholder := ""
			canDerive := false
			if targetInfo != nil {
				if upVar, ok := targetInfo.Variables[varName]; ok {
					switch strings.ToLower(strings.TrimSpace(upVar.Type)) {
					case "string":
						placeholder = ""
						canDerive = true
					case "bool":
						placeholder = "false"
						canDerive = true
					case "number":
						placeholder = "0"
						canDerive = true
					}
				}
			}
			if !canDerive {
				_, _ = fmt.Fprintf(stderr, "Cannot derive a safe placeholder for required input '%s'; refusing to write null. Supply it manually.\n", varName)
				return 2
			}
			missingInputs[varName] = placeholder
		}
		if len(missingInputs) > 0 {
			added, err := codemod.AddMissingInputs(wrapperPath, res.TargetModuleName, missingInputs)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "Error applying missing-input fixes: %v\n", err)
				return 1
			}
			modified = append(modified, added...)
		}
		if pinnedInfo != nil && targetInfo != nil {
			var shifts []ast.ResourceAddressShift
			if res.UpstreamDiff != nil {
				shifts = res.UpstreamDiff.AddressShifts
			}
			if len(shifts) > 0 {
				var allMoved []ast.MovedBlockDef
				if targetInfo != nil {
					allMoved = append(allMoved, targetInfo.MovedBlocks...)
				}
				if wInfo != nil {
					allMoved = append(allMoved, wInfo.MovedBlocks...)
				}
				covered := codemod.CoveredMovedAddresses(allMoved)
				movedFile := filepath.Join(wrapperPath, "moved.tf")
				if err := codemod.SynthesizeAndWriteMovedBlocks(movedFile, shifts, covered); err != nil {
					_, _ = fmt.Fprintf(stderr, "Error writing moved blocks: %v\n", err)
					return 1
				}
				modified = append(modified, movedFile)
			}
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
	if pinnedInfo != nil && targetInfo != nil && res.UpstreamDiff != nil {
		findings.AddressShifts = res.UpstreamDiff.AddressShifts
	}
	if checkRecreation {
		findings.RecreationHazards = recreation.AnalyzeWrapperHazardsWithRegistry(res, dynamicRegistry)
	}
	var taintReport *eval.TaintReport
	if taintVar != "" {
		vlog("Analyzing taint for input variable var.%s", taintVar)
		var err error
		taintReport, err = analyzeVariableTaint(wrapperPath, taintVar, wInfo.ParsedBodies)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error analyzing variable taint: %v\n", err)
			return 1
		}
		findings.Taint = taintReport
	}
	if blastRadiusDir != "" {
		vlog("Scanning downstream consumers in %s", blastRadiusDir)
		blastReport, err := blastradius.ScanDirectoryWithTaint(blastRadiusDir, wrapperPath, wInfo, res, dynamicRegistry, taintReport)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error scanning blast radius: %v\n", err)
			return 1
		}
		if !blastReport.CanSafelyUpdateAll {
			canSafelyUpdate = false
		}
		findings.BlastRadius = blastReport
	}

	var planVer *recreation.GoldenPlanVerification
	if goldenPlanPath != "" {
		vlog("Verifying golden plan %s", goldenPlanPath)
		var err error
		planVer, err = recreation.VerifyGoldenPlanFile(goldenPlanPath, false, res, findings.RecreationHazards, findings.AddressShifts)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error verifying golden plan %s: %v\n", goldenPlanPath, err)
			return 2
		}
	} else {
		autoPlan, _ := recreation.AutodiscoverGoldenPlan(wrapperPath)
		if autoPlan != "" {
			vlog("Autodiscovered golden plan %s", autoPlan)
			planVer, _ = recreation.VerifyGoldenPlanFile(autoPlan, true, res, findings.RecreationHazards, findings.AddressShifts)
		}
	}
	if planVer != nil {
		findings.GoldenPlan = planVer
		if planVer.HasRecreationHazards {
			canSafelyUpdate = false
		}
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

func runCompareMode(ctx context.Context, registryTimeout time.Duration, oldPath, newPath, oldVersion, newVersion, format, outputFile string, checkRecreation, enforceExit1, analyzeUpgradePath bool, policyConfig *policy.Policy, regoPolicy *policy.RegoPolicy, verbose bool, dynamicRegistry *recreation.DynamicHazardRegistry, svgBefore, svgAfter, svgTransition string, stdout, stderr io.Writer) int {
	vlog := func(format string, a ...any) {
		if verbose {
			_, _ = fmt.Fprintf(stderr, "[verbose] "+format+"\\n", a...)
		}
	}
	vlog("Comparing modules %s and %s", oldPath, newPath)
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

	var (
		oldInfo   *ast.ModuleInfo
		newInfo   *ast.ModuleInfo
		actualOld = oldPath
		actualNew = newPath
		oldErr    string
		newErr    string
		cleanupMu sync.Mutex
	)

	// Check persistent cache first if versions are known (#127)
	if oldVersion != "" {
		if cached, ok := ast.CachedVersionModule(oldPath, oldVersion); ok {
			oldInfo = cached
		}
	}
	if newVersion != "" {
		if cached, ok := ast.CachedVersionModule(newPath, newVersion); ok {
			newInfo = cached
		}
	}

	var fetchWG sync.WaitGroup
	if oldInfo == nil {
		fetchWG.Add(1)
		go func() {
			defer fetchWG.Done()
			if registry.IsRegistrySource(oldPath) {
				tmpOld, err := mkdirTemp("", "tf_registry_old_*")
				if err != nil {
					oldErr = fmt.Sprintf("Error creating temp dir: %v", err)
					return
				}
				cleanupMu.Lock()
				cleanupDirs = append(cleanupDirs, tmpOld)
				cleanupMu.Unlock()
				client := registryClient(registryTimeout)
				ver := oldVersion
				if ver == "" {
					regCtx, cancel := registryContext(ctx, registryTimeout)
					ver, err = client.GetLatestVersion(regCtx, oldPath)
					cancel()
					if err != nil {
						oldErr = fmt.Sprintf("Error resolving the latest version of %s: %v", oldPath, err)
						return
					}
				}
				vlog("Downloading old module version %s@%s", oldPath, ver)
				regCtx, cancel := registryContext(ctx, registryTimeout)
				err = client.DownloadModule(regCtx, oldPath, ver, tmpOld)
				cancel()
				if err != nil {
					oldErr = fmt.Sprintf("Error checking out old version: %v", err)
					return
				}
				actualOld = tmpOld
				if sub := registry.SubmodulePath(oldPath); sub != "" {
					actualOld = filepath.Join(tmpOld, sub)
				}
			} else if isGitURL(oldPath) || (oldVersion != "" && isGitRepo(oldPath)) {
				vlog("Checking out old repository version %s from %s", oldVersion, oldPath)
				tmpOld, err := gitops.GlobalCache.GetOrExtract(oldPath, oldVersion, newCachedDir("tf_old_*"))
				if err != nil {
					oldErr = fmt.Sprintf("Error checking out old version: %v", err)
					return
				}
				actualOld = tmpOld
			}
		}()
	}

	if newInfo == nil {
		fetchWG.Add(1)
		go func() {
			defer fetchWG.Done()
			if registry.IsRegistrySource(newPath) {
				tmpNew, err := mkdirTemp("", "tf_registry_new_*")
				if err != nil {
					newErr = fmt.Sprintf("Error creating temp dir: %v", err)
					return
				}
				cleanupMu.Lock()
				cleanupDirs = append(cleanupDirs, tmpNew)
				cleanupMu.Unlock()
				client := registryClient(registryTimeout)
				ver := newVersion
				if ver == "" {
					regCtx, cancel := registryContext(ctx, registryTimeout)
					ver, err = client.GetLatestVersion(regCtx, newPath)
					cancel()
					if err != nil {
						newErr = fmt.Sprintf("Error resolving the latest version of %s: %v", newPath, err)
						return
					}
				}
				vlog("Downloading new module version %s@%s", newPath, ver)
				regCtx, cancel := registryContext(ctx, registryTimeout)
				err = client.DownloadModule(regCtx, newPath, ver, tmpNew)
				cancel()
				if err != nil {
					newErr = fmt.Sprintf("Error checking out new version: %v", err)
					return
				}
				actualNew = tmpNew
				if sub := registry.SubmodulePath(newPath); sub != "" {
					actualNew = filepath.Join(tmpNew, sub)
				}
			} else if isGitURL(newPath) || (newVersion != "" && isGitRepo(newPath)) {
				vlog("Checking out new repository version %s from %s", newVersion, newPath)
				tmpNew, err := gitops.GlobalCache.GetOrExtract(newPath, newVersion, newCachedDir("tf_new_*"))
				if err != nil {
					newErr = fmt.Sprintf("Error checking out new version: %v", err)
					return
				}
				actualNew = tmpNew
			}
		}()
	}
	fetchWG.Wait()

	if oldErr != "" {
		_, _ = fmt.Fprintln(stderr, oldErr)
		return 1
	}
	if newErr != "" {
		_, _ = fmt.Fprintln(stderr, newErr)
		return 1
	}

	var inspectDirs []string
	if oldInfo == nil {
		inspectDirs = append(inspectDirs, actualOld)
	}
	if newInfo == nil {
		inspectDirs = append(inspectDirs, actualNew)
	}
	if len(inspectDirs) > 0 {
		vlog("Inspecting modules in parallel: %s", strings.Join(inspectDirs, " and "))
		modules, err := ast.InspectModulesParallel(ctx, inspectDirs, len(inspectDirs))
		if err != nil {
			if strings.Contains(err.Error(), actualOld) {
				_, _ = fmt.Fprintf(stderr, "Error comparing modules: inspecting old module %s: %v\n", actualOld, err)
			} else {
				_, _ = fmt.Fprintf(stderr, "Error comparing modules: inspecting new module %s: %v\n", actualNew, err)
			}
			return 1
		}
		idx := 0
		if oldInfo == nil {
			oldInfo = modules[idx]
			idx++
			if oldVersion != "" {
				ast.CacheVersionModule(oldPath, oldVersion, oldInfo)
			}
		}
		if newInfo == nil {
			newInfo = modules[idx]
			if newVersion != "" {
				ast.CacheVersionModule(newPath, newVersion, newInfo)
			}
		}
	}

	if checkRecreation && dynamicRegistry == nil {
		dynamicRegistry, _ = recreation.AutoDiscoverSchemas(newInfo, ast.GetPersistentCacheDir())
	}

	vlog("Comparing modules: %s -> %s", oldVersion, newVersion)
	diffReport := diff.Compare(oldInfo, newInfo, oldVersion, newVersion, dynamicRegistry)
	// Identify the compared module by the source the user asked for, so reports and SARIF
	// artifact locations name the module instead of a temporary extraction directory.
	comparedSource := comparisonSource(newPath, oldPath)
	if comparedSource != "" {
		diffReport.ModuleName = comparedSource
	}
	if analyzeUpgradePath {
		vlog("Analyzing upgrade path between %s and %s", oldVersion, newVersion)
		pathReport, err := analyzeUpgradePathVersions(ctx, registryTimeout, oldPath, newPath, oldVersion, newVersion, oldInfo, newInfo, dynamicRegistry)
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
					Report:          step.Diff,
				})...)
			}
		}
		if regoPolicy != nil {
			for _, step := range pathReport.Steps {
				pathReport.PolicyViolations = append(pathReport.PolicyViolations, policy.EvaluateRego(regoPolicy, policy.Input{
					BreakingChanges: step.Diff.TotalBreaking,
					Warnings:        step.Diff.TotalWarnings,
					IsDowngrade:     step.Diff.IsDowngrade,
					BumpType:        string(step.Diff.BumpType),
					Report:          step.Diff,
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
	if policyConfig != nil || regoPolicy != nil {
		input := policy.Input{
			BreakingChanges: diffReport.TotalBreaking,
			Warnings:        diffReport.TotalWarnings,
			IsDowngrade:     diffReport.IsDowngrade,
			BumpType:        string(diffReport.BumpType),
			Report:          diffReport,
		}
		if policyConfig != nil {
			diffReport.PolicyViolations = append(diffReport.PolicyViolations, policy.Evaluate(policyConfig, input)...)
		}
		if regoPolicy != nil {
			diffReport.PolicyViolations = append(diffReport.PolicyViolations, policy.EvaluateRego(regoPolicy, input)...)
		}
	}

	findings := report.Findings{
		AddressShifts: diffReport.AddressShifts,
	}
	if len(findings.AddressShifts) == 0 && oldInfo != nil && newInfo != nil {
		findings.AddressShifts = ast.DetectAddressShifts(oldInfo, newInfo)
	}
	if checkRecreation {
		findings.RecreationHazards = recreation.AnalyzeHazardsWithRegistry(diffReport, dynamicRegistry)
	}
	if svgBefore != "" {
		data, _ := visualize.RenderBefore(oldInfo, visualize.Options{Title: "Before"})
		if err := os.WriteFile(filepath.Clean(svgBefore), data, 0600); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error writing SVG: %v\n", err)
			return 1
		}
	}
	if svgAfter != "" {
		data, _ := visualize.RenderAfter(newInfo, visualize.Options{Title: "After"})
		if err := os.WriteFile(filepath.Clean(svgAfter), data, 0600); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error writing SVG: %v\n", err)
			return 1
		}
	}
	if svgTransition != "" {
		data, _ := visualize.RenderTransition(oldInfo, newInfo, diffReport.AddressShifts, visualize.Options{Title: "Transition"})
		if err := os.WriteFile(filepath.Clean(svgTransition), data, 0600); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error writing SVG: %v\n", err)
			return 1
		}
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
		if findings.Taint != nil {
			out += "\n" + report.FormatTaintMarkdown(findings.Taint)
		}
		if findings.GoldenPlan != nil {
			out += "\n" + recreation.FormatGoldenPlanMarkdown(findings.GoldenPlan)
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

func analyzeUpgradePathVersions(ctx context.Context, registryTimeout time.Duration, source, newSource, oldVersion, newVersion string, oldInfo, newInfo *ast.ModuleInfo, hazardLookups ...ast.HazardLookup) (*planner.UpgradePathReport, error) {
	if (registry.IsRegistrySource(source) || isGitURL(source) || isGitRepo(source)) && source != newSource {
		return nil, fmt.Errorf("intermediate version analysis requires --old and --new to use the same source")
	}
	regCtx, cancel := registryContext(ctx, registryTimeout)
	defer cancel()
	versions, err := planner.NewUpgradePlanner(registryClient(registryTimeout)).PlanUpgrade(regCtx, source, oldVersion, newVersion)
	if err != nil {
		return nil, err
	}
	snapshots := make([]planner.VersionSnapshot, len(versions))
	var intermediateIndices []int

	for i, version := range versions {
		switch {
		case semverutil.Compare(version, oldVersion) == 0:
			snapshots[i] = planner.VersionSnapshot{Version: version, Module: oldInfo}
		case semverutil.Compare(version, newVersion) == 0:
			snapshots[i] = planner.VersionSnapshot{Version: version, Module: newInfo}
		default:
			if cached, ok := ast.CachedVersionModule(source, version); ok {
				snapshots[i] = planner.VersionSnapshot{Version: version, Module: cached}
				continue
			}
			intermediateIndices = append(intermediateIndices, i)
		}
	}

	if len(intermediateIndices) > 0 {
		intermediateDirs := make([]string, len(intermediateIndices))
		var cleanupMu sync.Mutex
		var cleanupDirs []string
		defer func() {
			for _, d := range cleanupDirs {
				_ = os.RemoveAll(d)
			}
		}()

		workers := min(8, runtime.GOMAXPROCS(0))
		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup
		var firstErr error
		var errOnce sync.Once

		for idx, origIdx := range intermediateIndices {
			ver := versions[origIdx]
			wg.Add(1)
			go func(slot int, v string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				dir, isTemp, err := resolveModuleVersionDir(regCtx, registryTimeout, source, v)
				if err != nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("retrieve intermediate version %s: %w", v, err)
					})
					return
				}
				if isTemp {
					cleanupMu.Lock()
					cleanupDirs = append(cleanupDirs, dir)
					cleanupMu.Unlock()
				}
				intermediateDirs[slot] = dir
			}(idx, ver)
		}
		wg.Wait()
		if firstErr != nil {
			return nil, firstErr
		}

		inspected, err := inspectModulesParallel(ctx, intermediateDirs, workers)
		if err != nil {
			for idx, dir := range intermediateDirs {
				if strings.Contains(err.Error(), dir) {
					origIdx := intermediateIndices[idx]
					return nil, fmt.Errorf("inspect intermediate version %s: %w", versions[origIdx], err)
				}
			}
			return nil, fmt.Errorf("inspect intermediate versions: %w", err)
		}
		for idx, mod := range inspected {
			origIdx := intermediateIndices[idx]
			snapshots[origIdx] = planner.VersionSnapshot{Version: versions[origIdx], Module: mod}
			ast.CacheVersionModule(source, versions[origIdx], mod)
		}
	}

	return planner.AnalyzeUpgradePath(snapshots, hazardLookups...)
}

func resolveModuleVersionDir(ctx context.Context, registryTimeout time.Duration, source, version string) (string, bool, error) {
	if registry.IsRegistrySource(source) {
		directory, err := mkdirTemp("", "tfastdiff_upgrade_step_*")
		if err != nil {
			return "", false, fmt.Errorf("create temporary module directory: %w", err)
		}
		regCtx, cancel := registryContext(ctx, registryTimeout)
		defer cancel()
		if err := registryClient(registryTimeout).DownloadModule(regCtx, source, version, directory); err != nil {
			_ = os.RemoveAll(directory)
			return "", false, fmt.Errorf("download registry module %s@%s: %w", source, version, err)
		}
		return directory, true, nil
	}
	if isGitURL(source) || isGitRepo(source) {
		directory, err := gitops.GlobalCache.GetOrExtract(source, version, newCachedDir("tfastdiff_upgrade_step_*"))
		if err != nil {
			return "", false, fmt.Errorf("checkout repository version %s: %w", version, err)
		}
		return directory, false, nil
	}
	return "", false, fmt.Errorf("cannot retrieve intermediate version %s from a non-versioned local directory", version)
}

// newCachedDir builds the directory factory handed to the shared repository cache so that
// temporary directory creation stays under the CLI's own (injectable) mkdirTemp seam.
func newCachedDir(pattern string) gitops.DirFactory {
	return func() (string, error) {
		return mkdirTemp("", pattern)
	}
}

// isGitURL reports whether path is a remote VCS source. The predicate lives in gitops so the CLI,
// the planner and the clone path all agree on what counts as a git source.
func isGitURL(path string) bool {
	return gitops.IsGitURL(path)
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
		// #nosec G306 -- reports are intended to be readable by downstream CI tooling
		return os.WriteFile(filepath.Clean(outputFile), []byte(content), 0644)
	}
	_, err := fmt.Fprint(stdout, content)
	return err
}
