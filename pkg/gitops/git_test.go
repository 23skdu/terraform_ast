package gitops

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tempDir adapts testing.T.TempDir to the cache's DirFactory signature.
func tempDir(t *testing.T) DirFactory {
	return func() (string, error) { return t.TempDir(), nil }
}

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

	// Test repo with removed .git directory
	corruptDir, corruptRepo := createTestRepoWithTags(t)
	require.NoError(t, os.RemoveAll(filepath.Join(corruptDir, ".git")))
	_, _ = ListSemverTags(corruptRepo)
}

func TestListSemverTags(t *testing.T) {
	_, repo := createTestRepoWithTags(t)

	tags, err := ListSemverTags(repo)
	require.NoError(t, err)
	assert.Equal(t, []string{"v1.0.0", "v2.0.0"}, tags)

	// Empty repo tag check
	emptyDir := t.TempDir()
	emptyRepo, err := git.PlainInit(emptyDir, false)
	require.NoError(t, err)

	emptyTags, err := ListSemverTags(emptyRepo)
	require.NoError(t, err)
	assert.Empty(t, emptyTags)
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

func TestExtractVersionToDirShallow_LocalRepo(t *testing.T) {
	repoDir, _ := createTestRepoWithTags(t)
	destDir := t.TempDir()

	err := ExtractVersionToDirShallow(repoDir, "v1.0.0", destDir)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(destDir, "README.md"))

	// Re-using an existing checkout opens it in place instead of cloning again.
	err = ExtractVersionToDirShallow(repoDir, "", destDir)
	require.NoError(t, err)
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
	cachedDir1, err := cache.GetOrExtract("file://"+repoDir, "v1.0.0", tempDir(t))
	require.NoError(t, err)
	assert.DirExists(t, cachedDir1)
	assert.FileExists(t, filepath.Join(cachedDir1, "README.md"))

	// Second extraction should hit cache
	cachedDir2, err := cache.GetOrExtract("file://"+repoDir, "v1.0.0", tempDir(t))
	require.NoError(t, err)
	assert.Equal(t, cachedDir1, cachedDir2)

	// A different ref is extracted separately but reuses the same local source
	cachedDir3, err := cache.GetOrExtract("file://"+repoDir, "v2.0.0", tempDir(t))
	require.NoError(t, err)
	assert.NotEqual(t, cachedDir1, cachedDir3)
	assert.DirExists(t, cachedDir3)

	// Test error branch
	_, err = cache.GetOrExtract("/invalid/repo", "v1.0.0", tempDir(t))
	assert.Error(t, err)

	// Clear cache removes every cached directory
	cache.Clear()
	assert.Empty(t, cache.entries)
	assert.NoDirExists(t, cachedDir1)
	assert.NoDirExists(t, cachedDir3)

	// GlobalCache test
	GlobalCache.Clear()
	dir, err := GlobalCache.GetOrExtract("file://"+repoDir, "v1.0.0", tempDir(t))
	require.NoError(t, err)
	assert.DirExists(t, dir)
	GlobalCache.Clear()
	assert.NoDirExists(t, dir)
}

func TestRepositoryCache_Concurrent(t *testing.T) {
	repoDir, _ := createTestRepoWithTags(t)
	cache := NewRepositoryCache()
	defer cache.Clear()

	const workers = 8
	results := make(chan string, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			d, err := cache.GetOrExtract("file://"+repoDir, "v1.0.0", tempDir(t))
			if err != nil {
				errs <- err
				return
			}
			results <- d
		}()
	}

	var first string
	for i := 0; i < workers; i++ {
		select {
		case err := <-errs:
			require.NoError(t, err)
		case d := <-results:
			if first == "" {
				first = d
			} else {
				assert.Equal(t, first, d, "concurrent callers must share one cached directory")
			}
		}
	}
	assert.NotEmpty(t, first)
}

func TestExtractVersionToDir_NonGitDir(t *testing.T) {
	plainDir := t.TempDir()
	subDir := filepath.Join(plainDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "main.tf"), []byte(`resource "null_resource" "a" {}`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(plainDir, "root.tf"), []byte(`variable "x" {}`), 0600))

	dest1 := t.TempDir()
	err := ExtractVersionToDirShallow(plainDir, "", dest1)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dest1, "sub", "main.tf"))
	assert.FileExists(t, filepath.Join(dest1, "root.tf"))

	dest2 := t.TempDir()
	err = ExtractVersionToDirShallow(plainDir, "", dest2)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dest2, "sub", "main.tf"))
}

func TestCopyDir_Errors(t *testing.T) {
	// Source does not exist
	err := copyDir("/nonexistent/path", t.TempDir())
	assert.Error(t, err)

	// Unreadable file in source
	src := t.TempDir()
	badFile := filepath.Join(src, "bad.txt")
	require.NoError(t, os.WriteFile(badFile, []byte("secret"), 0600))
	require.NoError(t, os.Chmod(badFile, 0000))
	defer func() { _ = os.Chmod(badFile, 0600) }()
	err = copyDir(src, t.TempDir())
	assert.Error(t, err)
}

