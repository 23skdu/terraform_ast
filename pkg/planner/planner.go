package planner

import (
	"context"
	"fmt"

	"github.com/23skdu/terraform_ast/pkg/registry"
	"github.com/23skdu/terraform_ast/pkg/semverutil"
	"github.com/Masterminds/semver/v3"
)

// UpgradePlanner is responsible for analyzing multi-version jumps
type UpgradePlanner struct {
	RegistryClient *registry.Client
}

func NewUpgradePlanner(rc *registry.Client) *UpgradePlanner {
	if rc == nil {
		rc = registry.NewClient(nil)
	}
	return &UpgradePlanner{
		RegistryClient: rc,
	}
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

	if !registry.IsRegistrySource(moduleSource) {
		return []string{fromVersion, toVersion}, nil
	}

	versions, err := p.RegistryClient.ListVersions(ctx, moduleSource)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch versions from registry: %w", err)
	}

	var plan []string
	for _, vStr := range versions {
		v, err := semver.NewVersion(vStr)
		if err != nil {
			continue
		}
		if (v.GreaterThan(from) || v.Equal(from)) && (v.LessThan(to) || v.Equal(to)) {
			plan = append(plan, vStr)
		}
	}

	if len(plan) == 0 {
		return []string{fromVersion, toVersion}, nil
	}
	
	plan = semverutil.Sort(plan)
	return plan, nil
}
