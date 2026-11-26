// Package crossplane provides lightweight inspection and comparison of Crossplane
// compositions and claims.
package crossplane

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// TypeRef identifies a Kubernetes/Crossplane API type.
type TypeRef struct {
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	Kind       string `json:"kind" yaml:"kind"`
}

// ComposedResource is one entry in a Composition's resources list.
type ComposedResource struct {
	Name string `json:"name" yaml:"name"`
	Base struct {
		APIVersion string `json:"apiVersion" yaml:"apiVersion"`
		Kind       string `json:"kind" yaml:"kind"`
	} `json:"base" yaml:"base"`
	Patches []map[string]any `json:"patches,omitempty" yaml:"patches"`
}

// CompositionSpec is the spec of a Crossplane Composition.
type CompositionSpec struct {
	CompositeTypeRef TypeRef            `json:"compositeTypeRef" yaml:"compositeTypeRef"`
	Resources        []ComposedResource `json:"resources" yaml:"resources"`
}

// Composition represents a Crossplane Composition document.
type Composition struct {
	APIVersion string          `json:"apiVersion" yaml:"apiVersion"`
	Kind       string          `json:"kind" yaml:"kind"`
	Metadata   map[string]any  `json:"metadata,omitempty" yaml:"metadata"`
	Spec       CompositionSpec `json:"spec" yaml:"spec"`
}

// Claim is a Crossplane claim document. Claims request a composite resource and
// expose operator-supplied parameters that can drift independently of the Composition.
type Claim struct {
	APIVersion string         `json:"apiVersion" yaml:"apiVersion"`
	Kind       string         `json:"kind" yaml:"kind"`
	Metadata   map[string]any `json:"metadata,omitempty" yaml:"metadata"`
	Spec       ClaimSpec      `json:"spec" yaml:"spec"`
}

// ClaimSpec is the spec of a Crossplane claim.
type ClaimSpec struct {
	CompositeTypeRef           TypeRef          `json:"compositeTypeRef" yaml:"compositeTypeRef"`
	Parameters                 map[string]any   `json:"parameters,omitempty" yaml:"parameters"`
	CompositionRef             map[string]any   `json:"compositionRef,omitempty" yaml:"compositionRef"`
	CompositionSelector        map[string]any   `json:"compositionSelector,omitempty" yaml:"compositionSelector"`
	ResourceRefs               []map[string]any `json:"resourceRefs,omitempty" yaml:"resourceRefs"`
	WriteConnectionSecretToRef map[string]any   `json:"writeConnectionSecretToRef,omitempty" yaml:"writeConnectionSecretToRef"`
}

// ClaimDiff summarizes deterministic drift between two claims.
type ClaimDiff struct {
	TypeChanged        bool     `json:"type_changed"`
	CompositionChanged bool     `json:"composition_changed"`
	AddedParameters    []string `json:"added_parameters,omitempty"`
	RemovedParameters  []string `json:"removed_parameters,omitempty"`
	ChangedParameters  []string `json:"changed_parameters,omitempty"`
	HasDrift           bool     `json:"has_drift"`
	Summary            string   `json:"summary"`
}

// LoadClaim reads and parses a Crossplane claim from a YAML file.
func LoadClaim(path string) (*Claim, error) {
	// #nosec G304 -- operator specified claim file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read claim %s: %w", path, err)
	}
	var claim Claim
	if err := yaml.Unmarshal(data, &claim); err != nil {
		return nil, fmt.Errorf("decode claim %s: %w", path, err)
	}
	if claim.Kind != "" && claim.Kind != "Claim" {
		return nil, fmt.Errorf("expected kind Claim, got %s", claim.Kind)
	}
	return &claim, nil
}

