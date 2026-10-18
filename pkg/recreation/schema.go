package recreation

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ProviderSchemas represents the top-level structure of 'terraform providers schema -json'.
type ProviderSchemas struct {
	FormatVersion   string                    `json:"format_version"`
	ProviderSchemas map[string]ProviderSchema `json:"provider_schemas"`
}

// ProviderSchema contains the resource and data source schemas for a provider.
type ProviderSchema struct {
	ResourceSchemas map[string]ResourceSchema `json:"resource_schemas"`
}

// ResourceSchema contains the block schema definition for a resource.
type ResourceSchema struct {
	Block BlockSchema `json:"block"`
}

// BlockSchema defines attributes and nested blocks for a resource.
type BlockSchema struct {
	Attributes map[string]AttributeSchema `json:"attributes"`
	BlockTypes map[string]BlockTypeSchema `json:"block_types,omitempty"`
}

// AttributeSchema contains metadata for a single resource attribute.
type AttributeSchema struct {
	Type            any    `json:"type,omitempty"`
	Description     string `json:"description,omitempty"`
	Required        bool   `json:"required,omitempty"`
	Optional        bool   `json:"optional,omitempty"`
	Computed        bool   `json:"computed,omitempty"`
	RequiresReplace bool   `json:"requires_replace,omitempty"`
}

// BlockTypeSchema contains a nested block structure.
type BlockTypeSchema struct {
	NestingMode string      `json:"nesting_mode"`
	Block       BlockSchema `json:"block"`
}

// ParseProviderSchema decodes JSON provider schemas from a reader.
func ParseProviderSchema(reader io.Reader) (*ProviderSchemas, error) {
	if reader == nil {
		return nil, fmt.Errorf("reader cannot be nil")
	}
	var ps ProviderSchemas
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&ps); err != nil {
		return nil, fmt.Errorf("decode provider schema JSON: %w", err)
	}
	return &ps, nil
}

// LoadProviderSchema reads and parses a provider schema JSON file.
func LoadProviderSchema(path string) (*ProviderSchemas, error) {
	// #nosec G304 -- operator specified schema file path
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open provider schema file %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	return ParseProviderSchema(file)
}

// ExtractForceNewDescriptors extracts immutable attributes (requires_replace) from the schema,
// mapping attribute names to HazardDescriptors for each resource in deterministic order.
func (ps *ProviderSchemas) ExtractForceNewDescriptors() map[string]HazardDescriptor {
	descriptors := make(map[string]HazardDescriptor)
	if ps == nil {
		return descriptors
	}

	provKeys := make([]string, 0, len(ps.ProviderSchemas))
	for k := range ps.ProviderSchemas {
		provKeys = append(provKeys, k)
	}
	sort.Strings(provKeys)

	for _, provKey := range provKeys {
		provider := ps.ProviderSchemas[provKey]
		resKeys := make([]string, 0, len(provider.ResourceSchemas))
		for k := range provider.ResourceSchemas {
			resKeys = append(resKeys, k)
		}
		sort.Strings(resKeys)

		for _, resName := range resKeys {
			resSchema := provider.ResourceSchemas[resName]
			extractFromBlock(resName, "", resSchema.Block, descriptors)
		}
	}
	return descriptors
}

func extractFromBlock(resName, prefix string, block BlockSchema, descriptors map[string]HazardDescriptor) {
	attrKeys := make([]string, 0, len(block.Attributes))
	for k := range block.Attributes {
		attrKeys = append(attrKeys, k)
	}
	sort.Strings(attrKeys)

	for _, attrName := range attrKeys {
		attr := block.Attributes[attrName]
		if attr.RequiresReplace {
			fullPath := attrName
			if prefix != "" {
				fullPath = prefix + "." + attrName
			}
			desc := attr.Description
			if strings.TrimSpace(desc) == "" {
				desc = fmt.Sprintf("Attribute '%s' requires replacement of %s", fullPath, resName)
			}
			hd := HazardDescriptor{
				Resource:    resName,
				Impact:      fmt.Sprintf("%s; modifying it triggers ForceNew destruction of %s.", desc, resName),
				Remediation: fmt.Sprintf("Keep %s unchanged to avoid resource destruction.", fullPath),
			}
			descriptors[resName+"."+fullPath] = hd
			descriptors[fullPath] = hd
			if _, exists := descriptors[attrName]; !exists {
				descriptors[attrName] = hd
			}
		}
	}

	btKeys := make([]string, 0, len(block.BlockTypes))
	for k := range block.BlockTypes {
		btKeys = append(btKeys, k)
	}
	sort.Strings(btKeys)

	for _, blockName := range btKeys {
		nested := block.BlockTypes[blockName]
		nextPrefix := blockName
		if prefix != "" {
			nextPrefix = prefix + "." + blockName
		}
		extractFromBlock(resName, nextPrefix, nested.Block, descriptors)
	}
}

// DynamicHazardRegistry stores dynamically loaded provider schema hazards.
type DynamicHazardRegistry struct {
	hazards map[string]HazardDescriptor
}

// NewDynamicHazardRegistry creates a registry from loaded schemas.
func NewDynamicHazardRegistry(ps *ProviderSchemas) *DynamicHazardRegistry {
	hazards := make(map[string]HazardDescriptor)
	if ps != nil {
		hazards = ps.ExtractForceNewDescriptors()
	}
	return &DynamicHazardRegistry{hazards: hazards}
}

// Lookup checks if a variable name corresponds to an immutable attribute in the dynamic registry.
func (r *DynamicHazardRegistry) Lookup(name string) (HazardDescriptor, bool) {
	if r == nil || len(r.hazards) == 0 {
		return HazardDescriptor{}, false
	}
	desc, ok := r.hazards[name]
	return desc, ok
}

// Register adds a descriptor for a key into the registry.
func (r *DynamicHazardRegistry) Register(key string, desc HazardDescriptor) {
	if r == nil {
		return
	}
	if r.hazards == nil {
		r.hazards = make(map[string]HazardDescriptor)
	}
	r.hazards[key] = desc
}

// MergeDescriptors merges multiple descriptors into the registry.
func (r *DynamicHazardRegistry) MergeDescriptors(descriptors map[string]HazardDescriptor) {
	if r == nil {
		return
	}
	for k, v := range descriptors {
		r.Register(k, v)
	}
}
