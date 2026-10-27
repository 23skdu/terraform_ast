package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/blastradius"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/eval"
	"github.com/23skdu/terraform_ast/pkg/policy"
	"github.com/23skdu/terraform_ast/pkg/recreation"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleDiffReport() *diff.DiffReport {
	return &diff.DiffReport{
		ModuleName:      "terraform-google-modules/kubernetes-engine/google",
		OldVersion:      "v29.0.0",
		NewVersion:      "v30.0.0",
		BumpType:        "MAJOR",
		Verdict:         "BREAKING_CHANGES",
		Summary:         "Detected 1 breaking change(s)",
		IsDowngrade:     false,
		CanSafelyUpdate: false,
		BreakingChanges: []diff.DifferenceItem{
			{
				Category:    "Variable",
				Name:        "ip_range_pods",
				Severity:    diff.SeverityBreaking,
				Description: "Input was removed",
				Remediation: "Remove from wrapper",
			},
		},
		NewFeatures: []diff.DifferenceItem{
			{
				Category:    "Variable",
				Name:        "enable_cost_allocation",
				Severity:    diff.SeverityNewFeature,
				Description: "New optional input",
			},
		},
		Warnings: []diff.DifferenceItem{
			{
				Category:    "Variable",
				Name:        "zones",
				Severity:    diff.SeverityWarning,
				Description: "Widened to set(string)",
				Remediation: "Ensure set validity",
			},
		},
	}
}

func sampleWrapperReport() *wrapper.WrapperCompatibilityReport {
	return &wrapper.WrapperCompatibilityReport{
		WrapperPath:      "/repos/gke-wrapper",
		TargetModuleName: "gke",
		TargetVersion:    "v30.0.0",
		RecommendedPin:   `version = "30.0.0"`,
		Verdict:          "BREAKING_CHANGES_DETECTED",
		Summary:          "Wrapper has 1 breaking issue(s)",
		TotalBreaking:    1,
		Issues: []wrapper.WrapperIssue{
			{
				Category:    "PassedRemovedInput",
				Target:      "ip_range_pods",
				Severity:    diff.SeverityBreaking,
				Description: "Wrapper passes removed argument",
				Remediation: "Remove argument from module block",
			},
		},
		NewFeatureAdoptions: []wrapper.WrapperIssue{
			{
				Category:    "OptionalVariableAvailable",
				Target:      "enable_cost_allocation",
				Severity:    diff.SeverityNewFeature,
				Description: "New optional feature available",
			},
		},
	}
}

func TestRenderText(t *testing.T) {
	assert.Equal(t, "", RenderText(nil, Findings{}))

	r := sampleDiffReport()
	out := RenderText(r, Findings{})
	assert.Contains(t, out, "TERRAFORM AST COMPATIBILITY DIFF REPORT")
	assert.Contains(t, out, "ip_range_pods")
	assert.Contains(t, out, "enable_cost_allocation")
	assert.Contains(t, out, "zones")
}

func TestRenderMarkdown(t *testing.T) {
	assert.Equal(t, "", RenderMarkdown(nil))

	// Caution alert (breaking)
	r := sampleDiffReport()
	out := RenderMarkdown(r)
	assert.Contains(t, out, "# Terraform AST Compatibility Diff Report")
	assert.Contains(t, out, "[!CAUTION]")
	assert.Contains(t, out, "ip_range_pods")
	assert.Contains(t, out, "enable_cost_allocation")
	assert.Contains(t, out, "zones")

	// Warning alert
	rWarning := sampleDiffReport()
	rWarning.BreakingChanges = nil
	outWarning := RenderMarkdown(rWarning)
	assert.Contains(t, outWarning, "[!WARNING]")

	// Note alert
	rSafe := sampleDiffReport()
	rSafe.BreakingChanges = nil
	rSafe.Warnings = nil
	outSafe := RenderMarkdown(rSafe)
	assert.Contains(t, outSafe, "[!NOTE]")
}

