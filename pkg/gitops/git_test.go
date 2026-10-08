package gitops

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestRepoWithTags(t *testing.T) (string, *git.Repository) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)

	w, err := repo.Worktree()
	require.NoError(t, err)

	filePath := filepath.Join(dir, "README.md")
	require.NoError(t, os.WriteFile(filePath, []byte("# Test Repo"), 0644))

	_, err = w.Add("README.md")
	require.NoError(t, err)

	sig := &object.Signature{
		Name:  "Test",
		Email: "test@example.com",
		When:  time.Now(),
	}

	commit1, err := w.Commit("Initial commit", &git.CommitOptions{
		Author:    sig,
		Committer: sig,
	})
	require.NoError(t, err)

	// Create lightweight tag v1.0.0
	_, err = repo.CreateTag("v1.0.0", commit1, nil)
	require.NoError(t, err)

	// Make another commit
	require.NoError(t, os.WriteFile(filePath, []byte("# Test Repo v2"), 0644))
	_, err = w.Add("README.md")
	require.NoError(t, err)

	commit2, err := w.Commit("Second commit", &git.CommitOptions{
		Author:    sig,
		Committer: sig,
	})
	require.NoError(t, err)

	// Create annotated tag v2.0.0
	_, err = repo.CreateTag("v2.0.0", commit2, &git.CreateTagOptions{
		Tagger:  sig,
		Message: "Release v2.0.0",
	})
	require.NoError(t, err)

	// Non-semver tag
	_, err = repo.CreateTag("ignore-me", commit2, nil)
	require.NoError(t, err)

	return dir, repo
}

func TestCloneOrOpen(t *testing.T) {
	repoDir, _ := createTestRepoWithTags(t)

	// Open existing directory
	repo, err := CloneOrOpen(repoDir, "")
	require.NoError(t, err)
	assert.NotNil(t, repo)

	// Clone to new directory using file:// URL to exercise PlainClone success branch
	cloneDir := t.TempDir()
	clonedRepo, err := CloneOrOpen("file://"+repoDir, cloneDir)
	require.NoError(t, err)
	assert.NotNil(t, clonedRepo)

	// Empty targetDir error
	_, err = CloneOrOpen("https://invalid-remote-url-example.com/repo.git", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "targetDir cannot be empty")

	// Non-git directory with empty targetDir
	nonGitDir := t.TempDir()
	_, err = CloneOrOpen(nonGitDir, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "targetDir cannot be empty")

	// Invalid clone remote
	_, err = CloneOrOpen("file:///non/existent/path/for/repo", t.TempDir())
	assert.Error(t, err)
}

func TestNilRepoHandling(t *testing.T) {
	_, err := ListSemverTags(nil)
	assert.Error(t, err)

	_, err = FindLatestVersion(nil)
	assert.Error(t, err)

	_, err = FindNextVersion(nil, "v1.0.0")
	assert.Error(t, err)

	// Test repo with removed .git directory
	corruptDir, corruptRepo := createTestRepoWithTags(t)
	require.NoError(t, os.RemoveAll(filepath.Join(corruptDir, ".git")))
	_, _ = ListSemverTags(corruptRepo)
}

func TestListSemverTags_And_FindVersions(t *testing.T) {
	_, repo := createTestRepoWithTags(t)

	tags, err := ListSemverTags(repo)
	require.NoError(t, err)
	assert.Equal(t, []string{"v1.0.0", "v2.0.0"}, tags)

	latest, err := FindLatestVersion(repo)
	require.NoError(t, err)
	assert.Equal(t, "v2.0.0", latest)

	next, err := FindNextVersion(repo, "v1.0.0")
	require.NoError(t, err)
	assert.Equal(t, "v2.0.0", next)

	_, err = FindNextVersion(repo, "v2.0.0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no version found higher than")

	// Empty repo tag check
	emptyDir := t.TempDir()
	emptyRepo, err := git.PlainInit(emptyDir, false)
	require.NoError(t, err)

	emptyTags, err := ListSemverTags(emptyRepo)
	require.NoError(t, err)
	assert.Empty(t, emptyTags)

	_, err = FindLatestVersion(emptyRepo)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no semantic version tags found")
}