// CompareClaims compares two claims, including parameters, composition selection,
// resource references, and connection secret wiring.
func CompareClaims(a, b *Claim) ClaimDiff {
	diff := ClaimDiff{
		AddedParameters:   []string{},
		RemovedParameters: []string{},
		ChangedParameters: []string{},
	}
	if a == nil || b == nil {
		diff.HasDrift = true
		diff.Summary = "nil claim"
		return diff
	}
	diff.TypeChanged = a.Spec.CompositeTypeRef != b.Spec.CompositeTypeRef
	diff.CompositionChanged = !equalAny(a.Spec.CompositionRef, b.Spec.CompositionRef) ||
		!equalAny(a.Spec.CompositionSelector, b.Spec.CompositionSelector) ||
		!equalAny(a.Spec.WriteConnectionSecretToRef, b.Spec.WriteConnectionSecretToRef) ||
		!equalAny(a.Spec.ResourceRefs, b.Spec.ResourceRefs)

	for key, value := range b.Spec.Parameters {
		oldValue, ok := a.Spec.Parameters[key]
		if !ok {
			diff.AddedParameters = append(diff.AddedParameters, key)
			continue
		}
		if !equalAny(oldValue, value) {
			diff.ChangedParameters = append(diff.ChangedParameters, key)
		}
	}
	for key := range a.Spec.Parameters {
		if _, ok := b.Spec.Parameters[key]; !ok {
			diff.RemovedParameters = append(diff.RemovedParameters, key)
		}
	}
	sort.Strings(diff.AddedParameters)
	sort.Strings(diff.RemovedParameters)
	sort.Strings(diff.ChangedParameters)
	diff.HasDrift = diff.TypeChanged || diff.CompositionChanged ||
		len(diff.AddedParameters) > 0 || len(diff.RemovedParameters) > 0 || len(diff.ChangedParameters) > 0
	diff.Summary = fmt.Sprintf("type_changed=%t composition_changed=%t added=%d removed=%d changed=%d",
		diff.TypeChanged, diff.CompositionChanged,
		len(diff.AddedParameters), len(diff.RemovedParameters), len(diff.ChangedParameters))
	if diff.HasDrift {
		diff.Summary += " (DRIFT DETECTED)"
	} else {
		diff.Summary += " (IN SYNC)"
	}
	return diff
}

func equalAny(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// Diff summarizes the differences between two compositions.
type Diff struct {
	CompositeTypeChanged bool     `json:"composite_type_changed"`
	Added                []string `json:"added,omitempty"`
	Removed              []string `json:"removed,omitempty"`
	Changed              []string `json:"changed,omitempty"`
	HasDrift             bool     `json:"has_drift"`
	Summary              string   `json:"summary"`
}

// Load reads and parses a Crossplane composition from a YAML file.
func Load(path string) (*Composition, error) {
	// #nosec G304 -- operator specified composition file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read composition %s: %w", path, err)
	}
	var c Composition
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("decode composition %s: %w", path, err)
	}
	if c.Kind != "" && c.Kind != "Composition" {
		return nil, fmt.Errorf("expected kind Composition, got %s", c.Kind)
	}
	return &c, nil
}

// Compare compares two compositions and returns a deterministic diff.
func Compare(a, b *Composition) Diff {
	diff := Diff{Added: []string{}, Removed: []string{}, Changed: []string{}}
	if a == nil || b == nil {
		diff.HasDrift = true
		diff.Summary = "nil composition"
		return diff
	}
	if a.Spec.CompositeTypeRef != b.Spec.CompositeTypeRef {
		diff.CompositeTypeChanged = true
	}

	aMap := make(map[string]ComposedResource)
	for _, r := range a.Spec.Resources {
		aMap[r.Name] = r
	}
	bMap := make(map[string]ComposedResource)
	for _, r := range b.Spec.Resources {
		bMap[r.Name] = r
	}

	var added, removed, changed []string
	for name := range bMap {
		if _, ok := aMap[name]; !ok {
			added = append(added, name)
		}
	}
	for name := range aMap {
		if _, ok := bMap[name]; !ok {
			removed = append(removed, name)
		}
	}
	for name, ra := range aMap {
		if rb, ok := bMap[name]; ok {
			if !equalComposed(ra, rb) {
				changed = append(changed, name)
			}
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	diff.Added = added
	diff.Removed = removed
	diff.Changed = changed
	diff.HasDrift = diff.CompositeTypeChanged || len(added) > 0 || len(removed) > 0 || len(changed) > 0
	diff.Summary = fmt.Sprintf("added=%d removed=%d changed=%d composite_changed=%t",
		len(added), len(removed), len(changed), diff.CompositeTypeChanged)
	if diff.HasDrift {
		diff.Summary += " (DRIFT DETECTED)"
	} else {
		diff.Summary += " (IN SYNC)"
	}
	return diff
}

func equalComposed(a, b ComposedResource) bool {
	if a.Base.APIVersion != b.Base.APIVersion || a.Base.Kind != b.Base.Kind {
		return false
	}
	// Compare patches via JSON.
	ab, _ := json.Marshal(a.Patches)
	bb, _ := json.Marshal(b.Patches)
	return string(ab) == string(bb)
}

// Save writes a composition to disk as YAML.
func Save(path string, c *Composition) (err error) {
	if c == nil {
		return fmt.Errorf("composition is required")
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("encode composition: %v", r)
		}
	}()
	data, _ := yaml.Marshal(c)
	// #nosec G306 -- composition file intentionally readable
	if werr := os.WriteFile(path, data, 0o644); werr != nil {
		return fmt.Errorf("write composition %s: %w", path, werr)
	}
	return nil
}