func TestConfidenceScoresAreRendered(t *testing.T) {
	report := sampleDiffReport()
	report.BreakingChanges[0].ConfidenceScore = 9
	report.NewFeatures[0].ConfidenceScore = 9
	report.Warnings[0].ConfidenceScore = 7

	text := RenderText(report, Findings{})
	assert.Contains(t, text, "confidence: 9/10")
	assert.Contains(t, text, "confidence: 7/10")
	markdown := RenderMarkdown(report)
	assert.Contains(t, markdown, "Confidence")
	assert.Contains(t, markdown, "9/10")

	jsonBytes := RenderJSON(report, Findings{})
	assert.Contains(t, string(jsonBytes), `"confidence_score": 9`)
	sarifBytes := RenderSARIF(report, Findings{})
	assert.Contains(t, string(sarifBytes), "Confidence: 9/10")
}

func TestRenderJSON(t *testing.T) {
	r := sampleDiffReport()
	data := RenderJSON(r, Findings{})
	assert.Contains(t, string(data), `"ip_range_pods"`)
}

func TestRenderWrapperText(t *testing.T) {
	assert.Equal(t, "", RenderWrapperText(nil, Findings{}))

	w := sampleWrapperReport()
	out := RenderWrapperText(w, Findings{})
	assert.Contains(t, out, "TERRAFORM WRAPPER COMPATIBILITY & PIN AUDIT")
	assert.Contains(t, out, "ip_range_pods")
	assert.Contains(t, out, "enable_cost_allocation")
}

func TestRenderWrapperMarkdown(t *testing.T) {
	assert.Equal(t, "", RenderWrapperMarkdown(nil))

	// Caution alert
	w := sampleWrapperReport()
	out := RenderWrapperMarkdown(w)
	assert.Contains(t, out, "# Terraform Wrapper Compatibility Audit")
	assert.Contains(t, out, "[!CAUTION]")
	assert.Contains(t, out, "ip_range_pods")
	assert.Contains(t, out, "enable_cost_allocation")

	// Warning alert
	wWarning := sampleWrapperReport()
	wWarning.TotalBreaking = 0
	wWarning.TotalWarnings = 1
	outWarning := RenderWrapperMarkdown(wWarning)
	assert.Contains(t, outWarning, "[!WARNING]")

	// Note alert
	wSafe := sampleWrapperReport()
	wSafe.TotalBreaking = 0
	wSafe.TotalWarnings = 0
	outSafe := RenderWrapperMarkdown(wSafe)
	assert.Contains(t, outSafe, "[!NOTE]")
}

func TestRenderWrapperJSON(t *testing.T) {
	w := sampleWrapperReport()
	data := RenderWrapperJSON(w, Findings{})
	assert.Contains(t, string(data), `"ip_range_pods"`)
}

