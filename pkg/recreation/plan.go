package recreation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
)

// TerraformPlanJSON models the relevant fields from `terraform show -json <planfile>`.
type TerraformPlanJSON struct {
	FormatVersion    string               `json:"format_version,omitempty"`
	TerraformVersion string               `json:"terraform_version,omitempty"`
	ResourceChanges  []PlanResourceChange `json:"resource_changes,omitempty"`
	PlannedValues    *PlanPlannedValues   `json:"planned_values,omitempty"`
	Resources        []PlanResourceBlock  `json:"resources,omitempty"`
}

// PlanResourceChange represents a planned resource transition in a Terraform plan.
type PlanResourceChange struct {
	Address       string          `json:"address"`
	ModuleAddress string          `json:"module_address,omitempty"`
	Mode          string          `json:"mode,omitempty"`
	Type          string          `json:"type"`
	Name          string          `json:"name"`
	ProviderName  string          `json:"provider_name,omitempty"`
	Change        PlanChangeBlock `json:"change"`
}

// PlanChangeBlock contains the planned actions and values.
type PlanChangeBlock struct {
	Actions []string               `json:"actions"`
	Before  map[string]interface{} `json:"before,omitempty"`
	After   map[string]interface{} `json:"after,omitempty"`
}

// PlanPlannedValues models the planned_values hierarchy in a Terraform plan.
type PlanPlannedValues struct {
	RootModule PlanModuleBlock `json:"root_module"`
}

// PlanModuleBlock models a module node in planned values.
type PlanModuleBlock struct {
	Address      string              `json:"address,omitempty"`
	Resources    []PlanResourceBlock `json:"resources,omitempty"`
	ChildModules []PlanModuleBlock   `json:"child_modules,omitempty"`
}

// PlanResourceBlock models an individual resource in planned values or state.
type PlanResourceBlock struct {
	Address string                 `json:"address"`
	Mode    string                 `json:"mode,omitempty"`
	Type    string                 `json:"type"`
	Name    string                 `json:"name"`
	Values  map[string]interface{} `json:"values,omitempty"`
}

// PlanRecreationHazard describes a recreation hazard confirmed on a planned resource.
type PlanRecreationHazard struct {
	ResourceAddress string `json:"resource_address"`
	ResourceType    string `json:"resource_type"`
	HazardType      string `json:"hazard_type"`
	Severity        string `json:"severity"`
	Detail          string `json:"detail"`
	Remediation     string `json:"remediation,omitempty"`
}

// GoldenPlanVerification records the result of verifying a plan against module hazards.
type GoldenPlanVerification struct {
	PlanPath              string                 `json:"plan_path"`
	Autodiscovered        bool                   `json:"autodiscovered"`
	TotalPlannedResources int                    `json:"total_planned_resources"`
	ConfirmedHazards      []PlanRecreationHazard `json:"confirmed_hazards,omitempty"`
	HasRecreationHazards  bool                   `json:"has_recreation_hazards"`
	Summary               string                 `json:"summary"`
}

var autodiscoverCandidates = []string{
	"golden-plan.json",
	"golden_plan.json",
	"tfplan.json",
	"plan.json",
	filepath.Join(".terraform", "golden-plan.json"),
	filepath.Join(".terraform", "tfplan.json"),
}

// AutodiscoverGoldenPlan searches wrapperDir for standard Terraform plan JSON files.
func AutodiscoverGoldenPlan(wrapperDir string) (string, error) {
	for _, cand := range autodiscoverCandidates {
		p := filepath.Join(wrapperDir, cand)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			// #nosec G304 -- candidate plan file within inspected wrapper directory
			data, err := os.ReadFile(filepath.Clean(p))
			if err == nil && isValidPlanJSON(data) {
				return p, nil
			}
		}
	}
	return "", nil
}

