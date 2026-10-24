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
		baseRepo, subpath := extractGitBaseAndSubpath(moduleSource)
		repositoryPath := baseRepo
		if isGitURL(repositoryPath) {
			// Reuse the shared repository clone so that listing tags and extracting the
			// intermediate versions never downloads the same repository twice.
			var errClone error
			repositoryPath, errClone = gitops.GlobalCache.SharedClone(repositoryPath)
			if errClone != nil {
				return nil, fmt.Errorf("clone repository for upgrade planning: %w", errClone)
			}
		}
		repo, errOpen := gitops.CloneOrOpen(baseRepo, repositoryPath)
		if errOpen != nil {
			return nil, fmt.Errorf("open repository for upgrade planning: %w", errOpen)
		}
		if subpath == "" {
			subpath = registry.SubmodulePath(moduleSource)
		}
		versions, _ = gitops.ListScopedSemverTags(repo, subpath)
	}

	// Filter out prereleases from intermediate versions unless toVersion or fromVersion is a prerelease
	stable := semverutil.FilterPrereleases(versions)
	if len(stable) > 0 {
		versions = stable
	}

	var candidates []string
	for _, vStr := range versions {
		if semverutil.Compare(vStr, fromVersion) > 0 && semverutil.Compare(vStr, toVersion) < 0 {
			candidates = append(candidates, vStr)
		}
	}

	pruned := pruneToSafeHarbor(candidates)

	plan := []string{fromVersion}
	plan = append(plan, pruned...)
	plan = append(plan, toVersion)
	plan = semverutil.Sort(plan)
	return plan, nil
}

func extractGitBaseAndSubpath(source string) (base string, subpath string) {
	offset := 0
	if schemeIdx := strings.Index(source, "://"); schemeIdx != -1 {
		offset = schemeIdx + 3
	}
	if idx := strings.Index(source[offset:], "//"); idx != -1 {
		fullIdx := offset + idx
		return source[:fullIdx], strings.Trim(source[fullIdx+2:], "/")
	}
	return source, ""
}

func pruneToSafeHarbor(candidates []string) []string {
	if len(candidates) <= 2 {
		return candidates
	}
	sorted := semverutil.Sort(candidates)
	groups := make(map[string][]string)
	var order []string
	for _, v := range sorted {
		norm := strings.TrimPrefix(semverutil.Normalize(v), "v")
		parts := strings.Split(norm, ".")
		var key string
		if len(parts) >= 2 {
			key = parts[0] + "." + parts[1]
		} else {
			key = norm
		}
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], v)
	}

	var result []string
	for _, key := range order {
		vers := groups[key]
		first := vers[0]
		last := vers[len(vers)-1]
		normFirst := strings.TrimPrefix(semverutil.Normalize(first), "v")
		if strings.HasSuffix(normFirst, ".0.0") && first != last {
			result = append(result, first)
		}
		result = append(result, last)
	}
	return semverutil.Sort(result)
}

func isGitSource(source string) bool {
	base, _ := extractGitBaseAndSubpath(source)
	if isGitURL(base) {
		return true
	}
	info, err := os.Stat(base)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(strings.TrimRight(base, "/") + "/.git")
	return err == nil
}

func isGitURL(source string) bool {
	return gitops.IsGitURL(source)
}
