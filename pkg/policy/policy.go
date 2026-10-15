package policy

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Policy configures CI enforcement independently from compatibility analysis.
type Policy struct {
	BlockBreaking   bool   `json:"block_breaking,omitempty"`
	BlockWarnings   bool   `json:"block_warnings,omitempty"`
	BlockDowngrades bool   `json:"block_downgrades,omitempty"`
	MaxBump         string `json:"max_bump,omitempty"`
}

// Violation describes a policy gate that failed.
type Violation struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// Input is the compatibility summary evaluated against a policy.
type Input struct {
	BreakingChanges int
	Warnings        int
	IsDowngrade     bool
	BumpType        string
}

// Load reads and validates a JSON policy from path. The path is supplied directly by the operator
// through the --policy flag; it is never derived from analyzed Terraform content.
func Load(path string) (*Policy, error) {
	// #nosec G304 -- operator supplied --policy path, not derived from analyzed input
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open policy file: %w", err)
	}
	defer func() { _ = file.Close() }()
	return Decode(file)
}

// Decode parses a policy and rejects unknown fields and trailing JSON values.
func Decode(reader io.Reader) (*Policy, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var policy Policy
	if err := decoder.Decode(&policy); err != nil {
		return nil, fmt.Errorf("decode policy: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode policy: multiple JSON values")
		}
		return nil, fmt.Errorf("decode policy trailing data: %w", err)
	}
	policy.MaxBump = strings.ToUpper(strings.TrimSpace(policy.MaxBump))
	switch policy.MaxBump {
	case "", "PATCH", "MINOR", "MAJOR":
	default:
		return nil, fmt.Errorf("invalid max_bump %q: expected PATCH, MINOR, or MAJOR", policy.MaxBump)
	}
	return &policy, nil
}

// Evaluate returns the deterministic set of policy violations.
func Evaluate(policy *Policy, input Input) []Violation {
	if policy == nil {
		return nil
	}
	var violations []Violation
	if policy.BlockBreaking && input.BreakingChanges > 0 {
		violations = append(violations, Violation{
			Rule:    "block_breaking",
			Message: fmt.Sprintf("policy blocks upgrades with %d breaking change(s)", input.BreakingChanges),
		})
	}
	if policy.BlockWarnings && input.Warnings > 0 {
		violations = append(violations, Violation{
			Rule:    "block_warnings",
			Message: fmt.Sprintf("policy blocks upgrades with %d warning(s)", input.Warnings),
		})
	}
	if policy.BlockDowngrades && input.IsDowngrade {
		violations = append(violations, Violation{
			Rule:    "block_downgrades",
			Message: "policy blocks version downgrades",
		})
	}
	if max := bumpRank(policy.MaxBump); max > 0 {
		got := strings.ToUpper(strings.TrimSpace(input.BumpType))
		bump := bumpRank(got)
		known := bump > 0 || got == "IDENTICAL"
		if !known || bump > max {
			if got == "" {
				got = "unknown (version information unavailable)"
			}
			violations = append(violations, Violation{
				Rule:    "max_bump",
				Message: fmt.Sprintf("policy allows at most a %s version bump, got %s", policy.MaxBump, got),
			})
		}
	}
	return violations
}

func bumpRank(bump string) int {
	switch strings.ToUpper(strings.TrimSpace(bump)) {
	case "PATCH":
		return 1
	case "MINOR":
		return 2
	case "MAJOR":
		return 3
	default:
		return 0
	}
}
