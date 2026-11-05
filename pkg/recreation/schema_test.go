package recreation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleProviderSchema = `{
  "format_version": "1.0",
  "provider_schemas": {
    "registry.terraform.io/hashicorp/google": {
      "resource_schemas": {
        "google_compute_instance": {
          "block": {
            "attributes": {
              "zone": {
                "type": "string",
                "description": "The zone that the machine should be created in.",
                "optional": true,
                "requires_replace": true
              },
              "name": {
                "type": "string",
                "optional": true,
                "requires_replace": true
              },
              "tags": {
                "type": ["list", "string"],
                "optional": true,
                "requires_replace": false
              }
            },
            "block_types": {
              "boot_disk": {
                "nesting_mode": "list",
                "block": {
                  "attributes": {
                    "auto_delete": {
                      "type": "bool",
                      "optional": true,
                      "requires_replace": false
                    },
                    "source": {
                      "type": "string",
                      "optional": true,
                      "requires_replace": true
                    }
                  },
                  "block_types": {
                    "initialize_params": {
                      "nesting_mode": "list",
                      "block": {
                        "attributes": {
                          "image": {
                            "type": "string",
                            "optional": true,
                            "requires_replace": true
                          }
                        }
                      }
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}`

func TestParseProviderSchema(t *testing.T) {
	_, err := ParseProviderSchema(nil)
	assert.ErrorContains(t, err, "reader cannot be nil")

	_, err = ParseProviderSchema(strings.NewReader("invalid json"))
	assert.ErrorContains(t, err, "decode provider schema JSON")

	ps, err := ParseProviderSchema(strings.NewReader(sampleProviderSchema))
	require.NoError(t, err)
	assert.Equal(t, "1.0", ps.FormatVersion)

	descriptors := ps.ExtractForceNewDescriptors()
	assert.Contains(t, descriptors, "zone")
	assert.Contains(t, descriptors, "name")
	assert.Contains(t, descriptors, "boot_disk.source")
	assert.NotContains(t, descriptors, "tags")

	// Nil receiver test
	var nilPS *ProviderSchemas
	assert.Empty(t, nilPS.ExtractForceNewDescriptors())
}

func TestLoadProviderSchema(t *testing.T) {
	_, err := LoadProviderSchema("/non/existent/path.json")
	assert.ErrorContains(t, err, "open provider schema file")

	dir := t.TempDir()
	path := filepath.Join(dir, "schema.json")
	require.NoError(t, os.WriteFile(path, []byte(sampleProviderSchema), 0o600))

	ps, err := LoadProviderSchema(path)
	require.NoError(t, err)
	assert.Equal(t, "1.0", ps.FormatVersion)

	// Second load hits memoized cache
	psCached, err := LoadProviderSchema(path)
	require.NoError(t, err)
	assert.Same(t, ps, psCached)

	// Second extraction hits memoizedDesc
	d1 := ps.ExtractForceNewDescriptors()
	d2 := ps.ExtractForceNewDescriptors()
	assert.Equal(t, len(d1), len(d2))

	// Invalid JSON file returns error
	badPath := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(badPath, []byte("broken json"), 0o600))
	_, err = LoadProviderSchema(badPath)
	assert.Error(t, err)
}

func TestDynamicHazardRegistryAndAnalysis(t *testing.T) {
	var nilReg *DynamicHazardRegistry
	_, found := nilReg.Lookup("zone")
	assert.False(t, found)

	emptyReg := NewDynamicHazardRegistry(nil)
	_, found = emptyReg.Lookup("zone")
	assert.False(t, found)

	ps, err := ParseProviderSchema(strings.NewReader(sampleProviderSchema))
	require.NoError(t, err)
	reg := NewDynamicHazardRegistry(ps)

	desc, found := reg.Lookup("zone")
	assert.True(t, found)
	assert.Equal(t, "google_compute_instance", desc.Resource)

	_, found = reg.Lookup("unknown_attr")
	assert.False(t, found)

	// LookupHazard
	isForce, impact, remediation := reg.LookupHazard("zone")
	assert.True(t, isForce)
	assert.NotEmpty(t, impact)
	assert.NotEmpty(t, remediation)
	isForce, _, _ = reg.LookupHazard("unknown_attr")
	assert.False(t, isForce)
	isForce, _, _ = nilReg.LookupHazard("zone")
	assert.False(t, isForce)

	// AnalyzeHazardsWithRegistry with nil diffReport
	assert.Nil(t, AnalyzeHazardsWithRegistry(nil, reg))

	// AnalyzeHazardsWithRegistry with dynamic breaking changes and warnings
	diffRep := &diff.DiffReport{
		ModuleName: "custom-module",
		BreakingChanges: []diff.DifferenceItem{
			{Name: "zone", Category: "Variable", Description: "Zone changed"},
			{Name: "other", Category: "Variable", Description: "Other"},
		},
		Warnings: []diff.DifferenceItem{
			{Name: "boot_disk.source", Category: "VariableType", Description: "Source type widened"},
			{Name: "other_warn", Category: "Variable", Description: "Other warning"},
		},
	}
	hazards := AnalyzeHazardsWithRegistry(diffRep, reg)
	require.Len(t, hazards, 2)
	assert.Equal(t, "zone", hazards[0].Variable)
	assert.Equal(t, "CRITICAL_HAZARD", hazards[0].Severity)
	assert.Equal(t, "boot_disk.source", hazards[1].Variable)
	assert.Equal(t, "WARNING_HAZARD", hazards[1].Severity)

	// AnalyzeWrapperHazardsWithRegistry
	assert.Nil(t, AnalyzeWrapperHazardsWithRegistry(nil, reg))
	wrapRep := &wrapper.WrapperCompatibilityReport{
		TargetModuleName: "custom-module",
		Issues: []wrapper.WrapperIssue{
			{Target: "zone", Category: "Variable", Description: "Missing required zone"},
			{Target: "other", Category: "Variable", Description: "Missing other"},
		},
	}
	wrapHazards := AnalyzeWrapperHazardsWithRegistry(wrapRep, reg)
	require.Len(t, wrapHazards, 1)
	assert.Equal(t, "zone", wrapHazards[0].Variable)

	// 3-part resource-instance lookup ("type.label.attr" -> "type.attr") (#153, #168)
	rule, found := reg.Lookup("google_compute_instance.vm_1.zone")
	assert.True(t, found)
	assert.NotEmpty(t, rule.Impact)
}