func TestFindingsAreRenderedInEveryFormat(t *testing.T) {
	findings := Findings{
		RecreationHazards: []recreation.RecreationHazard{
			{
				Variable:    "location",
				Resource:    "google_container_cluster",
				HazardType:  "VariableType",
				Severity:    "WARNING_HAZARD",
				Description: "Location is immutable",
				Remediation: "Keep location unchanged",
			},
		},
		AddressShifts: []ast.ResourceAddressShift{
			{
				OldAddress: ast.ResourceAddress{Type: "aws_s3_bucket", Name: "b1"},
				NewAddress: ast.ResourceAddress{Type: "aws_s3_bucket", Name: "b2"},
				Reason:     "renamed",
			},
		},
		BlastRadius: &blastradius.BlastRadiusReport{
			SearchDir:             "/app",
			WrapperIdentifier:     "mod",
			TotalCallSitesScanned: 1,
			CanSafelyUpdateAll:    false,
			HighestRisk:           "HIGH",
			Summary:               "impacts detected",
			ImpactedRootModules: []blastradius.RootModuleImpact{
				{Dir: "/app/prod", ModuleName: "prod_cluster", RiskScore: "HIGH", MissingRequiredInputs: []string{"grant_registry_access"}},
			},
		},
	}

	d := sampleDiffReport()
	text := RenderText(d, findings)
	assert.Contains(t, text, "STATE DESTRUCTION & RECREATION HAZARDS")
	assert.Contains(t, text, "RESOURCE ADDRESS SHIFTS (1):")
	assert.NotContains(t, text, "recreation_hazards")

	md := RenderMarkdown(d, findings)
	assert.Contains(t, md, "### Resource Address Shifts (1)")
	assert.Contains(t, md, "aws_s3_bucket.b1")

	w := sampleWrapperReport()
	wrapped := RenderWrapperText(w, findings)
	assert.Contains(t, wrapped, "STATE DESTRUCTION & RECREATION HAZARDS")
	assert.Contains(t, wrapped, "RESOURCE ADDRESS SHIFTS (1):")
	assert.Contains(t, wrapped, "BLAST-RADIUS DOWNSTREAM CONSUMER ANALYSIS")

	wmd := RenderWrapperMarkdown(w, findings)
	assert.Contains(t, wmd, "### Resource Address Shifts (1)")
	assert.Contains(t, wmd, "aws_s3_bucket.b1")

	// Fallback to report AddressShifts when findings are empty
	dWithShifts := sampleDiffReport()
	dWithShifts.AddressShifts = findings.AddressShifts
	dShiftsText := RenderText(dWithShifts, Findings{})
	assert.Contains(t, dShiftsText, "RESOURCE ADDRESS SHIFTS (1):")
	dShiftsMd := RenderMarkdown(dWithShifts)
	assert.Contains(t, dShiftsMd, "### Resource Address Shifts (1)")

	wWithDiff := sampleWrapperReport()
	wWithDiff.UpstreamDiff = dWithShifts
	wShiftsText := RenderWrapperText(wWithDiff, Findings{})
	assert.Contains(t, wShiftsText, "RESOURCE ADDRESS SHIFTS (1):")
	wShiftsMd := RenderWrapperMarkdown(wWithDiff)
	assert.Contains(t, wShiftsMd, "### Resource Address Shifts (1)")

	dJSON := RenderJSON(d, findings)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(dJSON, &decoded))
	assert.Equal(t, d.ModuleName, decoded["module_name"], "report fields stay at the top level")
	assert.Contains(t, decoded, "recreation_hazards")
	assert.Contains(t, decoded, "blast_radius")

	wJSON := RenderWrapperJSON(w, findings)
	var decodedWrapper map[string]any
	require.NoError(t, json.Unmarshal(wJSON, &decodedWrapper))
	assert.Equal(t, w.Verdict, decodedWrapper["verdict"])
	assert.Contains(t, decodedWrapper, "blast_radius")

	// Empty findings must not change the document shape.
	plainJSON := RenderJSON(d, Findings{})
	var plain map[string]any
	require.NoError(t, json.Unmarshal(plainJSON, &plain))
	assert.NotContains(t, plain, "recreation_hazards")
	assert.NotContains(t, plain, "blast_radius")
}

func TestSafePinAndPolicyViolationsAreRendered(t *testing.T) {
	violations := []policy.Violation{
		{Rule: "block_breaking", Message: "policy blocks upgrades with 1 breaking change(s)"},
		{Rule: "max_bump", Message: "policy allows at most a PATCH version bump, got MAJOR"},
	}

	d := sampleDiffReport()
	d.SafePinVersion = "v29.0.0"
	d.SafePinRationale = "Keep the currently analyzed version pinned."
	d.PolicyViolations = violations

	text := RenderText(d, Findings{})
	assert.Contains(t, text, "Safest pin:   v29.0.0 (Keep the currently analyzed version pinned.)")
	assert.Contains(t, text, "POLICY VIOLATIONS (2)")
	assert.Contains(t, text, "[block_breaking] policy blocks upgrades with 1 breaking change(s)")
	assert.Contains(t, text, "[max_bump] policy allows at most a PATCH version bump, got MAJOR")

	markdown := RenderMarkdown(d)
	assert.Contains(t, markdown, "**Safest Known Pin**: `v29.0.0` — Keep the currently analyzed version pinned.")
	assert.Contains(t, markdown, "[block_breaking] policy blocks upgrades with 1 breaking change(s)")

	sarifLog := unmarshalSARIF(t, RenderSARIF(d, Findings{}))
	require.Len(t, sarifLog.Runs[0].Results, 5)
	assert.Equal(t, rulePolicy, sarifLog.Runs[0].Results[3].RuleID)
	assert.Equal(t, rulePolicy, sarifLog.Runs[0].Results[4].RuleID)

	// The markdown safe-pin line is omitted when no rationale is available.
	noPin := sampleDiffReport()
	assert.NotContains(t, RenderMarkdown(noPin), "Safest Known Pin")
	// The text safe-pin line is omitted when no pin was recommended.
	assert.NotContains(t, RenderText(noPin, Findings{}), "Safest pin:")

	w := sampleWrapperReport()
	w.SafePinVersion = "v29.0.0"
	w.SafePinRationale = "Keep the currently pinned upstream version."
	w.PolicyViolations = violations
	wrappedText := RenderWrapperText(w, Findings{})
	assert.Contains(t, wrappedText, "[block_breaking] policy blocks upgrades")
	assert.Contains(t, wrappedText, "POLICY VIOLATIONS (2)")

	wrappedSARIF := unmarshalSARIF(t, RenderWrapperSARIF(w, Findings{}))
	require.Len(t, wrappedSARIF.Runs[0].Results, 3)
	assert.Equal(t, rulePolicy, wrappedSARIF.Runs[0].Results[1].RuleID)
	assert.Equal(t, rulePolicy, wrappedSARIF.Runs[0].Results[2].RuleID)

	// Empty reports render empty documents rather than panicking.
	assert.Equal(t, "", RenderText(nil, Findings{}))
	assert.Equal(t, "", RenderMarkdown(nil))
	assert.Equal(t, "", RenderWrapperText(nil, Findings{}))
	assert.Equal(t, "", RenderWrapperMarkdown(nil))

	emptySARIFLog := unmarshalSARIF(t, RenderSARIF(nil, Findings{}))
	assert.Empty(t, emptySARIFLog.Runs)
	emptyWrapperSARIFLog := unmarshalSARIF(t, RenderWrapperSARIF(nil, Findings{}))
	assert.Empty(t, emptyWrapperSARIFLog.Runs)
}

