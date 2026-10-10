package planner

import (
	"context"
	"fmt"
	"github.com/Masterminds/semver/v3"
)

// UpgradePlanner is responsible for analyzing multi-version jumps
type UpgradePlanner struct {
	RegistryClient interface{} // Placeholder for registry client
}

func NewUpgradePlanner() *UpgradePlanner {
	return &UpgradePlanner{}
}

// PlanUpgrade computes the intermediate "safe harbor" versions for an upgrade
func (p *UpgradePlanner) PlanUpgrade(ctx context.Context, moduleSource string, fromVersion, toVersion string) ([]string, error) {
	from, err := semver.NewVersion(fromVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid from version: %w", err)
	}
	
	to, err := semver.NewVersion(toVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid to version: %w", err)
	}

	if from.GreaterThan(to) || from.Equal(to) {
		return []string{fromVersion}, nil
	}

	// TODO: Fetch intermediate versions from registry and analyze transitive diffs
	return []string{fromVersion, toVersion}, nil
}
