package changelog

import (
	"context"
	"fmt"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/planner"
	"github.com/23skdu/terraform_ast/pkg/registry"
)

// Aggregator collects changelogs across multiple versions
type Aggregator struct {
	Planner *planner.UpgradePlanner
}

func NewAggregator(p *planner.UpgradePlanner) *Aggregator {
	if p == nil {
		p = planner.NewUpgradePlanner(registry.NewClient(nil))
	}
	return &Aggregator{
		Planner: p,
	}
}

// Aggregate gathers release notes between versions
func (a *Aggregator) Aggregate(ctx context.Context, moduleSource, fromVersion, toVersion string) (string, error) {
	plan, err := a.Planner.PlanUpgrade(ctx, moduleSource, fromVersion, toVersion)
	if err != nil {
		return "", fmt.Errorf("failed to plan upgrade for changelog: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Changelog for %s (%s -> %s)\n\n", moduleSource, fromVersion, toVersion))
	
	if len(plan) <= 1 {
		sb.WriteString("No version changes detected.\n")
		return sb.String(), nil
	}

	sb.WriteString("## Upgrade Path\n")
	for i := 0; i < len(plan)-1; i++ {
		sb.WriteString(fmt.Sprintf("* %s -> %s\n", plan[i], plan[i+1]))
	}
	
	sb.WriteString("\n## Summary\n")
	sb.WriteString("Full intermediate diff analysis is simulated here. Please refer to individual module releases for detailed structural changes.\n")

	return sb.String(), nil
}