func TestTaintRendering(t *testing.T) {
	assert.Equal(t, "", FormatTaintMarkdown(nil))

	deadTaint := &eval.TaintReport{
		TargetVariable: "unused_var",
		IsDeadCode:     true,
	}
	mdDead := FormatTaintMarkdown(deadTaint)
	assert.Contains(t, mdDead, "### Variable Taint Analysis (`var.unused_var`)")
	assert.Contains(t, mdDead, "Unused / dead code")

	w := sampleWrapperReport()
	findingsDead := Findings{Taint: deadTaint}
	textDead := RenderWrapperText(w, findingsDead)
	assert.Contains(t, textDead, "DEAD CODE (no downstream references in /repos/gke-wrapper)")

	liveTaint := &eval.TaintReport{
		TargetVariable:             "active_var",
		IsDeadCode:                 false,
		TracePath:                  []string{"var.active_var", "local.active", "google_container_cluster.primary.name"},
		AffectedResourceAttributes: []string{"google_container_cluster.primary.name"},
	}
	mdLive := FormatTaintMarkdown(liveTaint)
	assert.Contains(t, mdLive, "Reachable")
	assert.Contains(t, mdLive, "Affected Resource Attributes (1)")

	findingsLive := Findings{Taint: liveTaint}
	textLive := RenderWrapperText(w, findingsLive)
	assert.Contains(t, textLive, "Status: REACHABLE")
	assert.Contains(t, textLive, "Affected attributes (1):")
}

func TestSARIF_EdgeCases(t *testing.T) {
	loc1 := sarifLocation("custom.tf")
	assert.Equal(t, 1, loc1[0].PhysicalLocation.Region.StartLine)
	assert.Equal(t, "custom.tf", loc1[0].PhysicalLocation.ArtifactLocation.URI)

	locZero := sarifLocationWithLine("", 0)
	assert.Equal(t, 1, locZero[0].PhysicalLocation.Region.StartLine)
	assert.Equal(t, "main.tf", locZero[0].PhysicalLocation.ArtifactLocation.URI)

	locPos := sarifLocationWithLine("test.tf", 42)
	assert.Equal(t, 42, locPos[0].PhysicalLocation.Region.StartLine)
	assert.Equal(t, "test.tf", locPos[0].PhysicalLocation.ArtifactLocation.URI)

	// Blast radius with incomplete analysis and CanSafelyUpdate = true produces a warning result
	blastIncomplete := &blastradius.BlastRadiusReport{
		ImpactedRootModules: []blastradius.RootModuleImpact{
			{
				Dir:                "/consumer/dir",
				ModuleName:         "consumer_app",
				RiskScore:          "MEDIUM",
				CanSafelyUpdate:    true,
				AnalysisIncomplete: true,
			},
		},
	}
	sarifBytes := RenderWrapperSARIF(sampleWrapperReport(), Findings{BlastRadius: blastIncomplete})
	log := unmarshalSARIF(t, sarifBytes)
	var foundWarning bool
	for _, res := range log.Runs[0].Results {
		if res.RuleID == ruleBlastRadius {
			assert.Equal(t, "warning", res.Level)
			assert.Contains(t, res.Message.Text, "analysis incomplete")
			foundWarning = true
		}
	}
	assert.True(t, foundWarning)
}

