package planner

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/gitops"
	"github.com/23skdu/terraform_ast/pkg/registry"
	"github.com/23skdu/terraform_ast/pkg/semverutil"
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
	if !semverutil.IsValid(fromVersion) {
		return nil, fmt.Errorf("invalid from version: %s", fromVersion)
	}

	if !semverutil.IsValid(toVersion) {
		return nil, fmt.Errorf("invalid to version: %s", toVersion)
	}

	if semverutil.Compare(fromVersion, toVersion) >= 0 {
		return []string{fromVersion}, nil
	}

	if !registry.IsRegistrySource(moduleSource) && !isGitSource(moduleSource) {
		return []string{fromVersion, toVersion}, nil
	}

	var (
		versions []string
		err      error
	)
	if registry.IsRegistrySource(moduleSource) {
		versions, err = p.RegistryClient.ListVersions(ctx, moduleSource)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch versions from registry: %w", err)
		}
	} else {
		repositoryPath := moduleSource
		if isGitURL(moduleSource) {
			// Reuse the shared repository clone so that listing tags and extracting the
			// intermediate versions never downloads the same repository twice.
			repositoryPath, err = gitops.GlobalCache.SharedClone(moduleSource)
			if err != nil {
				return nil, fmt.Errorf("clone repository for upgrade planning: %w", err)
			}
		}
		repo, err := gitops.CloneOrOpen(moduleSource, repositoryPath)
		if err != nil {
			return nil, fmt.Errorf("open repository for upgrade planning: %w", err)
		}
		versions, _ = gitops.ListSemverTags(repo)
	}

	plan := []string{fromVersion}
	for _, vStr := range versions {
		// The published version lists are already filtered to valid semver by the registry client
		// and the tag lister, so plain semver comparisons are enough to bound the window.
		if semverutil.Compare(vStr, fromVersion) > 0 && semverutil.Compare(vStr, toVersion) < 0 {
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
	return gitops.IsGitURL(source)
}
