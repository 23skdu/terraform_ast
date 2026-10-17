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
					// Verify AST inspect preserved the module path.
					if info.Path != dir {
						t.Fatalf("Compiler accepted module but AST inspect path mismatch:\n%s", snippet)
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

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
