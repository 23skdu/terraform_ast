package recreation

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProviderSchemas represents the top-level structure of 'terraform providers schema -json'.
type ProviderSchemas struct {
	FormatVersion   string                      `json:"format_version"`
	ProviderSchemas map[string]ProviderSchema   `json:"provider_schemas"`
	memoizedDesc    map[string]HazardDescriptor `json:"-"`
}

type schemaCacheEntry struct {
	mtime   time.Time
	size    int64
	stored  time.Time
	schemas *ProviderSchemas
}

type schemaCache struct {
	mu       sync.Mutex
	entries  map[string]schemaCacheEntry
	order    []string
	capacity int
	ttl      time.Duration
	now      func() time.Time
}

func newSchemaCache(capacity int, ttl time.Duration) *schemaCache {
	if capacity <= 0 {
		capacity = 1
	}
	return &schemaCache{
		entries:  make(map[string]schemaCacheEntry),
		capacity: capacity,
		ttl:      ttl,
		now:      time.Now,
	}
}

var schemaCacheInstance = newSchemaCache(64, 30*time.Minute)

const (
	// persistentSchemaTTL bounds how long an on-disk provider schema is reused.
	persistentSchemaTTL = 30 * time.Minute
	// persistentSchemaLimit caps the number of schema files kept on disk.
	persistentSchemaLimit = 64
)

// pruneSchemaCache removes expired entries and, when the directory exceeds the
// configured bound, the oldest files until it fits. Without this a long-lived
// --cache-dir accumulates provider schemas indefinitely and never refreshes them.
func pruneSchemaCache(dir string, now time.Time) {
	entries, _ := os.ReadDir(dir) // #nosec G304 -- cache directory is operator supplied
	type cachedFile struct {
		path string
		mod  time.Time
	}
	var files []cachedFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Stat(path)
		if err != nil {
			// Broken symlinks and files removed concurrently cannot be aged out.
			continue
		}
		if now.Sub(info.ModTime()) > persistentSchemaTTL {
			_ = os.Remove(path)
			continue
		}
		files = append(files, cachedFile{path: path, mod: info.ModTime()})
	}
	if len(files) <= persistentSchemaLimit {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, file := range files[:len(files)-persistentSchemaLimit] {
		_ = os.Remove(file.path)
	}
}

func (c *schemaCache) touch(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, key)
			return
		}
	}
	c.order = append(c.order, key)
}

func (c *schemaCache) remove(key string) {
	delete(c.entries, key)
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

func (c *schemaCache) put(key string, entry schemaCacheEntry) {
	if _, ok := c.entries[key]; ok {
		c.entries[key] = entry
		c.touch(key)
	} else {
		// Evict LRU if at capacity.
		for len(c.entries) >= c.capacity && len(c.order) > 0 {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
		c.entries[key] = entry
		c.order = append(c.order, key)
	}
}

// resetSchemaCache clears the in-memory schema cache; used by tests for isolation.
func resetSchemaCache() {
	schemaCacheInstance.mu.Lock()
	defer schemaCacheInstance.mu.Unlock()
	schemaCacheInstance.entries = make(map[string]schemaCacheEntry)
	schemaCacheInstance.order = nil
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

// LoadProviderSchema reads and parses a provider schema JSON file, memoized by path and file stats.
func LoadProviderSchema(path string) (*ProviderSchemas, error) {
	cleanPath := filepath.Clean(path)
	// #nosec G304 -- operator specified schema file path
	file, err := os.Open(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("open provider schema file %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	fi, _ := file.Stat()

	// Check bounded TTL LRU cache.
	if fi != nil {
		schemaCacheInstance.mu.Lock()
		if entry, ok := schemaCacheInstance.entries[cleanPath]; ok && entry.mtime.Equal(fi.ModTime()) && entry.size == fi.Size() {
			if schemaCacheInstance.ttl <= 0 || schemaCacheInstance.now().Sub(entry.stored) < schemaCacheInstance.ttl {
				// Touch LRU order.
				schemaCacheInstance.touch(cleanPath)
				schemaCacheInstance.mu.Unlock()
				return entry.schemas, nil
			}
			// Expired: drop.
			schemaCacheInstance.remove(cleanPath)
		}
		schemaCacheInstance.mu.Unlock()
	}

	ps, err := ParseProviderSchema(file)
	if err != nil {
		return nil, err
	}

	if fi != nil {
		schemaCacheInstance.mu.Lock()
		schemaCacheInstance.put(cleanPath, schemaCacheEntry{
			mtime:   fi.ModTime(),
			size:    fi.Size(),
			stored:  schemaCacheInstance.now(),
			schemas: ps,
		})
		schemaCacheInstance.mu.Unlock()
	}

	return ps, nil
}

// ExtractForceNewDescriptors extracts immutable attributes (requires_replace) from the schema,
// mapping attribute names to HazardDescriptors for each resource in deterministic order.
func (ps *ProviderSchemas) ExtractForceNewDescriptors() map[string]HazardDescriptor {
	if ps == nil {
		return make(map[string]HazardDescriptor)
	}
	if ps.memoizedDesc != nil {
		return ps.memoizedDesc
	}
	descriptors := make(map[string]HazardDescriptor)

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
	ps.memoizedDesc = descriptors
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
	if desc, ok := r.hazards[name]; ok {
		return desc, true
	}
	if parts := strings.SplitN(name, ".", 3); len(parts) == 3 {
		if desc, ok := r.hazards[parts[0]+"."+parts[2]]; ok {
			return desc, true
		}
	}
	return HazardDescriptor{}, false
}

// LookupHazard implements ast.HazardLookup for schema-driven ForceNew detection.
func (r *DynamicHazardRegistry) LookupHazard(key string) (bool, string, string) {
	desc, ok := r.Lookup(key)
	if !ok {
		return false, "", ""
	}
	return true, desc.Impact, desc.Remediation
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
