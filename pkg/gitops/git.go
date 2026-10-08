package gitops

import (
	"fmt"
	"os"
	"path/filepath"
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

// FindLatestVersion returns the highest semantic version tag found in the repository.
func FindLatestVersion(repo *git.Repository) (string, error) {
	tags, err := ListSemverTags(repo)
	if err != nil {
		return "", err
	}
	if len(tags) == 0 {
		return "", fmt.Errorf("no semantic version tags found in repository")
	}
	return tags[len(tags)-1], nil
}

// FindNextVersion returns the next higher semantic version tag after currentVersion.
func FindNextVersion(repo *git.Repository, currentVersion string) (string, error) {
	tags, err := ListSemverTags(repo)
	if err != nil {
		return "", err
	}
	normCurrent := semverutil.Normalize(currentVersion)
	for _, tag := range tags {
		if semverutil.Compare(tag, normCurrent) > 0 {
			return tag, nil
		}
	}
	return "", fmt.Errorf("no version found higher than %s", currentVersion)
}

// ExtractVersionToDir clones or copies a repository and checks out the specified ref into destDir.
func ExtractVersionToDir(repoURLOrPath, refName, destDir string) error {
	if fi, err := os.Stat(repoURLOrPath); err == nil && fi.IsDir() && !isGitRepo(repoURLOrPath) {
		return copyDir(repoURLOrPath, destDir)
	}
	repo, err := CloneOrOpen(repoURLOrPath, destDir)
	if err != nil {
		return err
	}
	if strings.TrimSpace(refName) == "" {
		return nil
	}
	return CheckoutRef(repo, refName)
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

func copyDir(src, dst string) error {
	cleanDst := filepath.Clean(dst)
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
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

// RepositoryCache provides thread-safe caching of extracted repositories.
type RepositoryCache struct {
	mu    sync.RWMutex
	cache map[string]string // key: "repoURL@ref" -> cachedDir
}

// NewRepositoryCache creates an in-memory repository cache.
func NewRepositoryCache() *RepositoryCache {
	return &RepositoryCache{
		cache: make(map[string]string),
	}
}

// GlobalCache is the default global in-memory repository cache.
var GlobalCache = NewRepositoryCache()

// GetOrExtract retrieves the version from cache or extracts it into a cached directory.
func (c *RepositoryCache) GetOrExtract(repoURLOrPath, refName string) (string, error) {
	key := fmt.Sprintf("%s@%s", repoURLOrPath, refName)

	c.mu.RLock()
	cachedDir, found := c.cache[key]
	c.mu.RUnlock()

	if found {
		if fi, err := os.Stat(cachedDir); err == nil && fi.IsDir() {
			return cachedDir, nil
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check under write lock
	if cachedDir, found := c.cache[key]; found {
		if fi, err := os.Stat(cachedDir); err == nil && fi.IsDir() {
			return cachedDir, nil
		}
	}

	tmpDir, err := os.MkdirTemp("", "tf_git_cache_*")
	if err != nil {
		return "", err
	}

	if err := ExtractVersionToDirShallow(repoURLOrPath, refName, tmpDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", err
	}

	c.cache[key] = tmpDir
	return tmpDir, nil
}

// Clear removes all cached repositories.
func (c *RepositoryCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, dir := range c.cache {
		_ = os.RemoveAll(dir)
	}
	c.cache = make(map[string]string)
}

