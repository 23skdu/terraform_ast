package recreation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeProviderKey(t *testing.T) {
	assert.Equal(t, "unknown", SanitizeProviderKey(""))
	assert.Equal(t, "unknown", SanitizeProviderKey("   "))
	assert.Equal(t, "hashicorp_aws", SanitizeProviderKey("hashicorp/aws"))
	assert.Equal(t, "registry.terraform.io_hashicorp_azurerm", SanitizeProviderKey("registry.terraform.io/hashicorp/azurerm"))
	assert.Equal(t, "a_b_c", SanitizeProviderKey("a\\b:c"))
}

func TestGetBuiltinProviderSchema(t *testing.T) {
	azurePS := GetBuiltinProviderSchema("hashicorp/azurerm")
	require.NotNil(t, azurePS)
	assert.Contains(t, azurePS.ProviderSchemas["registry.terraform.io/hashicorp/azurerm"].ResourceSchemas, "azurerm_kubernetes_cluster")

	k8sPS := GetBuiltinProviderSchema("kubernetes")
	require.NotNil(t, k8sPS)
	assert.Contains(t, k8sPS.ProviderSchemas["registry.terraform.io/hashicorp/kubernetes"].ResourceSchemas, "kubernetes_namespace")

	cfPS := GetBuiltinProviderSchema("cloudflare/cloudflare")
	require.NotNil(t, cfPS)
	assert.Contains(t, cfPS.ProviderSchemas["registry.terraform.io/cloudflare/cloudflare"].ResourceSchemas, "cloudflare_zone")

	unknownPS := GetBuiltinProviderSchema("random/unknown")
	assert.Nil(t, unknownPS)
}

func TestCacheProviderSchema(t *testing.T) {
	// Empty cacheDir or nil ps is a no-op
	assert.NoError(t, CacheProviderSchema("", "aws", nil))
	assert.NoError(t, CacheProviderSchema(t.TempDir(), "aws", nil))

	cacheDir := t.TempDir()
	ps := GetBuiltinProviderSchema("azurerm")
	require.NoError(t, CacheProviderSchema(cacheDir, "hashicorp/azurerm", ps))

	expectedFile := filepath.Join(cacheDir, "schemas", "hashicorp_azurerm.json")
	require.FileExists(t, expectedFile)

	loaded, err := LoadProviderSchema(expectedFile)
	require.NoError(t, err)
	assert.Equal(t, ps.FormatVersion, loaded.FormatVersion)

	// Error path: target path blocked by file
	blockedDir := filepath.Join(t.TempDir(), "blocked")
	require.NoError(t, os.WriteFile(blockedDir, []byte("x"), 0o600))
	err = CacheProviderSchema(blockedDir, "hashicorp/azurerm", ps)
	assert.ErrorContains(t, err, "create schema cache directory")

	// Error path: target file cannot be written
	schemasDir := filepath.Join(cacheDir, "schemas", "unwritable.json")
	require.NoError(t, os.Mkdir(schemasDir, 0o750))
	err = CacheProviderSchema(cacheDir, "unwritable", ps)
	assert.ErrorContains(t, err, "write schema cache file")
}

func TestDynamicHazardRegistry_RegisterAndMergeNil(t *testing.T) {
	var nilReg *DynamicHazardRegistry
	nilReg.Register("k", HazardDescriptor{})
	nilReg.MergeDescriptors(map[string]HazardDescriptor{"k": {}})

	emptyReg := &DynamicHazardRegistry{}
	emptyReg.Register("k", HazardDescriptor{Resource: "test"})
	assert.Equal(t, "test", emptyReg.hazards["k"].Resource)
}

func TestAutoDiscoverSchemas(t *testing.T) {
	// Nil mod
	reg, err := AutoDiscoverSchemas(nil, "")
	require.NoError(t, err)
	require.NotNil(t, reg)

	// Nil reg in AutoDiscoverAndRegister
	assert.NoError(t, AutoDiscoverAndRegister(nil, "", nil))
	assert.NoError(t, AutoDiscoverAndRegister(&ast.ModuleInfo{}, "", nil))

	// Mod with builtin provider (azurerm) and caching
	cacheDir := t.TempDir()
	mod := &ast.ModuleInfo{
		Path: t.TempDir(),
		RequiredProviders: map[string]ast.ProviderRequirement{
			"azurerm": {
				Name:   "azurerm",
				Source: "hashicorp/azurerm",
			},
			"unknown": {
				Name: "unknown",
			},
		},
	}

	reg, err = AutoDiscoverSchemas(mod, cacheDir)
	require.NoError(t, err)
	require.NotNil(t, reg)

	// Check that azurerm ForceNew attribute is discovered
	desc, ok := reg.Lookup("resource_group_name")
	assert.True(t, ok)
	assert.Contains(t, desc.Impact, "ForceNew")

	// Ensure cached file was created
	cachedFile := filepath.Join(cacheDir, "schemas", "hashicorp_azurerm.json")
	require.FileExists(t, cachedFile)

	// Discovering again uses the cache directly
	reg2, err := AutoDiscoverSchemas(mod, cacheDir)
	require.NoError(t, err)
	desc2, ok := reg2.Lookup("resource_group_name")
	assert.True(t, ok)
	assert.Equal(t, desc.Resource, desc2.Resource)

	// Local provider-schema.json discovery
	localDir := t.TempDir()
	customPS := &ProviderSchemas{
		FormatVersion: "1.0",
		ProviderSchemas: map[string]ProviderSchema{
			"custom": {
				ResourceSchemas: map[string]ResourceSchema{
					"custom_thing": {
						Block: BlockSchema{
							Attributes: map[string]AttributeSchema{
								"immutable_field": {RequiresReplace: true, Description: "Immutable custom field"},
							},
						},
					},
				},
			},
		},
	}
	customBytes, err := json.Marshal(customPS)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "provider-schema.json"), customBytes, 0o600))

	customMod := &ast.ModuleInfo{
		Path: localDir,
		RequiredProviders: map[string]ast.ProviderRequirement{
			"custom": {
				Name:   "custom",
				Source: "custom/custom",
			},
		},
	}

	reg3, err := AutoDiscoverSchemas(customMod, "")
	require.NoError(t, err)
	desc3, ok := reg3.Lookup("immutable_field")
	assert.True(t, ok)
	assert.Equal(t, "custom_thing", desc3.Resource)
}