func isValidPlanJSON(data []byte) bool {
	var probe struct {
		FormatVersion   string            `json:"format_version"`
		ResourceChanges []json.RawMessage `json:"resource_changes"`
		PlannedValues   json.RawMessage   `json:"planned_values"`
		Resources       []json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.FormatVersion != "" || len(probe.ResourceChanges) > 0 || len(probe.PlannedValues) > 0 || len(probe.Resources) > 0
}

// PlannedResource holds normalized planned resource identity.
type PlannedResource struct {
	Address string
	Type    string
	Name    string
	Actions []string
}

// ExtractPlannedResources flattens planned resources from Terraform plan JSON.
func ExtractPlannedResources(plan *TerraformPlanJSON) []PlannedResource {
	var resources []PlannedResource
	seen := make(map[string]struct{})

	add := func(addr, rType, name string, actions []string) {
		if addr == "" {
			addr = fmt.Sprintf("%s.%s", rType, name)
		}
		if _, exists := seen[addr]; !exists {
			seen[addr] = struct{}{}
			resources = append(resources, PlannedResource{
				Address: addr,
				Type:    rType,
				Name:    name,
				Actions: actions,
			})
		}
	}

	for _, rc := range plan.ResourceChanges {
		if rc.Mode != "" && rc.Mode != "managed" {
			continue
		}
		add(rc.Address, rc.Type, rc.Name, rc.Change.Actions)
	}

	for _, rb := range plan.Resources {
		if rb.Mode != "" && rb.Mode != "managed" {
			continue
		}
		add(rb.Address, rb.Type, rb.Name, nil)
	}

	if plan.PlannedValues != nil {
		var walkModule func(m PlanModuleBlock)
		walkModule = func(m PlanModuleBlock) {
			for _, r := range m.Resources {
				if r.Mode != "" && r.Mode != "managed" {
					continue
				}
				add(r.Address, r.Type, r.Name, nil)
			}
			for _, cm := range m.ChildModules {
				walkModule(cm)
			}
		}
		walkModule(plan.PlannedValues.RootModule)
	}

	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Address < resources[j].Address
	})

	return resources
}

// VerifyGoldenPlanFile loads and verifies a golden plan file against wrapper hazards.
func VerifyGoldenPlanFile(planPath string, autodiscovered bool, wReport *wrapper.WrapperCompatibilityReport, recreationHazards []RecreationHazard, shifts []ast.ResourceAddressShift) (*GoldenPlanVerification, error) {
	// #nosec G304 -- operator specified golden plan file path
	data, err := os.ReadFile(filepath.Clean(planPath))
	if err != nil {
		return nil, fmt.Errorf("read golden plan %s: %w", planPath, err)
	}

	var plan TerraformPlanJSON
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("parse golden plan %s: %w", planPath, err)
	}

	return VerifyGoldenPlan(planPath, autodiscovered, &plan, wReport, recreationHazards, shifts), nil
}

// VerifyGoldenPlan evaluates planned resources against ForceNew hazards, address shifts, and plan actions.
func VerifyGoldenPlan(planPath string, autodiscovered bool, plan *TerraformPlanJSON, wReport *wrapper.WrapperCompatibilityReport, recreationHazards []RecreationHazard, shifts []ast.ResourceAddressShift) *GoldenPlanVerification {
	planned := ExtractPlannedResources(plan)
	var confirmed []PlanRecreationHazard
	seen := make(map[string]struct{})

	addHazard := func(h PlanRecreationHazard) {
		key := fmt.Sprintf("%s:%s:%s", h.ResourceAddress, h.ResourceType, h.HazardType)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			confirmed = append(confirmed, h)
		}
	}

	for _, res := range planned {
		// Check 1: Explicit plan action replaces or deletes existing resource
		if isReplacementAction(res.Actions) {
			addHazard(PlanRecreationHazard{
				ResourceAddress: res.Address,
				ResourceType:    res.Type,
				HazardType:      "PlanActionReplacement",
				Severity:        "CRITICAL_HAZARD",
				Detail:          fmt.Sprintf("Plan action %v replaces existing resource instance", res.Actions),
				Remediation:     "Review changed attributes triggering ForceNew replacement in the execution plan.",
			})
		}

		// Check 2: Planned resource type matches ForceNew recreation hazard
		for _, h := range recreationHazards {
			if res.Type == h.Resource {
				addHazard(PlanRecreationHazard{
					ResourceAddress: res.Address,
					ResourceType:    res.Type,
					HazardType:      h.HazardType,
					Severity:        h.Severity,
					Detail:          fmt.Sprintf("Planned resource matches %s ForceNew hazard on argument '%s': %s", h.Resource, h.Variable, h.Impact),
					Remediation:     h.Remediation,
				})
			}
		}

		// Check 3: Planned resource address matches unmigrated address shift
		for _, s := range shifts {
			if !s.IsRecreation {
				continue
			}
			oldFull := s.OldAddress.FullAddress()
			if strings.Contains(res.Address, oldFull) || (res.Type == s.OldAddress.Type && res.Name == s.OldAddress.Name) {
				addHazard(PlanRecreationHazard{
					ResourceAddress: res.Address,
					ResourceType:    res.Type,
					HazardType:      "AddressShiftRecreation",
					Severity:        "CRITICAL_HAZARD",
					Detail:          fmt.Sprintf("Address shifted from %s to %s without moved block: %s", oldFull, s.NewAddress.FullAddress(), s.Reason),
					Remediation:     "Add a moved {} block to migrate state instance address and avoid destruction.",
				})
			}
		}
	}

	sort.Slice(confirmed, func(i, j int) bool {
		if confirmed[i].ResourceAddress == confirmed[j].ResourceAddress {
			return confirmed[i].HazardType < confirmed[j].HazardType
		}
		return confirmed[i].ResourceAddress < confirmed[j].ResourceAddress
	})

	hasHazards := len(confirmed) > 0
	var summary string
	if hasHazards {
		summary = fmt.Sprintf("Golden plan verification identified %d confirmed state recreation hazard(s) across %d planned resource(s)", len(confirmed), len(planned))
	} else {
		summary = fmt.Sprintf("Golden plan verification passed: 0 recreation hazards detected across %d planned resource(s)", len(planned))
	}

	return &GoldenPlanVerification{
		PlanPath:              planPath,
		Autodiscovered:        autodiscovered,
		TotalPlannedResources: len(planned),
		ConfirmedHazards:      confirmed,
		HasRecreationHazards:  hasHazards,
		Summary:               summary,
	}
}

