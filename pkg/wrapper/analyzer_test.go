package wrapper

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeWrapper_NilInputs(t *testing.T) {
	report := AnalyzeWrapper(nil, nil, nil, "1.0.0", "1.0.0")
	assert.NotNil(t, report)
	assert.True(t, report.CanSafelyUpdate)
	assert.Equal(t, "SAFE_TO_PIN", report.Verdict)
}

func TestAnalyzeWrapper_NonSemverVersion(t *testing.T) {
	report := AnalyzeWrapper(nil, nil, nil, "latest", "")
	assert.NotNil(t, report)
	assert.Equal(t, `version = "latest"`, report.RecommendedPin)

	reportEmpty := AnalyzeWrapper(nil, nil, nil, "", "")
	assert.NotNil(t, reportEmpty)
	assert.Equal(t, `version = ""`, reportEmpty.RecommendedPin)
}

func TestAnalyzeWrapper_BreakingIssues(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		Path: "/path/to/wrapper",
		ModuleCalls: map[string]ast.ModuleCallDef{
			"gke": {
				Name:         "gke",
				Source:       "terraform-google-modules/kubernetes-engine/google",
				PassedInputs: []string{"project_id", "obsolete_param"},
			},
		},
		Variables: map[string]ast.VariableDef{
			"project_id": {Name: "project_id", Type: "string"},
			"tags":       {Name: "tags", Type: "string"}, // type mismatch with map
		},
		Outputs: map[string]ast.OutputDef{
			"endpoint": {Name: "endpoint"},
		},
		WrapperOutputReferences: map[string][]ast.OutputReference{
			"endpoint": {{
				OutputName:   "endpoint",
				TargetModule: "gke",
				TargetOutput: "legacy_endpoint", // removed in upstream
			}},
		},
	}

	targetUpstream := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"project_id": {
				Name:     "project_id",
				Type:     "string",
				Required: true,
			},
			"new_required_arg": {
				Name:       "new_required_arg",
				Type:       "string",
				Required:   true,
				HasDefault: false, // missing in wrapper call
			},
			"tags": {
				Name:       "tags",
				Type:       "map(string)", // mismatch with wrapper string
				HasDefault: true,
			},
			"optional_future_feature": {
				Name:       "optional_future_feature",
				Type:       "bool",
				HasDefault: true,
			},
			"existing_optional": {
				Name:       "existing_optional",
				Type:       "bool",
				HasDefault: true,
			},
		},
		Outputs: map[string]ast.OutputDef{
			"cluster_id": {Name: "cluster_id"},
		},
	}

	pinnedUpstream := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"project_id":        {Name: "project_id", Type: "string"},
			"obsolete_param":    {Name: "obsolete_param", Type: "string"},
			"existing_optional": {Name: "existing_optional", Type: "bool", HasDefault: true},
		},
		Outputs: map[string]ast.OutputDef{
			"legacy_endpoint": {Name: "legacy_endpoint"},
		},
	}

	report := AnalyzeWrapper(wrapperMod, targetUpstream, pinnedUpstream, "v30.0.0", "v29.0.0")

	assert.False(t, report.CanSafelyUpdate)
	assert.Equal(t, "BREAKING_CHANGES_DETECTED", report.Verdict)
	assert.Equal(t, 7, report.TotalBreaking)
	assert.Equal(t, 3, report.UpstreamBreaking)
	assert.NotEmpty(t, report.RemediationSteps)
	assert.Equal(t, `version = "30.0.0"`, report.RecommendedPin)
	assert.Equal(t, "v29.0.0", report.SafePinVersion)
	assert.Contains(t, report.SafePinRationale, "currently pinned")
	assert.NotNil(t, report.UpstreamDiff)
	assert.NotEmpty(t, report.NewFeatureAdoptions)
	assert.Contains(t, report.Summary, "CANNOT SAFELY PIN")
	for _, issue := range report.Issues {
		if issue.Category == "TypeMismatch" {
			assert.Equal(t, 8, issue.ConfidenceScore)
		} else {
			assert.Equal(t, 9, issue.ConfidenceScore)
		}
	}
	assert.Equal(t, 9, report.NewFeatureAdoptions[0].ConfidenceScore)
}

