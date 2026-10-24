package ast_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

type tfValidationOutput struct {
	Valid        bool `json:"valid"`
	ErrorCount   int  `json:"error_count"`
	WarningCount int  `json:"warning_count"`
}

// FuzzDifferentialValidation synthesizes HCL configurations and differential-tests
// tfastdiff's AST inspection engine against the native Terraform/OpenTofu compiler.
func FuzzDifferentialValidation(f *testing.F) {
	seeds := []string{
		`variable "app_name" {
  type        = string
  description = "Application name"
  default     = "webapp"
}

output "app_id" {
  value = var.app_name
}`,
		`variable "cluster_size" {
  type        = number
  description = "Number of worker nodes"
  default     = 3
}`,
		`variable "tags" {
  type        = map(string)
  description = "Resource tags"
  default     = { env = "prod" }
}`,
		`variable "subnets" {
  type        = list(string)
  description = "Subnet CIDR blocks"
}`,
		`check "health" {
  assert {
    condition     = true
    error_message = "health check"
  }
}`,
	}

	for _, s := range seeds {
		f.Add(s)
	}

	compilerPath, err := exec.LookPath("terraform")
	if err != nil {
		compilerPath, err = exec.LookPath("tofu")
	}
	hasCompiler := (err == nil)

	f.Fuzz(func(t *testing.T, snippet string) {
		// Reject inputs containing null bytes or excessive length for speed
		if strings.Contains(snippet, "\x00") || len(snippet) > 4096 {
			return
		}

		dir := t.TempDir()
		tfFile := filepath.Join(dir, "main.tf")
		if err := os.WriteFile(tfFile, []byte(snippet), 0o600); err != nil {
			return
		}

		// Execute tfastdiff AST inspection
		info, err := ast.Inspect(dir)
		if err != nil {
			// Inspection gracefully rejected malformed syntax
			return
		}

		if info == nil {
			t.Fatalf("Inspect returned nil module without error for snippet: %s", snippet)
		}

		// Ensure extracted properties are structurally sound
		for varName, varDef := range info.Variables {
			if varDef.Name != varName {
				t.Fatalf("variable map key %q != struct Name %q", varName, varDef.Name)
			}
		}

		// Cross-validate against HashiCorp terraform-config-inspect reference engine
		refMod, diags := tfconfig.LoadModule(dir)
		if !diags.HasErrors() && refMod != nil {
			if len(info.Variables) != len(refMod.Variables) {
				t.Fatalf("variable count mismatch: ast=%d, tfconfig=%d for:\n%s", len(info.Variables), len(refMod.Variables), snippet)
			}
			for vName, refVar := range refMod.Variables {
				astVar, exists := info.Variables[vName]
				if !exists {
					t.Fatalf("variable %q missing in ast.Inspect for:\n%s", vName, snippet)
				}
				if astVar.Required != refVar.Required {
					t.Fatalf("variable %q required mismatch: ast=%v, tfconfig=%v", vName, astVar.Required, refVar.Required)
				}
			}
			if len(info.Outputs) != len(refMod.Outputs) {
				t.Fatalf("output count mismatch: ast=%d, tfconfig=%d for:\n%s", len(info.Outputs), len(refMod.Outputs), snippet)
			}
		}

		// If native compiler is present, execute differential verification
		if hasCompiler && !testing.Short() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, compilerPath, "validate", "-json")
			cmd.Dir = dir
			out, valErr := cmd.Output()
			if valErr == nil && len(out) > 0 {
				var valOut tfValidationOutput
				if jErr := json.Unmarshal(out, &valOut); jErr == nil && valOut.Valid {
					// Compiler confirmed valid Terraform syntax.
					// Verify AST inspect preserved the module path and error count is 0.
					if info.Path != dir {
						t.Fatalf("Compiler accepted module but AST inspect path mismatch:\n%s", snippet)
					}
					if valOut.ErrorCount > 0 {
						t.Fatalf("Compiler reported error count > 0 for valid config")
					}
				}
			}
		}
	})
}

// TestDifferentialValidation_DirectCompilerValidation directly runs native compiler
// cross-validation on test fixtures.
func TestDifferentialValidation_DirectCompilerValidation(t *testing.T) {
	compilerPath, err := exec.LookPath("terraform")
	if err != nil {
		compilerPath, err = exec.LookPath("tofu")
	}
	if err != nil {
		t.Skip("Neither terraform nor tofu found on PATH")
	}

	fixtures := []string{
		"../../test/fixtures/upstream_gke_v1",
		"../../test/fixtures/upstream_eks_v19",
		"../../test/fixtures/upstream_aks_v1",
	}

	for _, relPath := range fixtures {
		absPath, err := filepath.Abs(relPath)
		requireNoError(t, err)

		t.Run(filepath.Base(relPath), func(t *testing.T) {
			info, err := ast.Inspect(absPath)
			requireNoError(t, err)
			if info == nil {
				t.Fatalf("AST inspect failed for fixture %s", relPath)
			}

			cmd := exec.Command(compilerPath, "validate", "-json")
			cmd.Dir = absPath
			out, _ := cmd.Output()
			if len(out) > 0 {
				var valOut tfValidationOutput
				if jErr := json.Unmarshal(out, &valOut); jErr == nil && valOut.Valid {
					if len(info.Variables) == 0 {
						t.Fatalf("Compiler found valid configuration but AST found 0 variables for %s", relPath)
					}
				}
			}
		})
	}
}

