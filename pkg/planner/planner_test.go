package planner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(fmt.Sprintf("# %s\n", version)), 0600))
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