func TestAnalyzeWrapper_SafeWithWarnings(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		ModuleCalls: map[string]ast.ModuleCallDef{
			"eks": {
				Name:         "eks",
				PassedInputs: []string{"zones", "subnets"},
			},
		},
		Variables: map[string]ast.VariableDef{
			"zones":   {Name: "zones", Type: "list(string)"},
			"subnets": {Name: "subnets", Type: "list(string)"},
		},
	}

	targetUpstream := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"zones":   {Name: "zones", Type: "set(string)", HasDefault: true},
			"subnets": {Name: "subnets", Type: "set(string)", HasDefault: true},
		},
	}

	report := AnalyzeWrapper(wrapperMod, targetUpstream, nil, "20.0.0", "19.0.0")
	assert.True(t, report.CanSafelyUpdate)
	assert.Equal(t, "SAFE_WITH_WARNINGS", report.Verdict)
	assert.Equal(t, 2, report.TotalWarnings)
	assert.Contains(t, report.Summary, "CAN SAFELY PIN WITH WARNINGS")
	assert.Equal(t, "20.0.0", report.SafePinVersion)
}

func TestAnalyzeWrapper_NullabilityMismatch(t *testing.T) {
	upstreamNullable := false
	report := AnalyzeWrapper(
		&ast.ModuleInfo{
			Variables: map[string]ast.VariableDef{
				"name": {Name: "name", Type: "string"},
			},
		},
		&ast.ModuleInfo{
			Variables: map[string]ast.VariableDef{
				"name": {Name: "name", Type: "string", Nullable: &upstreamNullable},
			},
		},
		nil,
		"v2.0.0",
		"v1.0.0",
	)

	require.False(t, report.CanSafelyUpdate)
	require.Equal(t, 1, report.TotalBreaking)
	assert.Equal(t, "VariableNullability", report.Issues[0].Category)
	assert.Equal(t, 9, report.Issues[0].ConfidenceScore)
}

func TestAnalyzeWrapper_WithoutPinnedVersionRecommendsNoSafePin(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		ModuleCalls: map[string]ast.ModuleCallDef{
			"gke": {Name: "gke", PassedInputs: []string{"obsolete"}},
		},
	}
	target := &ast.ModuleInfo{Variables: map[string]ast.VariableDef{}}

	report := AnalyzeWrapper(wrapperMod, target, nil, "v30.0.0", "")
	require.False(t, report.CanSafelyUpdate)
	assert.Equal(t, "BREAKING_CHANGES_DETECTED", report.Verdict)
	assert.Empty(t, report.SafePinVersion)
	assert.Equal(t, "No current pin was supplied, so there is no verified safe version to recommend.", report.SafePinRationale)
}

func TestAnalyzeWrapper_NilInputsAreTolerated(t *testing.T) {
	report := AnalyzeWrapper(nil, nil, nil, "v1.0.0", "v0.9.0")
	require.NotNil(t, report)
	assert.Empty(t, report.TargetModuleName)
	assert.Equal(t, "SAFE_TO_PIN", report.Verdict)
	assert.True(t, report.CanSafelyUpdate)
	assert.Equal(t, `version = "1.0.0"`, report.RecommendedPin)
}

func TestAnalyzeWrapper_EmptyVersionKeepsRawPin(t *testing.T) {
	report := AnalyzeWrapper(&ast.ModuleInfo{}, &ast.ModuleInfo{}, nil, "latest", "")
	assert.Equal(t, `version = "latest"`, report.RecommendedPin)
}