func isReplacementAction(actions []string) bool {
	if len(actions) < 2 {
		return false
	}
	hasDelete := false
	hasCreate := false
	for _, a := range actions {
		if a == "delete" {
			hasDelete = true
		}
		if a == "create" {
			hasCreate = true
		}
	}
	return hasDelete && hasCreate
}

// FormatGoldenPlanText formats plan verification into human-readable text.
func FormatGoldenPlanText(v *GoldenPlanVerification) string {
	if v == nil {
		return ""
	}
	var sb strings.Builder
	sourceType := "explicit"
	if v.Autodiscovered {
		sourceType = "autodiscovered"
	}
	sb.WriteString("\nGOLDEN PLAN STATE RECREATION VERIFICATION:\n")
	sb.WriteString(fmt.Sprintf("  • Plan path: %s (%s)\n", v.PlanPath, sourceType))
	sb.WriteString(fmt.Sprintf("  • Planned resources evaluated: %d\n", v.TotalPlannedResources))
	sb.WriteString(fmt.Sprintf("  • Status: %s\n", v.Summary))

	if len(v.ConfirmedHazards) > 0 {
		sb.WriteString(fmt.Sprintf("  • Confirmed recreation hazards (%d):\n", len(v.ConfirmedHazards)))
		for _, h := range v.ConfirmedHazards {
			sb.WriteString(fmt.Sprintf("    - [%s] %s (%s): %s\n", h.Severity, h.ResourceAddress, h.HazardType, h.Detail))
			if h.Remediation != "" {
				sb.WriteString(fmt.Sprintf("      Remediation: %s\n", h.Remediation))
			}
		}
	}
	return sb.String()
}

// FormatGoldenPlanMarkdown formats plan verification into GitHub-flavored Markdown.
func FormatGoldenPlanMarkdown(v *GoldenPlanVerification) string {
	if v == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("### Golden Plan State Recreation Verification\n\n")
	sourceType := "explicit"
	if v.Autodiscovered {
		sourceType = "autodiscovered"
	}
	sb.WriteString(fmt.Sprintf("- **Plan File**: `%s` (%s)\n", v.PlanPath, sourceType))
	sb.WriteString(fmt.Sprintf("- **Planned Resources Evaluated**: %d\n", v.TotalPlannedResources))
	sb.WriteString(fmt.Sprintf("- **Verification Status**: %s\n\n", v.Summary))

	if len(v.ConfirmedHazards) > 0 {
		sb.WriteString("> [!CAUTION]\n> **Plan Verification Failed**: Upgrading version pins introduces changes that trigger resource destruction and recreation in the execution plan.\n\n")
		sb.WriteString("| Resource Address | Hazard Type | Severity | Detail | Remediation |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
		for _, h := range v.ConfirmedHazards {
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | `%s` | %s | %s |\n", h.ResourceAddress, h.HazardType, h.Severity, h.Detail, h.Remediation))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("> [!NOTE]\n> **Plan Verification Passed**: No planned resources are affected by ForceNew recreation hazards or unmigrated address shifts.\n\n")
	}
	return sb.String()
}
