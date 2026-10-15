package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/23skdu/terraform_ast/pkg/semverutil"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// CloneConfig configures git clone operations.
type CloneConfig struct {
	Shallow      bool
	Depth        int
	SingleBranch bool
	RefName      string
}

// CloneOrOpen opens a local git repository in-place if targetDir is empty or matches path,
// or clones the repository into targetDir.
func CloneOrOpen(repoURLOrPath, targetDir string) (*git.Repository, error) {
	return CloneOrOpenWithOptions(repoURLOrPath, targetDir, CloneConfig{})
}

// CloneOrOpenWithOptions opens or clones a repository with optional shallow cloning.
func CloneOrOpenWithOptions(repoURLOrPath, targetDir string, cfg CloneConfig) (*git.Repository, error) {
	if targetDir == "" || targetDir == repoURLOrPath {
		if fi, err := os.Stat(repoURLOrPath); err == nil && fi.IsDir() {
			repo, err := git.PlainOpen(repoURLOrPath)
			if err == nil {
				return repo, nil
			}
		}
		return nil, fmt.Errorf("targetDir cannot be empty when cloning repository %s", repoURLOrPath)
	}

	// If targetDir already exists as a git repo, open it
	if fi, err := os.Stat(targetDir); err == nil && fi.IsDir() {
		if repo, err := git.PlainOpen(targetDir); err == nil {
			return repo, nil
		}
	}

	cloneOpts := &git.CloneOptions{
		URL: repoURLOrPath,
	}

	if cfg.Shallow {
		depth := cfg.Depth
		if depth <= 0 {
			depth = 1
		}
		cloneOpts.Depth = depth
		cloneOpts.SingleBranch = cfg.SingleBranch
		if cfg.RefName != "" {
			if strings.HasPrefix(cfg.RefName, "refs/") {
				cloneOpts.ReferenceName = plumbing.ReferenceName(cfg.RefName)
			} else {
				cloneOpts.ReferenceName = plumbing.NewTagReferenceName(cfg.RefName)
			}
		}
	}

	repo, err := git.PlainClone(targetDir, false, cloneOpts)
	if err != nil && cfg.Shallow {
		// Fallback to full clone if shallow clone failed on specific ref
		repo, err = git.PlainClone(targetDir, false, &git.CloneOptions{
			URL: repoURLOrPath,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("failed to clone repository %s: %w", repoURLOrPath, err)
	}

	return repo, nil
}

// ListSemverTags extracts all semver-compatible tags from a git repository, sorted ascending.
func ListSemverTags(repo *git.Repository) ([]string, error) {
	if repo == nil {
		return nil, fmt.Errorf("git repository cannot be nil")
	}

	tagRefs, _ := repo.Tags()
	var rawTags []string
	if tagRefs != nil {
		_ = tagRefs.ForEach(func(ref *plumbing.Reference) error {
			name := ref.Name().Short()
			rawTags = append(rawTags, name)
			return nil
		})
	}

	valid := semverutil.FilterValid(rawTags)
	return semverutil.Sort(valid), nil
}

// CheckoutRef checks out a specific tag, branch, or commit hash in the worktree.
func CheckoutRef(repo *git.Repository, refName string) error {
	w, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to obtain worktree: %w", err)
	}

	// 1. Try checking out by Tag
	tagRef, err := repo.Tag(refName)
	if err == nil && tagRef != nil {
		// Annotated tag vs lightweight tag
		tagObj, err := repo.TagObject(tagRef.Hash())
		var targetHash plumbing.Hash
		if err == nil && tagObj != nil {
			targetHash = tagObj.Target
		} else {
			targetHash = tagRef.Hash()
		}
		return w.Checkout(&git.CheckoutOptions{
			Hash: targetHash,
		})
	}

	// 2. Try checking out by commit Hash
	if plumbing.IsHash(refName) {
		h := plumbing.NewHash(refName)
		return w.Checkout(&git.CheckoutOptions{
			Hash: h,
		})
	}

	// 3. Try checking out as branch
	branchRefName := plumbing.NewBranchReferenceName(refName)
	return w.Checkout(&git.CheckoutOptions{
		Branch: branchRefName,
	})
}

// ExtractVersionToDirShallow clones or copies a repository using shallow cloning if possible.
func ExtractVersionToDirShallow(repoURLOrPath, refName, destDir string) error {
	if fi, err := os.Stat(repoURLOrPath); err == nil && fi.IsDir() && !isGitRepo(repoURLOrPath) {
		return copyDir(repoURLOrPath, destDir)
	}
	repo, err := CloneOrOpenWithOptions(repoURLOrPath, destDir, CloneConfig{
		Shallow:      true,
		Depth:        1,
		SingleBranch: true,
		RefName:      refName,
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(refName) == "" {
		return nil
	}
	return CheckoutRef(repo, refName)
}

var (
	mkdirTemp = os.MkdirTemp
	relPath   = filepath.Rel
)

func copyDir(src, dst string) error {
	cleanDst := filepath.Clean(dst)
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := relPath(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(cleanDst, rel)
		if !strings.HasPrefix(filepath.Clean(target), cleanDst) {
			return fmt.Errorf("path traversal in copy: %s", target)
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0750)
		}
		// #nosec G122, G304
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return err
		}
		// #nosec G703 -- target is verified within cleanDst
		return os.WriteFile(filepath.Clean(target), data, 0600)
	})
}