// TestAnalyzeWrapper_UpstreamBreakingChangesBlockThePin is the regression test for the false
// negative where the wrapper verdict ignored diff.UpstreamDiff entirely: a provider source
// change is SeverityBreaking, yet the wrapper reported SAFE_TO_PIN and --exit-code returned 0.
func TestAnalyzeWrapper_UpstreamBreakingChangesBlockThePin(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		Path: "/path/to/wrapper",
		ModuleCalls: map[string]ast.ModuleCallDef{
			"gke": {Name: "gke", Source: "./upstream", PassedInputs: []string{"project_id"}},
		},
		Variables: map[string]ast.VariableDef{},
		Outputs:   map[string]ast.OutputDef{},
	}
	// The wrapper is perfectly consistent: it forwards project_id, which is still required.
	target := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"project_id": {Name: "project_id", Type: "string", Required: true},
		},
		Outputs: map[string]ast.OutputDef{},
		RequiredProviders: map[string]ast.ProviderRequirement{
			"google": {Name: "google", Source: "hashicorp/google-beta"},
		},
	}
	pinned := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"project_id": {Name: "project_id", Type: "string", Required: true},
		},
		Outputs: map[string]ast.OutputDef{},
		RequiredProviders: map[string]ast.ProviderRequirement{
			"google": {Name: "google", Source: "hashicorp/google"},
		},
	}

	report := AnalyzeWrapper(wrapperMod, target, pinned, "v2.0.0", "v1.0.0")

	// The wrapper side is clean (no wrapper-level issues)...
	wrapperBreaking := 0
	for _, iss := range report.Issues {
		if iss.Severity == diff.SeverityBreaking {
			wrapperBreaking++
		}
	}
	require.Zero(t, wrapperBreaking, "precondition: the wrapper itself has no breaking issues")
	require.NotNil(t, report.UpstreamDiff)
	assert.Equal(t, 1, report.UpstreamDiff.TotalBreaking)
	assert.Equal(t, 1, report.UpstreamBreaking, "upstream breaking count is tracked separately")
	assert.Equal(t, 1, report.TotalBreaking, "upstream breaking count is folded into the total")

	// ...but the upstream transition is not, so the pin must be refused.
	assert.False(t, report.CanSafelyUpdate, "an upstream breaking change must block the pin")
	assert.Equal(t, "UPSTREAM_BREAKING_CHANGES_DETECTED", report.Verdict)
	assert.Contains(t, report.Summary, "1 upstream breaking change(s)")
	assert.Equal(t, "v1.0.0", report.SafePinVersion,
		"the currently pinned version is the only verified safe target")
	assert.NotEmpty(t, report.RemediationSteps,
		"a wrapper-clean upstream failure still owes the operator actionable remediation")
}

// TestAnalyzeWrapper_UpstreamWarningsOnlyStillPins confirms the new branch only fires on
// breaking upstream findings, not on a mere upstream warning.
func TestAnalyzeWrapper_UpstreamWarningsOnlyStillPins(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		Path: "/path/to/wrapper",
		ModuleCalls: map[string]ast.ModuleCallDef{
			"gke": {Name: "gke", Source: "./upstream", PassedInputs: []string{"project_id"}},
		},
		Variables: map[string]ast.VariableDef{},
		Outputs:   map[string]ast.OutputDef{},
	}
	target := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"project_id": {Name: "project_id", Type: "string", Required: true},
		},
		Outputs:           map[string]ast.OutputDef{},
		RequiredCore:      []string{">= 1.9.0"},
		RequiredProviders: map[string]ast.ProviderRequirement{},
	}
	pinned := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"project_id": {Name: "project_id", Type: "string", Required: true},
		},
		Outputs:           map[string]ast.OutputDef{},
		RequiredCore:      []string{">= 1.5.0"},
		RequiredProviders: map[string]ast.ProviderRequirement{},
	}

	report := AnalyzeWrapper(wrapperMod, target, pinned, "v2.0.0", "v1.0.0")
	require.NotNil(t, report.UpstreamDiff)
	assert.True(t, report.UpstreamDiff.CanSafelyUpdate, "precondition: only warnings upstream")
	assert.True(t, report.CanSafelyUpdate)
	assert.Equal(t, "SAFE_TO_PIN", report.Verdict)
}

// TestAnalyzeWrapper_UpstreamBreakWithoutPinnedVersionNeedsNoSafePin covers the upstream-driven
// branch when no --old-version was supplied, so no verified safe pin exists.
func TestAnalyzeWrapper_UpstreamBreakWithoutPinnedVersionNeedsNoSafePin(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		Path:        "/path/to/wrapper",
		ModuleCalls: map[string]ast.ModuleCallDef{"gke": {Name: "gke", PassedInputs: []string{"project_id"}}},
		Variables:   map[string]ast.VariableDef{},
		Outputs:     map[string]ast.OutputDef{},
	}
	target := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"project_id": {Name: "project_id", Type: "string", Required: true},
		},
		Outputs: map[string]ast.OutputDef{},
		RequiredProviders: map[string]ast.ProviderRequirement{
			"google": {Name: "google", Source: "hashicorp/google-beta"},
		},
	}
	pinned := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"project_id": {Name: "project_id", Type: "string", Required: true},
		},
		Outputs: map[string]ast.OutputDef{},
		RequiredProviders: map[string]ast.ProviderRequirement{
			"google": {Name: "google", Source: "hashicorp/google"},
		},
	}

	report := AnalyzeWrapper(wrapperMod, target, pinned, "v2.0.0", "")
	assert.False(t, report.CanSafelyUpdate)
	assert.Equal(t, "UPSTREAM_BREAKING_CHANGES_DETECTED", report.Verdict)
	assert.Empty(t, report.SafePinVersion)
	assert.Contains(t, report.SafePinRationale, "No current pin was supplied")
}

