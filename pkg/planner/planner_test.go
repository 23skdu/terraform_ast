package planner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/23skdu/terraform_ast/pkg/gitops"
	"github.com/stretchr/testify/assert"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/require"
)

func TestPlanUpgrade(t *testing.T) {
	p := NewUpgradePlanner(nil)

	plan, err := p.PlanUpgrade(context.Background(), "hashicorp/consul/aws", "0.1.0", "0.2.0")
	if err != nil {
		t.Fatalf("PlanUpgrade failed: %v", err)
	}
	if len(plan) == 0 {
		t.Fatalf("expected plan")
	}

	plan2, err := p.PlanUpgrade(context.Background(), "local/module", "0.1.0", "0.2.0")
	if err != nil {
		t.Fatalf("PlanUpgrade failed: %v", err)
	}
	if len(plan2) != 2 {
		t.Fatalf("expected plan of length 2")
	}

	_, err = p.PlanUpgrade(context.Background(), "local/module", "invalid", "0.2.0")
	if err == nil {
		t.Fatalf("expected error")
	}

	_, err = p.PlanUpgrade(context.Background(), "local/module", "0.1.0", "invalid")
	if err == nil {
		t.Fatalf("expected error")
	}

	plan3, err := p.PlanUpgrade(context.Background(), "local/module", "0.3.0", "0.2.0")
	if err != nil {
		t.Fatalf("PlanUpgrade failed: %v", err)
	}
	if plan3[0] != "0.3.0" {
		t.Fatalf("expected fromVersion")
	}

	// Trigger ListVersions failure
	_, err = p.PlanUpgrade(context.Background(), "hashicorp/invalid/aws", "0.1.0", "0.2.0")
	if err == nil {
		t.Fatalf("expected ListVersions to fail")
	}
}

func TestPlanUpgradeIncludesIntermediateGitTags(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	signature := &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}

	for _, version := range []string{"v1.0.0", "v1.2.0", "v1.2.0-rc.1", "v2.0.0", "not-a-version"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), fmt.Appendf(nil, "# %s\n", version), 0600))
		_, err = worktree.Add("main.tf")
		require.NoError(t, err)
		commit, err := worktree.Commit(version, &git.CommitOptions{Author: signature, Committer: signature})
		require.NoError(t, err)
		if version != "not-a-version" {
			_, err = repo.CreateTag(version, commit, nil)
			require.NoError(t, err)
		}
	}

	plan, err := NewUpgradePlanner(nil).PlanUpgrade(context.Background(), dir, "1.0.0", "2.0.0")
	require.NoError(t, err)
	require.Equal(t, []string{"1.0.0", "v1.2.0-rc.1", "v1.2.0", "2.0.0"}, plan)
}

func TestPlanUpgradeClonesGitURLThroughSharedCache(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	signature := &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}

	for _, version := range []string{"v1.0.0", "v1.1.0", "v2.0.0"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), fmt.Appendf(nil, "# %s\n", version), 0600))
		_, err = worktree.Add("main.tf")
		require.NoError(t, err)
		commit, err := worktree.Commit(version, &git.CommitOptions{Author: signature, Committer: signature})
		require.NoError(t, err)
		_, err = repo.CreateTag(version, commit, nil)
		require.NoError(t, err)
	}

	t.Cleanup(gitops.GlobalCache.Clear)
	remote := "file://" + dir

	plan, err := NewUpgradePlanner(nil).PlanUpgrade(context.Background(), remote, "1.0.0", "2.0.0")
	require.NoError(t, err)
	require.Equal(t, []string{"1.0.0", "v1.1.0", "2.0.0"}, plan)

	// The remote repository is downloaded once and the clone is reused.
	require.Len(t, gitops.CachedRepositoryDirs(), 1, "a single shared clone should be cached")

	// Repeating the request resolves from the cache rather than cloning again.
	plan, err = NewUpgradePlanner(nil).PlanUpgrade(context.Background(), remote, "1.0.0", "2.0.0")
	require.NoError(t, err)
	require.Equal(t, []string{"1.0.0", "v1.1.0", "2.0.0"}, plan)
	require.Len(t, gitops.CachedRepositoryDirs(), 1)

	// Unreachable remotes surface as an error instead of an empty plan.
	_, err = NewUpgradePlanner(nil).PlanUpgrade(context.Background(), "file:///nonexistent/repo.git", "1.0.0", "2.0.0")
	require.Error(t, err)
}

func TestIsGitSourceAndIsGitURL(t *testing.T) {
	require.True(t, isGitURL("https://github.com/org/repo.git"))
	require.True(t, isGitURL("file:///srv/repo"))
	require.True(t, isGitURL("git@github.com:org/repo.git"))
	require.True(t, isGitURL("github.com/org/repo"))
	require.True(t, isGitURL("bitbucket.org/org/repo"))
	require.True(t, isGitURL("git://github.com/org/repo.git"))
	require.False(t, isGitURL("./local"))

	repoDir := t.TempDir()
	_, err := git.PlainInit(repoDir, false)
	require.NoError(t, err)
	require.True(t, isGitSource(repoDir))
	require.True(t, isGitSource("https://github.com/org/repo.git"))
	require.False(t, isGitSource("./local/module"))
	require.False(t, isGitSource(t.TempDir()))
}

func TestPlanUpgradeRejectsUnreadableLocalRepository(t *testing.T) {
	// isGitSource only checks for a .git entry, so a corrupt repository reaches CloneOrOpen.
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0750))

	_, err := NewUpgradePlanner(nil).PlanUpgrade(context.Background(), dir, "1.0.0", "2.0.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open repository for upgrade planning")
}

func TestPlanUpgradeClampsToEndpointVersions(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	signature := &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}

	for _, version := range []string{"v0.9.0", "v1.0.0", "v1.5.0", "v2.0.0", "v3.0.0"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte("# "+version), 0600))
		_, err = worktree.Add("main.tf")
		require.NoError(t, err)
		commit, err := worktree.Commit(version, &git.CommitOptions{Author: signature, Committer: signature})
		require.NoError(t, err)
		_, err = repo.CreateTag(version, commit, nil)
		require.NoError(t, err)
	}

	planner := NewUpgradePlanner(nil)

	// Only versions strictly inside the window are intermediate steps.
	plan, err := planner.PlanUpgrade(context.Background(), dir, "1.0.0", "2.0.0")
	require.NoError(t, err)
	require.Equal(t, []string{"1.0.0", "v1.5.0", "2.0.0"}, plan)

	// Equal or descending windows collapse to a single step.
	plan, err = planner.PlanUpgrade(context.Background(), dir, "2.0.0", "2.0.0")
	require.NoError(t, err)
	require.Equal(t, []string{"2.0.0"}, plan)

	plan, err = planner.PlanUpgrade(context.Background(), dir, "2.0.0", "1.0.0")
	require.NoError(t, err)
	require.Equal(t, []string{"2.0.0"}, plan)

	// Sources that are neither registry nor git sources only yield the two endpoints.
	plan, err = planner.PlanUpgrade(context.Background(), "./local/module", "1.0.0", "2.0.0")
	require.NoError(t, err)
	require.Equal(t, []string{"1.0.0", "2.0.0"}, plan)
}