func TestGoldenPlanReporting(t *testing.T) {
	planVer := &recreation.GoldenPlanVerification{
		PlanPath:              "tfplan.json",
		Autodiscovered:        true,
		TotalPlannedResources: 3,
		HasRecreationHazards:  true,
		Summary:               "1 hazard found",
		ConfirmedHazards: []recreation.PlanRecreationHazard{
			{
				ResourceAddress: "google_container_cluster.primary",
				ResourceType:    "google_container_cluster",
				HazardType:      "ForceNew",
				Severity:        "CRITICAL_HAZARD",
				Detail:          "IP range changed",
				Remediation:     "Do not modify IP range",
			},
		},
	}

	w := sampleWrapperReport()
	hazards := []recreation.RecreationHazard{
		{
			Variable:    "ip_range_pods",
			Resource:    "google_container_cluster",
			HazardType:  "ForceNew",
			Severity:    "CRITICAL_HAZARD",
			Description: "ForceNew cluster recreation",
			Impact:      "Destroys cluster",
			Remediation: "Keep pinned",
		},
	}
	findings := Findings{
		RecreationHazards: hazards,
		GoldenPlan:        planVer,
	}

	// 1. Text rendering
	text := RenderWrapperText(w, findings)
	assert.Contains(t, text, "GOLDEN PLAN STATE RECREATION VERIFICATION")
	assert.Contains(t, text, "google_container_cluster.primary")

	// 2. Markdown rendering with findings
	md := RenderWrapperMarkdown(w, findings)
	assert.Contains(t, md, "Golden Plan State Recreation Verification")
	assert.Contains(t, md, "google_container_cluster.primary")
	assert.Contains(t, md, "Recreation Hazards")

	diffMd := RenderMarkdown(sampleDiffReport(), findings)
	assert.Contains(t, diffMd, "Golden Plan State Recreation Verification")

	var sb strings.Builder
	writeAddressShiftsMarkdown(&sb, nil)
	assert.Equal(t, "", sb.String())

	// 3. Markdown rendering without findings
	mdNoFindings := RenderWrapperMarkdown(w)
	assert.NotContains(t, mdNoFindings, "Golden Plan State Recreation Verification")

	// 4. Wrapper JSON rendering
	jsonBytes := RenderWrapperJSON(w, findings)
	assert.Contains(t, string(jsonBytes), `"golden_plan"`)

	// 5. Wrapper SARIF rendering
	sarifWrapperBytes := RenderWrapperSARIF(w, findings)
	logWrapper := unmarshalSARIF(t, sarifWrapperBytes)
	var foundPlanHazard bool
	for _, res := range logWrapper.Runs[0].Results {
		if res.RuleID == rulePlanRecreationHazard {
			foundPlanHazard = true
			assert.Equal(t, "error", res.Level)
			assert.Contains(t, res.Message.Text, "google_container_cluster.primary")
		}
	}
	assert.True(t, foundPlanHazard)

	// 6. Diff SARIF rendering
	diffSARIFBytes := RenderSARIF(sampleDiffReport(), findings)
	logDiff := unmarshalSARIF(t, diffSARIFBytes)
	var foundPlanDiffHazard bool
	for _, res := range logDiff.Runs[0].Results {
		if res.RuleID == rulePlanRecreationHazard {
			foundPlanDiffHazard = true
			assert.Equal(t, "error", res.Level)
			assert.Contains(t, res.Message.Text, "google_container_cluster.primary")
		}
	}
	assert.True(t, foundPlanDiffHazard)
}

func unmarshalSARIF(t *testing.T, data []byte) SARIFLog {
	t.Helper()
	var log SARIFLog
	require.NoError(t, json.Unmarshal(data, &log))
	return log
}
