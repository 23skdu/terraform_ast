package planner

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/gitops"
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

	if !registry.IsRegistrySource(moduleSource) && !isGitSource(moduleSource) {
		return []string{fromVersion, toVersion}, nil
	}

	var versions []string
	if registry.IsRegistrySource(moduleSource) {
		versions, err = p.RegistryClient.ListVersions(ctx, moduleSource)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch versions from registry: %w", err)
		}
	} else {
		repositoryPath := moduleSource
		var temporaryPath string
		if isGitURL(moduleSource) {
			temporaryPath, err = os.MkdirTemp("", "tfastdiff-plan-repo-*")
			if err != nil {
				return nil, fmt.Errorf("create temporary repository directory: %w", err)
			}
			defer os.RemoveAll(temporaryPath)
			repositoryPath = temporaryPath
		}
		repo, err := gitops.CloneOrOpen(moduleSource, repositoryPath)
		if err != nil {
			return nil, fmt.Errorf("open repository for upgrade planning: %w", err)
		}
		versions, err = gitops.ListSemverTags(repo)
		if err != nil {
			return nil, fmt.Errorf("list repository versions: %w", err)
		}
	}

	plan := []string{fromVersion}
	for _, vStr := range versions {
		v, err := semver.NewVersion(vStr)
		if err != nil {
			continue
		}
		if v.GreaterThan(from) && v.LessThan(to) {
			plan = append(plan, vStr)
		}
	}

	plan = append(plan, toVersion)
	plan = semverutil.Sort(plan)
	return plan, nil
}

func isGitSource(source string) bool {
	if isGitURL(source) {
		return true
	}
	info, err := os.Stat(source)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(strings.TrimRight(source, "/") + "/.git")
	return err == nil
}

func isGitURL(source string) bool {
	return strings.HasPrefix(source, "https://") ||
		strings.HasPrefix(source, "http://") ||
		strings.HasPrefix(source, "git@") ||
		strings.HasPrefix(source, "git::")
}
