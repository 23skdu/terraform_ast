package recreation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/ast"
)

// SanitizeProviderKey converts a provider source (e.g. "hashicorp/azurerm") into a safe filename.
func SanitizeProviderKey(source string) string {
	cleaned := strings.TrimSpace(source)
	if cleaned == "" {
		return "unknown"
	}
	cleaned = strings.ReplaceAll(cleaned, "/", "_")
	cleaned = strings.ReplaceAll(cleaned, "\\", "_")
	cleaned = strings.ReplaceAll(cleaned, ":", "_")
	return cleaned
}

// GetBuiltinProviderSchema returns a synthetic ProviderSchemas for well-known providers
// to ensure ForceNew protection even without an explicit terraform providers schema export.
func GetBuiltinProviderSchema(providerName string) *ProviderSchemas {
	norm := strings.ToLower(strings.TrimSpace(providerName))
	// Strip namespace if present
	if idx := strings.LastIndex(norm, "/"); idx != -1 {
		norm = norm[idx+1:]
	}

	switch norm {
	case "azurerm":
		return &ProviderSchemas{
			FormatVersion: "1.0",
			ProviderSchemas: map[string]ProviderSchema{
				"registry.terraform.io/hashicorp/azurerm": {
					ResourceSchemas: map[string]ResourceSchema{
						"azurerm_kubernetes_cluster": {
							Block: BlockSchema{
								Attributes: map[string]AttributeSchema{
									"name":                {RequiresReplace: true, Description: "The name of the Managed Kubernetes Cluster"},
									"location":            {RequiresReplace: true, Description: "The Azure Region where the Managed Kubernetes Cluster should exist"},
									"resource_group_name": {RequiresReplace: true, Description: "Specifies the Resource Group where the Managed Kubernetes Cluster exists"},
									"dns_prefix":          {RequiresReplace: true, Description: "DNS prefix specified when creating the managed cluster"},
								},
							},
						},
						"azurerm_virtual_network": {
							Block: BlockSchema{
								Attributes: map[string]AttributeSchema{
									"name":                {RequiresReplace: true, Description: "The name of the virtual network"},
									"location":            {RequiresReplace: true, Description: "The location of the virtual network"},
									"resource_group_name": {RequiresReplace: true, Description: "The name of the resource group"},
								},
							},
						},
					},
				},
			},
		}
	case "kubernetes":
		return &ProviderSchemas{
			FormatVersion: "1.0",
			ProviderSchemas: map[string]ProviderSchema{
				"registry.terraform.io/hashicorp/kubernetes": {
					ResourceSchemas: map[string]ResourceSchema{
						"kubernetes_namespace": {
							Block: BlockSchema{
								BlockTypes: map[string]BlockTypeSchema{
									"metadata": {
										Block: BlockSchema{
											Attributes: map[string]AttributeSchema{
												"name": {RequiresReplace: true, Description: "Name of the namespace, must be unique"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
	case "cloudflare":
		return &ProviderSchemas{
			FormatVersion: "1.0",
			ProviderSchemas: map[string]ProviderSchema{
				"registry.terraform.io/cloudflare/cloudflare": {
					ResourceSchemas: map[string]ResourceSchema{
						"cloudflare_zone": {
							Block: BlockSchema{
								Attributes: map[string]AttributeSchema{
									"zone":       {RequiresReplace: true, Description: "The DNS zone name"},
									"account_id": {RequiresReplace: true, Description: "Account ID to associate with zone"},
								},
							},
						},
					},
				},
			},
		}
	default:
		return nil
	}
}

// CacheProviderSchema writes provider schemas atomically to the schema cache directory.
func CacheProviderSchema(cacheDir, providerSource string, ps *ProviderSchemas) error {
	if cacheDir == "" || ps == nil {
		return nil
	}

	targetDir := filepath.Join(cacheDir, "schemas")
	if err := os.MkdirAll(targetDir, 0o750); err != nil {
		return fmt.Errorf("create schema cache directory: %w", err)
	}

	data, _ := json.MarshalIndent(ps, "", "  ")

	filename := SanitizeProviderKey(providerSource) + ".json"
	targetPath := filepath.Join(targetDir, filename)
	// #nosec G703 -- cache path inside configured cache directory
	if err := os.WriteFile(targetPath, data, 0o600); err != nil {
		return fmt.Errorf("write schema cache file: %w", err)
	}

	return nil
}

// AutoDiscoverSchemas discovers or retrieves provider schemas for all required providers of a module.
func AutoDiscoverSchemas(mod *ast.ModuleInfo, cacheDir string) (*DynamicHazardRegistry, error) {
	registry := NewDynamicHazardRegistry(nil)
	if mod == nil {
		return registry, nil
	}

	_ = AutoDiscoverAndRegister(mod, cacheDir, registry)
	return registry, nil
}

// AutoDiscoverAndRegister enriches an existing DynamicHazardRegistry with discovered provider descriptors.
func AutoDiscoverAndRegister(mod *ast.ModuleInfo, cacheDir string, reg *DynamicHazardRegistry) error {
	if mod == nil || reg == nil {
		return nil
	}

	providerKeys := make([]string, 0, len(mod.RequiredProviders))
	for k := range mod.RequiredProviders {
		providerKeys = append(providerKeys, k)
	}
	sort.Strings(providerKeys)

	for _, name := range providerKeys {
		req := mod.RequiredProviders[name]
		source := req.Source
		if strings.TrimSpace(source) == "" {
			source = "hashicorp/" + name
		}

		var ps *ProviderSchemas

		// 1. Check persistent L2 cache if configured
		if cacheDir != "" {
			cachedFile := filepath.Join(cacheDir, "schemas", SanitizeProviderKey(source)+".json")
			if _, err := os.Stat(cachedFile); err == nil {
				if loaded, err := LoadProviderSchema(cachedFile); err == nil {
					ps = loaded
				}
			}
		}

		// 2. Check local module files (.terraform or provider-schema.json)
		if ps == nil && mod.Path != "" {
			localSchema := filepath.Join(mod.Path, "provider-schema.json")
			if _, err := os.Stat(localSchema); err == nil {
				if loaded, err := LoadProviderSchema(localSchema); err == nil {
					ps = loaded
				}
			}
		}

		// 3. Fallback to builtin schemas
		if ps == nil {
			ps = GetBuiltinProviderSchema(source)
			if ps != nil && cacheDir != "" {
				_ = CacheProviderSchema(cacheDir, source, ps)
			}
		}

		// 4. Extract descriptors and merge
		if ps != nil {
			descriptors := ps.ExtractForceNewDescriptors()
			reg.MergeDescriptors(descriptors)
		}
	}

	return nil
}