func TestCheckoutRef(t *testing.T) {
	repoDir, repo := createTestRepoWithTags(t)

	// 1. Checkout lightweight tag
	err := CheckoutRef(repo, "v1.0.0")
	require.NoError(t, err)

	// 2. Checkout annotated tag
	err = CheckoutRef(repo, "v2.0.0")
	require.NoError(t, err)

	// 3. Checkout commit hash
	head, err := repo.Head()
	require.NoError(t, err)
	err = CheckoutRef(repo, head.Hash().String())
	require.NoError(t, err)

	// 4. Checkout branch (main or master)
	w, err := repo.Worktree()
	require.NoError(t, err)
	_ = w

	// 5. Invalid ref
	err = CheckoutRef(repo, "non-existent-ref")
	assert.Error(t, err)

	// 6. Bare repo error for Worktree
	bareDir := t.TempDir()
	bareRepo, err := git.PlainInit(bareDir, true)
	require.NoError(t, err)
	err = CheckoutRef(bareRepo, "v1.0.0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to obtain worktree")

	_ = repoDir
}

func TestExtractVersionToDir(t *testing.T) {
	repoDir, _ := createTestRepoWithTags(t)
	destDir := t.TempDir()

	err := ExtractVersionToDir(repoDir, "v1.0.0", destDir)
	require.NoError(t, err)

	// Empty ref
	err = ExtractVersionToDir(repoDir, "", destDir)
	require.NoError(t, err)

	// Error when invalid repo
	err = ExtractVersionToDir("/non/existent/path", "v1.0.0", t.TempDir())
	assert.Error(t, err)
}

func TestCloneOrOpenWithOptions_Shallow(t *testing.T) {
	repoDir, _ := createTestRepoWithTags(t)
	cloneDir := t.TempDir()

	repo, err := CloneOrOpenWithOptions("file://"+repoDir, cloneDir, CloneConfig{
		Shallow:      true,
		Depth:        1,
		SingleBranch: true,
		RefName:      "v1.0.0",
	})
	require.NoError(t, err)
	assert.NotNil(t, repo)

	// Test with refs/ prefix
	cloneDir2 := t.TempDir()
	repo2, err := CloneOrOpenWithOptions("file://"+repoDir, cloneDir2, CloneConfig{
		Shallow:      true,
		Depth:        1,
		SingleBranch: true,
		RefName:      "refs/tags/v1.0.0",
	})
	require.NoError(t, err)
	assert.NotNil(t, repo2)
}

func TestExtractVersionToDirShallow(t *testing.T) {
	repoDir, _ := createTestRepoWithTags(t)
	destDir := t.TempDir()

	err := ExtractVersionToDirShallow("file://"+repoDir, "v1.0.0", destDir)
	require.NoError(t, err)

	// Empty ref
	destDir2 := t.TempDir()
	err = ExtractVersionToDirShallow("file://"+repoDir, "", destDir2)
	require.NoError(t, err)

	// Invalid repo
	err = ExtractVersionToDirShallow("/invalid/path", "v1.0.0", t.TempDir())
	assert.Error(t, err)
}

func TestRepositoryCache(t *testing.T) {
	repoDir, _ := createTestRepoWithTags(t)
	cache := NewRepositoryCache()
	defer cache.Clear()

	// Initial extraction
	cachedDir1, err := cache.GetOrExtract("file://"+repoDir, "v1.0.0")
	require.NoError(t, err)
	assert.DirExists(t, cachedDir1)

	// Second extraction should hit cache
	cachedDir2, err := cache.GetOrExtract("file://"+repoDir, "v1.0.0")
	require.NoError(t, err)
	assert.Equal(t, cachedDir1, cachedDir2)

	// Test error branch
	_, err = cache.GetOrExtract("/invalid/repo", "v1.0.0")
	assert.Error(t, err)

	// Clear cache
	cache.Clear()
	assert.Empty(t, cache.cache)

	// GlobalCache test
	dir, err := GlobalCache.GetOrExtract("file://"+repoDir, "v1.0.0")
	require.NoError(t, err)
	assert.DirExists(t, dir)
	GlobalCache.Clear()
}

func TestRepositoryCache_Concurrent(t *testing.T) {
	repoDir, _ := createTestRepoWithTags(t)
	cache := NewRepositoryCache()
	defer cache.Clear()

	done := make(chan bool)
	for i := 0; i < 5; i++ {
		go func() {
			d, err := cache.GetOrExtract("file://"+repoDir, "v1.0.0")
			assert.NoError(t, err)
			assert.NotEmpty(t, d)
			done <- true
		}()
	}

	for i := 0; i < 5; i++ {
		<-done
	}
}