func isGitRepo(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

// DirFactory creates a new directory that will hold an extracted module version.
// Callers supply it so that temporary directory creation stays injectable (and testable)
// while the cache owns the lifetime of everything it hands back.
type DirFactory func() (string, error)

// RepositoryCache provides thread-safe caching of cloned repositories and extracted versions.
//
// A remote repository is downloaded at most once per cache: the first request creates a shared
// local clone, and every later request for the same repository re-clones from that local copy
// instead of re-downloading. Extracted versions are additionally memoised per source and ref,
// so repeating the same comparison never touches the network twice.
type RepositoryCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	once sync.Once
	dir  string
	err  error
}

// NewRepositoryCache creates an in-memory repository cache.
func NewRepositoryCache() *RepositoryCache {
	return &RepositoryCache{entries: make(map[string]*cacheEntry)}
}

// GlobalCache is the default global in-memory repository cache.
var GlobalCache = NewRepositoryCache()

// SharedClone returns a local Git working tree for repoURLOrPath that can be reused to extract
// many refs without re-downloading the repository. Local repositories are returned unchanged
// because there is nothing to download; remote repositories are cloned once and cached.
func (c *RepositoryCache) SharedClone(repoURLOrPath string) (string, error) {
	if fi, err := os.Stat(repoURLOrPath); err == nil && fi.IsDir() {
		if !isGitRepo(repoURLOrPath) {
			return "", fmt.Errorf("%s is not a git repository", repoURLOrPath)
		}
		return repoURLOrPath, nil
	}
	return c.resolve("repo\x00"+repoURLOrPath, func() (string, error) {
		dir, err := mkdirTemp("", "tf_git_repo_*")
		if err != nil {
			return "", err
		}
		if _, err := CloneOrOpenWithOptions(repoURLOrPath, dir, CloneConfig{
			Shallow: true,
			Depth:   1,
		}); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
		return dir, nil
	})
}

// GetOrExtract returns a directory holding repoURLOrPath checked out at refName, creating it with
// newDir on a cache miss. The returned directory is owned by the cache and stays valid until Clear
// is called; callers must not delete it themselves.
func (c *RepositoryCache) GetOrExtract(repoURLOrPath, refName string, newDir DirFactory) (string, error) {
	return c.resolve(fmt.Sprintf("ref\x00%s\x00%s", repoURLOrPath, refName), func() (string, error) {
		dir, err := newDir()
		if err != nil {
			return "", err
		}
		if err := c.extractCached(repoURLOrPath, refName, dir); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
		return dir, nil
	})
}

// extractCached prefers the shared local clone over a fresh remote download. When the shared clone
// cannot serve the requested ref (for example an older tag excluded from the shallow clone) it
// falls back to the original remote extraction so behaviour never regresses.
func (c *RepositoryCache) extractCached(repoURLOrPath, refName, destDir string) error {
	mirror, err := c.SharedClone(repoURLOrPath)
	if err == nil && mirror != repoURLOrPath {
		if localErr := ExtractVersionToDirShallow(mirror, refName, destDir); localErr == nil {
			return nil
		}
		// The shared clone cannot serve this ref; retry against the original source from a clean
		// destination. PlainClone recreates the directory itself if it is still missing.
		if fi, statErr := os.Stat(destDir); statErr == nil && fi.IsDir() {
			_ = os.RemoveAll(destDir)
			_ = os.MkdirAll(destDir, 0750)
		}
	}
	return ExtractVersionToDirShallow(repoURLOrPath, refName, destDir)
}

// resolve memoises build per key, serialising concurrent builds of the same key while still
// allowing different keys to be produced in parallel. Failures are never cached.
func (c *RepositoryCache) resolve(key string, build func() (string, error)) (string, error) {
	c.mu.Lock()
	entry, ok := c.entries[key]
	if !ok {
		entry = &cacheEntry{}
		c.entries[key] = entry
	}
	c.mu.Unlock()

	entry.once.Do(func() { entry.dir, entry.err = build() })

	if entry.err != nil {
		c.forget(key, entry)
		return "", entry.err
	}
	if fi, err := os.Stat(entry.dir); err != nil || !fi.IsDir() {
		c.forget(key, entry)
		return "", fmt.Errorf("cached directory %s is no longer available", entry.dir)
	}
	return entry.dir, nil
}

// forget drops a cache entry so that a later request is able to retry it.
func (c *RepositoryCache) forget(key string, entry *cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries[key] == entry {
		delete(c.entries, key)
	}
}

// Clear removes all cached repositories and extracted versions.
func (c *RepositoryCache) Clear() {
	c.mu.Lock()
	entries := c.entries
	c.entries = make(map[string]*cacheEntry)
	c.mu.Unlock()

	for _, entry := range entries {
		_ = os.RemoveAll(entry.dir)
	}
}

// CachedRepositoryDirs returns the directories currently held by GlobalCache. It exists so callers
// and tests can assert that cached clones and extracted versions are released.
func CachedRepositoryDirs() []string {
	c := GlobalCache
	c.mu.Lock()
	defer c.mu.Unlock()

	dirs := make([]string, 0, len(c.entries))
	for _, entry := range c.entries {
		if entry.dir != "" {
			dirs = append(dirs, entry.dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}