func TestIsGitRepo(t *testing.T) {
	repoDir, _ := createTestRepoWithTags(t)
	assert.True(t, isGitRepo(repoDir))

	plainDir := t.TempDir()
	assert.False(t, isGitRepo(plainDir))

	file := filepath.Join(plainDir, "file.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0600))
	assert.False(t, isGitRepo(file))

	assert.False(t, isGitRepo("/nonexistent/path"))
}

func TestCloneOrOpenWithOptions_NotGitRepo(t *testing.T) {
	plainDir := t.TempDir()
	_, err := CloneOrOpenWithOptions(plainDir, t.TempDir(), CloneConfig{})
	assert.Error(t, err)
}

func TestCloneOrOpenWithOptions_DefaultDepth(t *testing.T) {
	repoDir, _ := createTestRepoWithTags(t)
	cloneDir := t.TempDir()

	repo, err := CloneOrOpenWithOptions("file://"+repoDir, cloneDir, CloneConfig{
		Shallow:      true,
		Depth:        0, // triggers depth = 1
		SingleBranch: true,
		RefName:      "v1.0.0",
	})
	require.NoError(t, err)
	assert.NotNil(t, repo)
}

func TestCopyDir_RelAndTraversalErrors(t *testing.T) {
	origRel := relPath
	defer func() { relPath = origRel }()

	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "test.txt"), []byte("data"), 0600))

	// Mock relPath error
	relPath = func(basepath, targpath string) (string, error) {
		return "", errors.New("rel path failure")
	}
	err := copyDir(src, t.TempDir())
	assert.Error(t, err)

	// Mock path traversal error
	relPath = func(basepath, targpath string) (string, error) {
		return "../evil_escape", nil
	}
	err = copyDir(src, t.TempDir())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

func TestRepositoryCache_DirFactoryFailure(t *testing.T) {
	repoDir, _ := createTestRepoWithTags(t)
	cache := NewRepositoryCache()
	defer cache.Clear()

	source := "file://" + repoDir
	failing := func() (string, error) { return "", errors.New("disk full") }
	_, err := cache.GetOrExtract(source, "v1.0.0", failing)
	assert.Error(t, err)
	assert.Empty(t, cache.entries, "a failed build must not be memoised")

	// The same key can still be requested again with a working directory factory.
	dir, err := cache.GetOrExtract(source, "v1.0.0", tempDir(t))
	require.NoError(t, err)
	assert.DirExists(t, dir)
	assert.Len(t, cache.entries, 2) // shared clone + extracted ref
}

func TestRepositoryCache_VanishedDirectoryIsNotReused(t *testing.T) {
	repoDir, _ := createTestRepoWithTags(t)
	cache := NewRepositoryCache()
	defer cache.Clear()
	source := "file://" + repoDir

	cached, err := cache.GetOrExtract(source, "v1.0.0", tempDir(t))
	require.NoError(t, err)
	require.DirExists(t, cached)

	// If the cached directory disappears from disk it must not be handed out again.
	require.NoError(t, os.RemoveAll(cached))
	_, err = cache.GetOrExtract(source, "v1.0.0", tempDir(t))
	assert.Error(t, err, "a cached directory that vanished from disk must not be reused")
}

func TestRepositoryCache_MkdirTempFailure(t *testing.T) {
	orig := mkdirTemp
	defer func() { mkdirTemp = orig }()

	mkdirTemp = func(dir, pattern string) (string, error) {
		return "", errors.New("disk full")
	}

	cache := NewRepositoryCache()
	_, err := cache.GetOrExtract("file:///any", "v1.0.0", tempDir(t))
	assert.Error(t, err)
}

func TestRepositoryCache_SharedClone(t *testing.T) {
	repoDir, _ := createTestRepoWithTags(t)
	cache := NewRepositoryCache()
	defer cache.Clear()

	// Local repositories are used in place instead of being cloned again.
	local, err := cache.SharedClone(repoDir)
	require.NoError(t, err)
	assert.Equal(t, repoDir, local)

	// Non-repository directories are rejected.
	plainDir := t.TempDir()
	_, err = cache.SharedClone(plainDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")

	// Remote sources are cloned once and then reused.
	remote := "file://" + repoDir
	first, err := cache.SharedClone(remote)
	require.NoError(t, err)
	assert.DirExists(t, first)
	second, err := cache.SharedClone(remote)
	require.NoError(t, err)
	assert.Equal(t, first, second)

	opened, err := git.PlainOpen(second)
	require.NoError(t, err)
	tags, err := ListSemverTags(opened)
	require.NoError(t, err)
	assert.Equal(t, []string{"v1.0.0", "v2.0.0"}, tags)

	_, err = cache.SharedClone("file:///non/existent/repo.git")
	assert.Error(t, err)
}

func TestRepositoryCache_FallsBackToRemoteWhenMirrorCannotServeRef(t *testing.T) {
	repoDir, _ := createTestRepoWithTags(t)
	remote := "file://" + repoDir
	cache := NewRepositoryCache()
	defer cache.Clear()

	mirror, err := cache.SharedClone(remote)
	require.NoError(t, err)

	// Break the shared clone so that extracting from it is impossible.
	require.NoError(t, os.RemoveAll(filepath.Join(mirror, ".git")))
	require.NoError(t, os.WriteFile(filepath.Join(mirror, ".git"), []byte("not a repository"), 0600))

	dir, err := cache.GetOrExtract(remote, "v1.0.0", tempDir(t))
	require.NoError(t, err, "extraction must fall back to the original source")
	assert.FileExists(t, filepath.Join(dir, "README.md"))
}

func TestCachedRepositoryDirs(t *testing.T) {
	GlobalCache.Clear()
	t.Cleanup(GlobalCache.Clear)

	assert.Empty(t, CachedRepositoryDirs())

	repoDir, _ := createTestRepoWithTags(t)
	source := "file://" + repoDir
	dir, err := GlobalCache.GetOrExtract(source, "v1.0.0", tempDir(t))
	require.NoError(t, err)

	cached := CachedRepositoryDirs()
	require.Len(t, cached, 2, "the shared clone and the extracted version are both cached")
	assert.Contains(t, cached, dir)
	assert.IsIncreasing(t, cached, "cached directories are reported in a deterministic order")

	GlobalCache.Clear()
	assert.Empty(t, CachedRepositoryDirs())
}
