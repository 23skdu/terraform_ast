package recreation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

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
var builtinSchemaCache sync.Map

func GetBuiltinProviderSchema(providerName string) (ps *ProviderSchemas) {
	norm := strings.ToLower(strings.TrimSpace(providerName))
	// Strip namespace if present
	if idx := strings.LastIndex(norm, "/"); idx != -1 {
		norm = norm[idx+1:]
	}
	if cached, ok := builtinSchemaCache.Load(norm); ok {
		return cached.(*ProviderSchemas)
	}
	defer func() {
		if ps != nil {
			builtinSchemaCache.Store(norm, ps)
		}
	}()

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
	case "aws":
		return awsBuiltinProviderSchema()
	case "google", "google-beta":
		return googleBuiltinProviderSchema()
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

// awsResourceSchema builds a ResourceSchema with the given ForceNew attribute names.
func awsResourceSchema(attrs ...string) ResourceSchema {
	attributes := make(map[string]AttributeSchema, len(attrs))
	for _, attr := range attrs {
		attributes[attr] = AttributeSchema{RequiresReplace: true, Description: "AWS ForceNew attribute: " + attr}
	}
	return ResourceSchema{Block: BlockSchema{Attributes: attributes}}
}

func awsBuiltinProviderSchema() *ProviderSchemas {
	resources := map[string]ResourceSchema{
		"aws_eks_cluster":       awsResourceSchema("name"),
		"aws_eks_node_group":    awsResourceSchema("cluster_name", "node_group_name", "subnet_ids"),
		"aws_db_instance":       awsResourceSchema("identifier", "engine", "storage_encrypted", "kms_key_id", "availability_zone"),
		"aws_db_cluster":        awsResourceSchema("cluster_identifier", "engine"),
		"aws_s3_bucket":         awsResourceSchema("bucket", "bucket_prefix"),
		"aws_lambda_function":   awsResourceSchema("function_name", "function_name_prefix"),
		"aws_iam_role":          awsResourceSchema("name", "name_prefix", "path"),
		"aws_vpc":               awsResourceSchema("cidr_block", "instance_tenancy"),
		"aws_subnet":            awsResourceSchema("vpc_id", "cidr_block", "availability_zone", "ipv6_cidr_block"),
		"aws_security_group":    awsResourceSchema("name", "name_prefix", "vpc_id"),
		"aws_instance":          awsResourceSchema("ami", "subnet_id", "availability_zone", "tenancy", "host_id"),
		"aws_autoscaling_group": awsResourceSchema("name", "name_prefix", "launch_configuration"),
		"aws_lb":                awsResourceSchema("name", "name_prefix", "internal", "load_balancer_type"),
		"aws_route_table":       awsResourceSchema("vpc_id"),
		"aws_nat_gateway":       awsResourceSchema("subnet_id", "allocation_id"),
		"aws_kms_key":           awsResourceSchema("key_usage", "customer_master_key_spec"),
		"aws_dynamodb_table":    awsResourceSchema("name", "name_prefix", "hash_key", "range_key"),
		"aws_sqs_queue":         awsResourceSchema("name", "name_prefix", "fifo_queue"),
		"aws_sns_topic":         awsResourceSchema("name", "name_prefix", "fifo_topic"),
	}
	return &ProviderSchemas{
		FormatVersion: "1.0",
		ProviderSchemas: map[string]ProviderSchema{
			"registry.terraform.io/hashicorp/aws": {ResourceSchemas: resources},
		},
	}
}

func googleResourceSchema(attrs ...string) ResourceSchema {
	attributes := make(map[string]AttributeSchema, len(attrs))
	for _, attr := range attrs {
		attributes[attr] = AttributeSchema{RequiresReplace: true, Description: "Google Cloud ForceNew attribute: " + attr}
	}
	return ResourceSchema{Block: BlockSchema{Attributes: attributes}}
}

func googleBuiltinProviderSchema() *ProviderSchemas {
	resources := map[string]ResourceSchema{
		"google_container_cluster":     googleResourceSchema("name", "location", "project", "project_id", "network", "subnetwork", "ip_range_pods", "ip_range_services"),
		"google_container_node_pool":   googleResourceSchema("name", "location", "project", "cluster"),
		"google_compute_instance":      googleResourceSchema("name", "zone", "project", "boot_disk", "network_interface"),
		"google_sql_database_instance": googleResourceSchema("name", "database_version", "region", "project"),
		"google_storage_bucket":        googleResourceSchema("name", "location", "project"),
		"google_compute_network":       googleResourceSchema("name", "project"),
		"google_compute_subnetwork":    googleResourceSchema("name", "region", "project", "network", "ip_cidr_range"),
		"google_project":               googleResourceSchema("project_id", "org_id", "folder_id"),
		"google_service_account":       googleResourceSchema("account_id", "project"),
		"google_compute_firewall":      googleResourceSchema("name", "project", "network"),
		"google_compute_disk":          googleResourceSchema("name", "zone", "project", "image", "source_image"),
		"google_pubsub_topic":          googleResourceSchema("name", "project"),
		"google_bigquery_dataset":      googleResourceSchema("dataset_id", "project", "location"),
		"google_cloud_run_service":     googleResourceSchema("name", "location", "project"),
	}
	return &ProviderSchemas{
		FormatVersion: "1.0",
		ProviderSchemas: map[string]ProviderSchema{
			"registry.terraform.io/hashicorp/google":      {ResourceSchemas: resources},
			"registry.terraform.io/hashicorp/google-beta": {ResourceSchemas: resources},
		},
	}
}
