package recreation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
)

func TestAutodiscoverGoldenPlan(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Empty directory -> returns empty string
	got, err := AutodiscoverGoldenPlan(tempDir)
	if err != nil || got != "" {
		t.Fatalf("expected empty string and nil error, got %q, %v", got, err)
	}

	// 2. Candidate exists but is a directory -> skipped
	dirCandidate := filepath.Join(tempDir, "golden-plan.json")
	if err := os.Mkdir(dirCandidate, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = AutodiscoverGoldenPlan(tempDir)
	if err != nil || got != "" {
		t.Fatalf("expected empty when candidate is dir, got %q, %v", got, err)
	}
	_ = os.Remove(dirCandidate)

	// 3. Candidate exists but has invalid JSON -> skipped
	if err := os.WriteFile(dirCandidate, []byte("not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = AutodiscoverGoldenPlan(tempDir)
	if err != nil || got != "" {
		t.Fatalf("expected empty when candidate is invalid JSON, got %q, %v", got, err)
	}
	_ = os.Remove(dirCandidate)

	// 4. Candidate exists with empty JSON object {} (no recognized plan fields) -> skipped
	if err := os.WriteFile(dirCandidate, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = AutodiscoverGoldenPlan(tempDir)
	if err != nil || got != "" {
		t.Fatalf("expected empty when candidate is empty JSON, got %q, %v", got, err)
	}
	_ = os.Remove(dirCandidate)

	// 5. Valid plan JSON in golden_plan.json (second candidate)
	validPlan := `{"format_version": "1.0", "resource_changes": []}`
	secondCand := filepath.Join(tempDir, "golden_plan.json")
	if err := os.WriteFile(secondCand, []byte(validPlan), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = AutodiscoverGoldenPlan(tempDir)
	if err != nil || got != secondCand {
		t.Fatalf("expected %q, got %q, err: %v", secondCand, got, err)
	}

	// 6. Subdirectory candidate .terraform/tfplan.json
	tfDir := filepath.Join(tempDir, "sub", ".terraform")
	if err := os.MkdirAll(tfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	subPlan := filepath.Join(tfDir, "tfplan.json")
	if err := os.WriteFile(subPlan, []byte(`{"resources": [{"type": "aws_s3_bucket"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = AutodiscoverGoldenPlan(filepath.Join(tempDir, "sub"))
	if err != nil || got != subPlan {
		t.Fatalf("expected %q, got %q, err: %v", subPlan, got, err)
	}
}

func TestIsValidPlanJSON(t *testing.T) {
	if isValidPlanJSON([]byte("bad json")) {
		t.Errorf("expected bad json to be invalid")
	}
	if isValidPlanJSON([]byte("{}")) {
		t.Errorf("expected empty json to be invalid")
	}
	if !isValidPlanJSON([]byte(`{"format_version": "1.2"}`)) {
		t.Errorf("expected format_version to be valid")
	}
	if !isValidPlanJSON([]byte(`{"resource_changes": [{}]}`)) {
		t.Errorf("expected resource_changes to be valid")
	}
	if !isValidPlanJSON([]byte(`{"planned_values": {}}`)) {
		t.Errorf("expected planned_values to be valid")
	}
	if !isValidPlanJSON([]byte(`{"resources": [{}]}`)) {
		t.Errorf("expected resources to be valid")
	}
}

func TestExtractPlannedResources(t *testing.T) {
	plan := &TerraformPlanJSON{
		ResourceChanges: []PlanResourceChange{
			{
				Address: "aws_s3_bucket.main",
				Type:    "aws_s3_bucket",
				Name:    "main",
				Mode:    "managed",
				Change: PlanChangeBlock{
					Actions: []string{"update"},
				},
			},
			{
				Address: "data.aws_caller_identity.current",
				Type:    "aws_caller_identity",
				Name:    "current",
				Mode:    "data",
			},
			{
				Address: "",
				Type:    "google_storage_bucket",
				Name:    "backup",
				Mode:    "",
			},
		},
		Resources: []PlanResourceBlock{
			{
				Address: "aws_s3_bucket.main", // duplicate address, should be deduped
				Type:    "aws_s3_bucket",
				Name:    "main",
				Mode:    "managed",
			},
			{
				Address: "data.terraform_remote_state.foo",
				Type:    "terraform_remote_state",
				Name:    "foo",
				Mode:    "data", // skipped
			},
			{
				Address: "azurerm_resource_group.rg",
				Type:    "azurerm_resource_group",
				Name:    "rg",
			},
		},
		PlannedValues: &PlanPlannedValues{
			RootModule: PlanModuleBlock{
				Resources: []PlanResourceBlock{
					{
						Address: "google_compute_instance.vm",
						Type:    "google_compute_instance",
						Name:    "vm",
						Mode:    "managed",
					},
					{
						Address: "data.google_client_config.default",
						Type:    "google_client_config",
						Name:    "default",
						Mode:    "data",
					},
				},
				ChildModules: []PlanModuleBlock{
					{
						Resources: []PlanResourceBlock{
							{
								Address: "module.nested.aws_dynamodb_table.tbl",
								Type:    "aws_dynamodb_table",
								Name:    "tbl",
								Mode:    "managed",
							},
						},
					},
				},
			},
		},
	}

	resources := ExtractPlannedResources(plan)
	if len(resources) != 5 {
		t.Fatalf("expected 5 distinct resources, got %d", len(resources))
	}

	expectedAddrs := []string{
		"aws_s3_bucket.main",
		"azurerm_resource_group.rg",
		"google_compute_instance.vm",
		"google_storage_bucket.backup",
		"module.nested.aws_dynamodb_table.tbl",
	}

	for i, r := range resources {
		if r.Address != expectedAddrs[i] {
			t.Errorf("at index %d: expected %s, got %s", i, expectedAddrs[i], r.Address)
		}
	}
}

func TestVerifyGoldenPlanFile(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Missing file -> error
	_, err := VerifyGoldenPlanFile(filepath.Join(tempDir, "nonexistent.json"), false, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing file")
	}

	// 2. Invalid JSON -> error
	invalidPath := filepath.Join(tempDir, "invalid.json")
	if err := os.WriteFile(invalidPath, []byte("bad-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = VerifyGoldenPlanFile(invalidPath, false, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}

	// 3. Valid plan file
	validPath := filepath.Join(tempDir, "valid.json")
	planJSON := `{
		"format_version": "1.0",
		"resource_changes": [
			{
				"address": "aws_s3_bucket.b",
				"type": "aws_s3_bucket",
				"name": "b",
				"change": {
					"actions": ["no-op"]
				}
			}
		]
	}`
	if err := os.WriteFile(validPath, []byte(planJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	ver, err := VerifyGoldenPlanFile(validPath, true, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ver == nil || !ver.Autodiscovered || ver.TotalPlannedResources != 1 || ver.HasRecreationHazards {
		t.Fatalf("unexpected verification result: %+v", ver)
	}
}

func TestVerifyGoldenPlan_HazardsAndActions(t *testing.T) {
	plan := &TerraformPlanJSON{
		ResourceChanges: []PlanResourceChange{
			{
				Address: "module.cluster.google_container_cluster.primary",
				Type:    "google_container_cluster",
				Name:    "primary",
				Change: PlanChangeBlock{
					Actions: []string{"delete", "create"},
				},
			},
			{
				Address: "aws_instance.server",
				Type:    "aws_instance",
				Name:    "server",
				Change: PlanChangeBlock{
					Actions: []string{"update"},
				},
			},
			{
				Address: "aws_db_instance.db",
				Type:    "aws_db_instance",
				Name:    "db",
				Change: PlanChangeBlock{
					Actions: []string{"create"},
				},
			},
		},
	}

	hazards := []RecreationHazard{
		{
			Resource:    "google_container_cluster",
			Variable:    "ip_range_pods",
			Severity:    "CRITICAL_HAZARD",
			HazardType:  "ForceNewClusterRecreation",
			Impact:      "Modifying secondary pod IP range forces destruction of the entire GKE cluster.",
			Remediation: "Ensure secondary CIDR allocations remain immutable.",
		},
	}

	shifts := []ast.ResourceAddressShift{
		{
			OldAddress: ast.ResourceAddress{
				Type: "aws_instance",
				Name: "server",
			},
			NewAddress: ast.ResourceAddress{
				ModulePath: "module.compute",
				Type:       "aws_instance",
				Name:       "server",
			},
			IsRecreation: true,
			Reason:       "refactored into module without moved block",
		},
		{
			OldAddress: ast.ResourceAddress{
				Type: "aws_db_instance",
				Name: "db",
			},
			NewAddress: ast.ResourceAddress{
				Type: "aws_db_instance",
				Name: "db",
			},
			IsRecreation: false, // not a recreation hazard
		},
	}

	wReport := &wrapper.WrapperCompatibilityReport{}
	ver := VerifyGoldenPlan("plan.json", false, plan, wReport, hazards, shifts)

	if !ver.HasRecreationHazards {
		t.Fatalf("expected recreation hazards, got none")
	}
	if ver.TotalPlannedResources != 3 {
		t.Fatalf("expected 3 planned resources, got %d", ver.TotalPlannedResources)
	}

	// Should have:
	// 1. PlanActionReplacement for google_container_cluster
	// 2. ForceNewClusterRecreation for google_container_cluster
	// 3. AddressShiftRecreation for aws_instance.server
	if len(ver.ConfirmedHazards) != 3 {
		t.Fatalf("expected 3 confirmed hazards, got %d: %+v", len(ver.ConfirmedHazards), ver.ConfirmedHazards)
	}

	// Verify deduplication
	ver2 := VerifyGoldenPlan("plan.json", false, plan, wReport, append(hazards, hazards[0]), shifts)
	if len(ver2.ConfirmedHazards) != 3 {
		t.Fatalf("expected deduplication to preserve 3 hazards, got %d", len(ver2.ConfirmedHazards))
	}
}

func TestIsReplacementAction(t *testing.T) {
	if isReplacementAction(nil) {
		t.Error("nil actions should not be replacement")
	}
	if isReplacementAction([]string{"create"}) {
		t.Error("single action should not be replacement")
	}
	if isReplacementAction([]string{"no-op", "read"}) {
		t.Error("no-op/read should not be replacement")
	}
	if isReplacementAction([]string{"delete", "update"}) {
		t.Error("delete/update should not be replacement")
	}
	if !isReplacementAction([]string{"delete", "create"}) {
		t.Error("delete, create should be replacement")
	}
	if !isReplacementAction([]string{"create", "delete"}) {
		t.Error("create, delete should be replacement")
	}
}

func TestFormatGoldenPlanTextAndMarkdown(t *testing.T) {
	// 1. Nil checks
	if got := FormatGoldenPlanText(nil); got != "" {
		t.Errorf("expected empty text for nil, got %q", got)
	}
	if got := FormatGoldenPlanMarkdown(nil); got != "" {
		t.Errorf("expected empty markdown for nil, got %q", got)
	}

	// 2. Verification with hazards (explicit)
	verWithHazards := &GoldenPlanVerification{
		PlanPath:              "/path/to/tfplan.json",
		Autodiscovered:        false,
		TotalPlannedResources: 2,
		HasRecreationHazards:  true,
		Summary:               "2 confirmed hazards",
		ConfirmedHazards: []PlanRecreationHazard{
			{
				ResourceAddress: "google_container_cluster.primary",
				ResourceType:    "google_container_cluster",
				HazardType:      "ForceNew",
				Severity:        "CRITICAL_HAZARD",
				Detail:          "Causes destruction",
				Remediation:     "Keep CIDR stable",
			},
			{
				ResourceAddress: "aws_s3_bucket.data",
				ResourceType:    "aws_s3_bucket",
				HazardType:      "ForceNew",
				Severity:        "HIGH",
				Detail:          "Bucket name changed",
				Remediation:     "", // empty remediation
			},
		},
	}

	textOut := FormatGoldenPlanText(verWithHazards)
	if !strings.Contains(textOut, "GOLDEN PLAN STATE RECREATION VERIFICATION") ||
		!strings.Contains(textOut, "(explicit)") ||
		!strings.Contains(textOut, "Remediation: Keep CIDR stable") ||
		!strings.Contains(textOut, "google_container_cluster.primary") {
		t.Errorf("unexpected text output:\n%s", textOut)
	}

	mdOut := FormatGoldenPlanMarkdown(verWithHazards)
	if !strings.Contains(mdOut, "### Golden Plan State Recreation Verification") ||
		!strings.Contains(mdOut, "(explicit)") ||
		!strings.Contains(mdOut, "[!CAUTION]") ||
		!strings.Contains(mdOut, "google_container_cluster.primary") {
		t.Errorf("unexpected markdown output:\n%s", mdOut)
	}

	// 3. Verification without hazards (autodiscovered)
	verClean := &GoldenPlanVerification{
		PlanPath:              "golden-plan.json",
		Autodiscovered:        true,
		TotalPlannedResources: 5,
		HasRecreationHazards:  false,
		Summary:               "0 hazards detected",
	}

	cleanText := FormatGoldenPlanText(verClean)
	if !strings.Contains(cleanText, "(autodiscovered)") ||
		strings.Contains(cleanText, "Confirmed recreation hazards") {
		t.Errorf("unexpected clean text output:\n%s", cleanText)
	}

	cleanMd := FormatGoldenPlanMarkdown(verClean)
	if !strings.Contains(cleanMd, "(autodiscovered)") ||
		!strings.Contains(cleanMd, "[!NOTE]") {
		t.Errorf("unexpected clean markdown output:\n%s", cleanMd)
	}
}

func FuzzParseGoldenPlan(f *testing.F) {
	for _, seed := range []string{
		`{"format_version": "1.0", "resource_changes": []}`,
		`{"resources": [{"address": "aws_s3_bucket.b", "type": "aws_s3_bucket", "name": "b"}]}`,
		`{"planned_values": {"root_module": {"resources": []}}}`,
		"{}",
		"invalid-json",
		"",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data string) {
		_ = isValidPlanJSON([]byte(data))
		var plan TerraformPlanJSON
		if err := json.Unmarshal([]byte(data), &plan); err == nil {
			_ = ExtractPlannedResources(&plan)
			_ = VerifyGoldenPlan("fuzz.json", false, &plan, nil, nil, nil)
		}
	})
}
