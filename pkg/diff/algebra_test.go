package diff_test

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAlgebraicInvariants_Identity verifies the identity property:
// For any Terraform module A, Δ(A -> A) = ∅
// Comparing identical versions produces zero breaking changes, zero warnings,
// and always yields a SAFE_TO_UPDATE verdict.
func TestAlgebraicInvariants_Identity(t *testing.T) {
	fixturePaths := []string{
		"../../test/fixtures/upstream_gke_v1",
		"../../test/fixtures/upstream_gke_v2",
		"../../test/fixtures/upstream_eks_v19",
		"../../test/fixtures/upstream_eks_v20",
		"../../test/fixtures/upstream_aks_v1",
		"../../test/fixtures/upstream_aks_v2",
		"../../test/fixtures/object_schema_v1",
		"../../test/fixtures/object_schema_v2",
	}

	for _, relPath := range fixturePaths {
		absPath, err := filepath.Abs(relPath)
		require.NoError(t, err)

		t.Run(filepath.Base(relPath), func(t *testing.T) {
			mod, err := ast.Inspect(absPath)
			require.NoError(t, err)

			report := diff.Compare(mod, mod, "v1.0.0", "v1.0.0")
			assert.True(t, report.CanSafelyUpdate, "Identity diff must be safe to update")
			assert.Equal(t, "SAFE", report.Verdict)
			assert.Equal(t, 0, report.TotalBreaking, "Identity diff must have 0 breaking changes")
			assert.Equal(t, 0, report.TotalWarnings, "Identity diff must have 0 warnings")
			assert.Empty(t, report.BreakingChanges)
			assert.Empty(t, report.Warnings)
		})
	}
}

// TestAlgebraicInvariants_TriangleInequality verifies the triangle inequality:
// For any sequence of module versions A -> B -> C, the union of stepwise differences
// must be a superset of the direct difference:
// Δ(A -> B) ∪ Δ(B -> C) ⊇ Δ(A -> C)
func TestAlgebraicInvariants_TriangleInequality(t *testing.T) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	types := []string{"string", "number", "bool", "list(string)", "map(string)"}

	for iteration := 0; iteration < 50; iteration++ {
		t.Run(fmt.Sprintf("Iteration_%d", iteration), func(t *testing.T) {
			// Generate module A
			modA := &ast.ModuleInfo{
				Path:      "/tmp/modA",
				Variables: make(map[string]ast.VariableDef),
				Outputs:   make(map[string]ast.OutputDef),
			}
			numVarsA := 3 + rng.Intn(5)
			for i := 0; i < numVarsA; i++ {
				varName := fmt.Sprintf("var_%d", i)
				hasDefault := rng.Float32() < 0.5
				var defVal string
				if hasDefault {
					defVal = `"default"`
				}
				modA.Variables[varName] = ast.VariableDef{
					Name:        varName,
					Type:        types[rng.Intn(len(types))],
					Description: "Synthetic var",
					Default:     defVal,
					HasDefault:  hasDefault,
					Required:    !hasDefault,
				}
			}

			// Generate module B from A (with mutations)
			modB := cloneModule(modA, "/tmp/modB")
			mutateModule(rng, modB, types)

			// Generate module C from B (with mutations)
			modC := cloneModule(modB, "/tmp/modC")
			mutateModule(rng, modC, types)

			// Step 1: Δ(A -> B)
			diffAB := diff.Compare(modA, modB, "v1.0.0", "v2.0.0")

			// Step 2: Δ(B -> C)
			diffBC := diff.Compare(modB, modC, "v2.0.0", "v3.0.0")

			// Step 3: Δ(A -> C)
			diffAC := diff.Compare(modA, modC, "v1.0.0", "v3.0.0")

			// Invariant verification:
			// Every breaking change in Δ(A -> C) must be accounted for in Δ(A -> B) ∪ Δ(B -> C).
			stepwiseHazards := make(map[string]bool)
			for _, item := range diffAB.BreakingChanges {
				stepwiseHazards[item.Category+":"+item.Name] = true
			}
			for _, item := range diffBC.BreakingChanges {
				stepwiseHazards[item.Category+":"+item.Name] = true
			}

			for _, directHazard := range diffAC.BreakingChanges {
				key := directHazard.Category + ":" + directHazard.Name
				// If a variable or output was removed or newly required in A -> C,
				// it must have occurred in A -> B or B -> C.
				if directHazard.Category == "Variable" || directHazard.Category == "Output" {
					assert.True(t, stepwiseHazards[key],
						"Direct breaking change %s in Δ(A -> C) missing from stepwise union Δ(A -> B) ∪ Δ(B -> C)", key)
				}
			}
		})
	}
}

func cloneModule(src *ast.ModuleInfo, newPath string) *ast.ModuleInfo {
	dst := &ast.ModuleInfo{
		Path:                    newPath,
		Variables:               make(map[string]ast.VariableDef),
		Outputs:                 make(map[string]ast.OutputDef),
		ModuleCalls:             make(map[string]ast.ModuleCallDef),
		WrapperOutputReferences: make(map[string][]ast.OutputReference),
		RequiredCore:            src.RequiredCore,
	}
	for k, v := range src.Variables {
		dst.Variables[k] = v
	}
	for k, v := range src.Outputs {
		dst.Outputs[k] = v
	}
	return dst
}

func mutateModule(rng *rand.Rand, mod *ast.ModuleInfo, types []string) {
	// Randomly remove a variable
	if len(mod.Variables) > 1 && rng.Float32() < 0.3 {
		for k := range mod.Variables {
			delete(mod.Variables, k)
			break
		}
	}
	// Randomly add a new required variable
	if rng.Float32() < 0.4 {
		name := fmt.Sprintf("new_req_%d", rng.Intn(100))
		mod.Variables[name] = ast.VariableDef{
			Name:     name,
			Type:     types[rng.Intn(len(types))],
			Required: true,
		}
	}
	// Randomly add an output
	if rng.Float32() < 0.3 {
		outName := fmt.Sprintf("out_%d", rng.Intn(100))
		mod.Outputs[outName] = ast.OutputDef{
			Name: "outName",
		}
	}
}
