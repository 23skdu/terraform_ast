package ast

import (
	"fmt"
	"sort"
	"strings"
)

// ResourceAddress represents a canonical hierarchical Terraform resource instance address
// (e.g., "aws_subnet.this", "data.aws_ami.ubuntu", "module.vpc.aws_subnet.this", or "aws_subnet.this[0]").
type ResourceAddress struct {
	ModulePath string `json:"module_path,omitempty"`
	Mode       string `json:"mode"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	IndexKey   string `json:"index_key,omitempty"`
}

// FullAddress renders the canonical Terraform resource instance address string.
func (ra ResourceAddress) FullAddress() string {
	if ra.Type == "" && ra.Name == "" {
		return ""
	}
	var sb strings.Builder
	if ra.ModulePath != "" {
		sb.WriteString(ra.ModulePath)
		sb.WriteString(".")
	}
	if ra.Mode == "data" {
		sb.WriteString("data.")
	}
	sb.WriteString(ra.Type)
	sb.WriteString(".")
	sb.WriteString(ra.Name)
	if ra.IndexKey != "" {
		sb.WriteString("[")
		sb.WriteString(ra.IndexKey)
		sb.WriteString("]")
	}
	return sb.String()
}

// String returns the canonical address string.
func (ra ResourceAddress) String() string {
	return ra.FullAddress()
}

// ParseResourceAddress parses a canonical Terraform address string into a ResourceAddress struct.
func ParseResourceAddress(addr string) ResourceAddress {
	cleaned := strings.TrimSpace(addr)
	res := ResourceAddress{
		Mode: "managed",
	}

	// Extract index key if present (e.g. [0] or ["a"])
	if idx := strings.Index(cleaned, "["); idx != -1 && strings.HasSuffix(cleaned, "]") {
		res.IndexKey = cleaned[idx+1 : len(cleaned)-1]
		cleaned = cleaned[:idx]
	}

	parts := strings.Split(cleaned, ".")
	var moduleParts []string
	i := 0
	for i < len(parts) {
		if parts[i] == "module" && i+1 < len(parts) {
			moduleParts = append(moduleParts, parts[i]+"."+parts[i+1])
			i += 2
		} else {
			break
		}
	}
	if len(moduleParts) > 0 {
		res.ModulePath = strings.Join(moduleParts, ".")
	}

	rem := parts[i:]
	if len(rem) > 0 && rem[0] == "data" {
		res.Mode = "data"
		rem = rem[1:]
	}
	if len(rem) >= 2 {
		res.Type = rem[0]
		res.Name = rem[1]
	} else if len(rem) == 1 {
		res.Name = rem[0]
	}

	return res
}

// ResourceAddressShift records an address or topological change between two versions of a module.
type ResourceAddressShift struct {
	OldAddress   ResourceAddress `json:"old_address"`
	NewAddress   ResourceAddress `json:"new_address"`
	IsRecreation bool            `json:"is_recreation"`
	Reason       string          `json:"reason"`
}

// EvaluationNode represents an element in the intermediate evaluation graph (IR).
type EvaluationNode struct {
	Address  ResourceAddress `json:"address"`
	Resource ResourceDef     `json:"resource"`
}

// EvaluationGraph represents the hierarchical intermediate representation of resources in a module.
type EvaluationGraph struct {
	Nodes     map[string]EvaluationNode `json:"nodes"`
	Addresses []ResourceAddress         `json:"addresses"`
}

// ExtractResourceAddresses extracts all declared resource addresses from a ModuleInfo.
func ExtractResourceAddresses(mod *ModuleInfo) []ResourceAddress {
	if mod == nil {
		return nil
	}

	var addresses []ResourceAddress

	// Managed resources
	managedKeys := make([]string, 0, len(mod.ManagedResources))
	for k := range mod.ManagedResources {
		managedKeys = append(managedKeys, k)
	}
	sort.Strings(managedKeys)
	for _, k := range managedKeys {
		r := mod.ManagedResources[k]
		addresses = append(addresses, ResourceAddress{
			Mode: "managed",
			Type: r.Type,
			Name: r.Name,
		})
	}

	// Data resources
	dataKeys := make([]string, 0, len(mod.DataResources))
	for k := range mod.DataResources {
		dataKeys = append(dataKeys, k)
	}
	sort.Strings(dataKeys)
	for _, k := range dataKeys {
		r := mod.DataResources[k]
		addresses = append(addresses, ResourceAddress{
			Mode: "data",
			Type: r.Type,
			Name: r.Name,
		})
	}

	return addresses
}

// BuildEvaluationGraph constructs an IR graph from a ModuleInfo.
func BuildEvaluationGraph(mod *ModuleInfo) *EvaluationGraph {
	graph := &EvaluationGraph{
		Nodes: make(map[string]EvaluationNode),
	}
	if mod == nil {
		return graph
	}

	addrs := ExtractResourceAddresses(mod)
	graph.Addresses = addrs

	for _, addr := range addrs {
		full := addr.FullAddress()
		var def ResourceDef
		if addr.Mode == "data" {
			mapKey := "data." + addr.Type + "." + addr.Name
			def = mod.DataResources[mapKey]
			if def.Name == "" {
				def = mod.DataResources[addr.Type+"."+addr.Name]
			}
		} else {
			mapKey := addr.Type + "." + addr.Name
			def = mod.ManagedResources[mapKey]
		}
		graph.Nodes[full] = EvaluationNode{
			Address:  addr,
			Resource: def,
		}
	}

	return graph
}

// DetectAddressShifts compares old and new module graphs to detect topology or address shifts.
func DetectAddressShifts(oldMod, newMod *ModuleInfo) []ResourceAddressShift {
	if oldMod == nil || newMod == nil {
		return nil
	}

	oldAddrs := ExtractResourceAddresses(oldMod)
	newAddrs := ExtractResourceAddresses(newMod)

	oldMap := make(map[string]ResourceAddress, len(oldAddrs))
	for _, a := range oldAddrs {
		oldMap[a.FullAddress()] = a
	}
	newMap := make(map[string]ResourceAddress, len(newAddrs))
	for _, a := range newAddrs {
		newMap[a.FullAddress()] = a
	}

	var shifts []ResourceAddressShift

	// Find resources in old that do not exist at identical address in new
	for _, oldAddr := range oldAddrs {
		oldKey := oldAddr.FullAddress()
		if _, exists := newMap[oldKey]; exists {
			continue
		}

		// Resource disappeared from its old address.
		// Check if a resource of the same type exists in new under a different name or module
		var candidate *ResourceAddress
		for _, newAddr := range newAddrs {
			if _, inOld := oldMap[newAddr.FullAddress()]; !inOld && newAddr.Type == oldAddr.Type && newAddr.Mode == oldAddr.Mode {
				c := newAddr
				candidate = &c
				break
			}
		}

		if candidate != nil {
			shifts = append(shifts, ResourceAddressShift{
				OldAddress:   oldAddr,
				NewAddress:   *candidate,
				IsRecreation: true,
				Reason:       fmt.Sprintf("Resource address shifted from %s to %s without state migration (triggers ForceNew recreation)", oldKey, candidate.FullAddress()),
			})
		} else {
			// Resource was removed or relocated into a child module
			shifts = append(shifts, ResourceAddressShift{
				OldAddress:   oldAddr,
				NewAddress:   ResourceAddress{},
				IsRecreation: true,
				Reason:       fmt.Sprintf("Resource %s was removed or relocated (state instance dropped)", oldKey),
			})
		}
	}

	// Suppress IsRecreation when an upstream moved {} block covers the transition.
	covered := make(map[string]bool)
	for _, mb := range newMod.MovedBlocks {
		covered[mb.From+"->"+mb.To] = true
	}
	for _, mb := range oldMod.MovedBlocks {
		covered[mb.From+"->"+mb.To] = true
	}
	for i := range shifts {
		key := shifts[i].OldAddress.FullAddress() + "->" + shifts[i].NewAddress.FullAddress()
		if covered[key] {
			shifts[i].IsRecreation = false
			shifts[i].Reason = fmt.Sprintf("Resource address migrated via moved block from %s to %s", shifts[i].OldAddress.FullAddress(), shifts[i].NewAddress.FullAddress())
		}
	}

	// Sort shifts deterministically by old address
	sort.Slice(shifts, func(i, j int) bool {
		return shifts[i].OldAddress.FullAddress() < shifts[j].OldAddress.FullAddress()
	})

	return shifts
}