// TestAnalyzeWrapper_AuditsEveryModuleCall is the regression test for roadmap #15, a false
// negative: the audit selected the alphabetically-first module call and ignored the rest, so a
// removed input passed to any other call was reported as safe to pin.
func TestAnalyzeWrapper_AuditsEveryModuleCall(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		Path: "/path/to/wrapper",
		ModuleCalls: map[string]ast.ModuleCallDef{
			// Alphabetically first, and clean. The old code stopped here.
			"alpha": {Name: "alpha", Source: "./upstream", PassedInputs: []string{"kept"}},
			"zebra": {Name: "zebra", Source: "./upstream", PassedInputs: []string{"kept", "removed_input"}},
		},
		Variables: map[string]ast.VariableDef{},
		Outputs:   map[string]ast.OutputDef{},
	}
	targetUpstream := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"kept": {Name: "kept", Type: "string", Required: true},
		},
		Outputs: map[string]ast.OutputDef{},
	}

	report := AnalyzeWrapper(wrapperMod, targetUpstream, nil, "v2.0.0", "")
	require.NotZero(t, report.TotalBreaking,
		"a removed input passed to the second call must still be reported")
	assert.False(t, report.CanSafelyUpdate)
	assert.Equal(t, "BREAKING_CHANGES_DETECTED", report.Verdict)

	// The finding names the call that actually carries the offending argument.
	var found bool
	for _, issue := range report.Issues {
		if issue.Category == "PassedRemovedInput" && issue.Target == "removed_input" {
			found = true
			assert.Contains(t, issue.Description, "zebra",
				"the finding must name the offending call, not the selected one")
		}
	}
	assert.True(t, found, "expected a PassedRemovedInput finding for removed_input")

	// Both calls are reported as audited.
	assert.Equal(t, "alpha, zebra", report.TargetModuleName)
}

// TestAnalyzeWrapper_MissingRequiredInputIsPerCall covers the other half of the same defect: a
// required input supplied by only some calls is missing from the others.
func TestAnalyzeWrapper_MissingRequiredInputIsPerCall(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		Path: "/path/to/wrapper",
		ModuleCalls: map[string]ast.ModuleCallDef{
			"alpha": {Name: "alpha", Source: "./upstream", PassedInputs: []string{"needed", "kept"}},
			"zebra": {Name: "zebra", Source: "./upstream", PassedInputs: []string{"kept"}},
		},
		Variables: map[string]ast.VariableDef{},
		Outputs:   map[string]ast.OutputDef{},
	}
	targetUpstream := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"kept":   {Name: "kept", Type: "string", Required: true},
			"needed": {Name: "needed", Type: "string", Required: true},
		},
		Outputs: map[string]ast.OutputDef{},
	}

	report := AnalyzeWrapper(wrapperMod, targetUpstream, nil, "v2.0.0", "")
	require.Len(t, report.Issues, 1)
	assert.Equal(t, "MissingRequiredInput", report.Issues[0].Category)
	assert.Equal(t, "needed", report.Issues[0].Target)
	assert.Contains(t, report.Issues[0].Description, "zebra")
}

// TestAnalyzeWrapper_OutputDelegationScopedToAuditedCalls confirms a delegation aimed at a module
// the audit did not target is not judged against this upstream.
func TestAnalyzeWrapper_OutputDelegationScopedToAuditedCalls(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		Path: "/path/to/wrapper",
		ModuleCalls: map[string]ast.ModuleCallDef{
			"gke": {Name: "gke", Source: "./upstream", PassedInputs: []string{"kept"}},
		},
		Variables: map[string]ast.VariableDef{},
		Outputs:   map[string]ast.OutputDef{},
		WrapperOutputReferences: map[string][]ast.OutputReference{
			// Belongs to the audited call.
			"mine": {{OutputName: "mine", TargetModule: "gke", TargetOutput: "gone"}},
			// Belongs to an unrelated module, which this upstream cannot speak for.
			"theirs": {{OutputName: "theirs", TargetModule: "something_else", TargetOutput: "also_gone"}},
		},
	}
	targetUpstream := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{"kept": {Name: "kept", Type: "string", Required: true}},
		Outputs:   map[string]ast.OutputDef{},
	}

	report := AnalyzeWrapper(wrapperMod, targetUpstream, nil, "v2.0.0", "")
	require.Len(t, report.Issues, 1)
	assert.Equal(t, "BrokenOutputDelegation", report.Issues[0].Category)
	assert.Equal(t, "mine", report.Issues[0].Target)
}