// TestDifferentialStructuralEquivalence validates deep structural property invariants
// between ast.Inspect and the reference terraform-config-inspect compiler.
func TestDifferentialStructuralEquivalence(t *testing.T) {
	fixtures := []string{
		"../../test/fixtures/upstream_gke_v1",
		"../../test/fixtures/upstream_eks_v19",
		"../../test/fixtures/upstream_aks_v1",
	}

	for _, relPath := range fixtures {
		absPath, err := filepath.Abs(relPath)
		requireNoError(t, err)

		t.Run(filepath.Base(relPath), func(t *testing.T) {
			info, err := ast.Inspect(absPath)
			requireNoError(t, err)
			if info == nil {
				t.Fatalf("ast.Inspect returned nil for %s", relPath)
			}

			tfMod, diags := tfconfig.LoadModule(absPath)
			if diags.HasErrors() {
				t.Fatalf("tfconfig.LoadModule returned diagnostics for %s: %s", relPath, diags.Error())
			}

			// Invariant 1: Variables equivalence
			if len(info.Variables) != len(tfMod.Variables) {
				t.Errorf("variable count mismatch: ast=%d, tfconfig=%d", len(info.Variables), len(tfMod.Variables))
			}
			for name, refVar := range tfMod.Variables {
				astVar, exists := info.Variables[name]
				if !exists {
					t.Errorf("variable %q missing in ast.Inspect", name)
					continue
				}
				if astVar.Required != refVar.Required {
					t.Errorf("variable %q Required mismatch: ast=%v, tfconfig=%v", name, astVar.Required, refVar.Required)
				}
				if astVar.Sensitive != refVar.Sensitive {
					t.Errorf("variable %q Sensitive mismatch: ast=%v, tfconfig=%v", name, astVar.Sensitive, refVar.Sensitive)
				}
			}

			// Invariant 2: Outputs equivalence
			if len(info.Outputs) != len(tfMod.Outputs) {
				t.Errorf("output count mismatch: ast=%d, tfconfig=%d", len(info.Outputs), len(tfMod.Outputs))
			}
			for name, refOut := range tfMod.Outputs {
				astOut, exists := info.Outputs[name]
				if !exists {
					t.Errorf("output %q missing in ast.Inspect", name)
					continue
				}
				if astOut.Sensitive != refOut.Sensitive {
					t.Errorf("output %q Sensitive mismatch: ast=%v, tfconfig=%v", name, astOut.Sensitive, refOut.Sensitive)
				}
			}

			// Invariant 3: Required Providers equivalence
			if len(info.RequiredProviders) != len(tfMod.RequiredProviders) {
				t.Errorf("provider count mismatch: ast=%d, tfconfig=%d", len(info.RequiredProviders), len(tfMod.RequiredProviders))
			}
			for name, refProv := range tfMod.RequiredProviders {
				astProv, exists := info.RequiredProviders[name]
				if !exists {
					t.Errorf("provider %q missing in ast.Inspect", name)
					continue
				}
				if astProv.Source != refProv.Source {
					t.Errorf("provider %q Source mismatch: ast=%q, tfconfig=%q", name, astProv.Source, refProv.Source)
				}
			}

			// Invariant 4: Module Calls equivalence
			if len(info.ModuleCalls) != len(tfMod.ModuleCalls) {
				t.Errorf("module call count mismatch: ast=%d, tfconfig=%d", len(info.ModuleCalls), len(tfMod.ModuleCalls))
			}
			for name, refMC := range tfMod.ModuleCalls {
				astMC, exists := info.ModuleCalls[name]
				if !exists {
					t.Errorf("module call %q missing in ast.Inspect", name)
					continue
				}
				if astMC.Source != refMC.Source {
					t.Errorf("module call %q Source mismatch: ast=%q, tfconfig=%q", name, astMC.Source, refMC.Source)
				}
			}

			// Invariant 5: Managed Resources equivalence
			if len(info.ManagedResources) != len(tfMod.ManagedResources) {
				t.Errorf("managed resource count mismatch: ast=%d, tfconfig=%d", len(info.ManagedResources), len(tfMod.ManagedResources))
			}
			for key, refRes := range tfMod.ManagedResources {
				astRes, exists := info.ManagedResources[key]
				if !exists {
					t.Errorf("managed resource %q missing in ast.Inspect", key)
					continue
				}
				if astRes.Type != refRes.Type || astRes.Name != refRes.Name {
					t.Errorf("managed resource %q identity mismatch", key)
				}
			}

			// Invariant 6: Data Resources equivalence
			if len(info.DataResources) != len(tfMod.DataResources) {
				t.Errorf("data resource count mismatch: ast=%d, tfconfig=%d", len(info.DataResources), len(tfMod.DataResources))
			}
			for key, refRes := range tfMod.DataResources {
				astRes, exists := info.DataResources[key]
				if !exists {
					t.Errorf("data resource %q missing in ast.Inspect", key)
					continue
				}
				if astRes.Type != refRes.Type || astRes.Name != refRes.Name {
					t.Errorf("data resource %q identity mismatch", key)
				}
			}
		})
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