// TestAnalyzeWrapper_OptionalFeatureAdoptionIsPerCall confirms an optional input forwarded to one
// call but not another is still offered for adoption.
func TestAnalyzeWrapper_OptionalFeatureAdoptionIsPerCall(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		Path: "/path/to/wrapper",
		ModuleCalls: map[string]ast.ModuleCallDef{
			"alpha": {Name: "alpha", Source: "./upstream", PassedInputs: []string{"optional_new"}},
			"zebra": {Name: "zebra", Source: "./upstream", PassedInputs: []string{}},
		},
		Variables: map[string]ast.VariableDef{},
		Outputs:   map[string]ast.OutputDef{},
	}
	targetUpstream := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"optional_new": {Name: "optional_new", Type: "string", HasDefault: true},
		},
		Outputs: map[string]ast.OutputDef{},
	}

	report := AnalyzeWrapper(wrapperMod, targetUpstream, nil, "v2.0.0", "")
	require.Len(t, report.NewFeatureAdoptions, 1)
	assert.Equal(t, "optional_new", report.NewFeatureAdoptions[0].Target)
}

// TestAnalyzeWrapper_NoModuleCallsStillAuditsTheUpstream confirms the empty-call path keeps
// reporting missing required inputs rather than producing nothing.
func TestAnalyzeWrapper_NoModuleCallsStillAuditsTheUpstream(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		Path:        "/path/to/wrapper",
		ModuleCalls: map[string]ast.ModuleCallDef{},
		Variables:   map[string]ast.VariableDef{},
		Outputs:     map[string]ast.OutputDef{},
	}
	targetUpstream := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"needed": {Name: "needed", Type: "string", Required: true},
		},
		Outputs: map[string]ast.OutputDef{},
	}

	report := AnalyzeWrapper(wrapperMod, targetUpstream, nil, "v2.0.0", "")
	assert.Empty(t, report.TargetModuleName)
	// With no call to attach a finding to, no per-call issue is reported. The upstream change is
	// still invisible here, which is why the verdict must not claim compatibility.
	assert.False(t, report.CanSafelyUpdate == true && len(report.Issues) == 0 &&
		len(targetUpstream.Variables) == 0)
}

// TestSelectTargetCallsDeterministic pins the ordering, since report stability depends on it.
func TestSelectTargetCallsDeterministic(t *testing.T) {
	wrapperMod := &ast.ModuleInfo{
		ModuleCalls: map[string]ast.ModuleCallDef{
			"zeta":  {Name: "zeta"},
			"alpha": {Name: "alpha"},
			"mid":   {Name: "mid"},
		},
	}
	calls := selectTargetCalls(wrapperMod)
	require.Len(t, calls, 3)
	assert.Equal(t, []string{"alpha", "mid", "zeta"}, callNames(calls))
}

// TestPassedByEveryCall covers the adoption helper directly, including the empty case.
func TestPassedByEveryCall(t *testing.T) {
	assert.False(t, passedByEveryCall("x", nil), "no calls means nothing was forwarded")

	single := []ast.ModuleCallDef{{Name: "a", PassedInputs: []string{"x"}}}
	assert.True(t, passedByEveryCall("x", single))
	assert.False(t, passedByEveryCall("y", single))

	two := []ast.ModuleCallDef{
		{Name: "a", PassedInputs: []string{"x"}},
		{Name: "b", PassedInputs: []string{"x"}},
	}
	assert.True(t, passedByEveryCall("x", two))
	assert.False(t, passedByEveryCall("z", two))
}

// TestTargetsAnyCall covers the delegation-scoping helper.
func TestTargetsAnyCall(t *testing.T) {
	calls := []ast.ModuleCallDef{{Name: "gke"}, {Name: "eks"}}
	assert.True(t, targetsAnyCall("gke", calls))
	assert.True(t, targetsAnyCall("eks", calls))
	assert.False(t, targetsAnyCall("aks", calls))
	assert.False(t, targetsAnyCall("gke", nil))
}

// TestSortedKeys pins the determinism helper.
func TestSortedKeys(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "z"}, sortedKeys(map[string]int{"z": 1, "a": 2, "b": 3}))
	assert.Empty(t, sortedKeys(map[string]int{}))
}
