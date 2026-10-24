package gitops

import (
	"fmt"
	"net/url"
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

// IsGitURL reports whether source must be resolved through git instead of being read from disk or
// fetched from the registry: the URL schemes go-git can clone (http, https, file, git, ssh), the
// SSH scp-like form Terraform accepts, and the github.com/ and bitbucket.org/ shorthands that
// normalizeCloneURL expands into clone URLs.
func IsGitURL(source string) bool {
	return strings.HasPrefix(source, "http://") ||
		strings.HasPrefix(source, "https://") ||
		strings.HasPrefix(source, "file://") ||
		strings.HasPrefix(source, "git://") ||
		strings.HasPrefix(source, "ssh://") ||
		strings.HasPrefix(source, "git@") ||
		strings.HasPrefix(source, "git::") ||
		strings.HasPrefix(source, "github.com/") ||
		strings.HasPrefix(source, "bitbucket.org/")
}

// GitSource is a parsed Terraform VCS module source.
type GitSource struct {
	// URL is a cloneable URL: the "git::" prefix stripped and the github.com/bitbucket.org
	// shorthands expanded. Subdirectory and reference syntax is not part of it.
	URL string
	// SubDir is the repository subdirectory holding the module, empty when the module sits at
	// the repository root.
	SubDir string
	// Ref is the reference the source pins with "?ref=", empty when the source pins none.
	Ref string
}

// ParseGitSource splits a Terraform VCS module source into the URL to clone, the repository
// subdirectory and the pinned reference, and makes the URL cloneable: the "git::" prefix is
// stripped and the github.com/ and bitbucket.org/ shorthands Terraform treats as
// "git::https://<host>/<path>.git" are expanded.
//
// Both separator orders Terraform accepts are handled ("repo.git//sub?ref=v1" and
// "repo.git?ref=v1//sub"), and the syntax is separated before the shorthand is expanded, so a
// pinned reference is never mistaken for part of the path. Only URL-shaped sources are split: in a
// local path "//" is an ordinary path character and must never be read as subdirectory syntax.
func ParseGitSource(source string) GitSource {
	normalized := strings.TrimPrefix(source, "git::")

	isURLShaped := strings.HasPrefix(normalized, "git@") ||
		strings.HasPrefix(normalized, "github.com/") ||
		strings.HasPrefix(normalized, "bitbucket.org/") ||
		strings.Contains(normalized, "://")
	if !isURLShaped {
		return GitSource{URL: normalized}
	}

	searchFrom := 0
	if i := strings.Index(normalized, "://"); i >= 0 {
		searchFrom = i + 3 // skip the scheme's own "//"
	}
	head, tail := normalized[:searchFrom], normalized[searchFrom:]

	subDirAt, queryAt := strings.Index(tail, "//"), strings.Index(tail, "?")
	var urlPart, subDir, query string
	switch {
	case subDirAt >= 0 && (queryAt < 0 || subDirAt < queryAt):
		urlPart = tail[:subDirAt]
		after := tail[subDirAt+2:]
		subDir = after
		if i := strings.Index(after, "?"); i >= 0 {
			subDir, query = after[:i], after[i+1:]
		}
	case queryAt >= 0:
		urlPart = tail[:queryAt]
		after := tail[queryAt+1:]
		query = after
		if i := strings.Index(after, "//"); i >= 0 {
			query, subDir = after[:i], after[i+2:]
		}
	default:
		urlPart = tail
	}
	return GitSource{
		URL:    expandGitShorthand(head + urlPart),
		SubDir: subDir,
		Ref:    refFromQuery(query),
	}
}

// expandGitShorthand rewrites the github.com/ and bitbucket.org/ source shorthands into the
// "https://<host>/<path>.git" URLs Terraform derives from them. It is only ever handed the URL
// portion of a source, with subdirectory and reference syntax already separated.
func expandGitShorthand(urlPart string) string {
	if !strings.HasPrefix(urlPart, "github.com/") && !strings.HasPrefix(urlPart, "bitbucket.org/") {
		return urlPart
	}
	if !strings.HasSuffix(urlPart, ".git") {
		urlPart += ".git"
	}
	return "https://" + urlPart
}

// refFromQuery extracts the "ref" parameter from a source's query string, decoding percent
// escapes so a branch name containing "/" round-trips.
func refFromQuery(query string) string {
	for _, param := range strings.Split(query, "&") {
		key, value, ok := strings.Cut(param, "=")
		if !ok || key != "ref" {
			continue
		}
		if decoded, err := url.QueryUnescape(value); err == nil {
			return decoded
		}
		return value
	}
	return ""
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
		URL: ParseGitSource(repoURLOrPath).URL,
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
			URL: ParseGitSource(repoURLOrPath).URL,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("failed to clone repository %s: %w", repoURLOrPath, err)
	}

	return repo, nil
}

// ParseScopedTag extracts a valid semver string and prefix from a tag name,
// handling monorepo tag formats like "modules/vpc/v1.2.0" or "vpc-v1.2.0".
func ParseScopedTag(tag string) (version string, prefix string, ok bool) {
	trimmed := strings.TrimSpace(tag)
	if semverutil.IsValid(trimmed) {
		return trimmed, "", true
	}
	// Try splitting by '/' (e.g. "modules/vpc/v1.2.0")
	if idx := strings.LastIndex(trimmed, "/"); idx != -1 {
		candidate := trimmed[idx+1:]
		if semverutil.IsValid(candidate) {
			return candidate, trimmed[:idx], true
		}
	}
	// Try splitting by '-' (e.g. "vpc-v1.2.0")
	if idx := strings.LastIndex(trimmed, "-"); idx != -1 {
		candidate := trimmed[idx+1:]
		if semverutil.IsValid(candidate) {
			return candidate, trimmed[:idx], true
		}
	}
	return "", "", false
}

// ListScopedSemverTags extracts semver tags from repo, optionally filtered by scope prefix.
// If scope is non-empty, tags whose prefix matches scope (e.g. "modules/vpc" or "vpc")
// are returned. If no tags match the scope, all valid semver tags in the repository are returned.
func ListScopedSemverTags(repo *git.Repository, scope string) ([]string, error) {
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

	cleanScope := strings.Trim(strings.TrimSpace(scope), "/")
	var scopedVersions []string
	var allVersions []string

	for _, tag := range rawTags {
		ver, prefix, ok := ParseScopedTag(tag)
		if !ok {
			continue
		}
		allVersions = append(allVersions, ver)
		if cleanScope != "" {
			trimmedPrefix := strings.Trim(prefix, "/")
			if trimmedPrefix == cleanScope || strings.HasSuffix(trimmedPrefix, "/"+cleanScope) || trimmedPrefix == strings.ReplaceAll(cleanScope, "/", "-") {
				scopedVersions = append(scopedVersions, ver)
			}
		}
	}

	if cleanScope != "" && len(scopedVersions) > 0 {
		return semverutil.Sort(semverutil.FilterValid(scopedVersions)), nil
	}
	return semverutil.Sort(semverutil.FilterValid(allVersions)), nil
}

// ListSemverTags extracts all semver-compatible tags from a git repository, sorted ascending.
// It supports both standard semver tags and monorepo component-scoped tags.
func ListSemverTags(repo *git.Repository) ([]string, error) {
	return ListScopedSemverTags(repo, "")
}

func checkoutTag(repo *git.Repository, w *git.Worktree, tagRef *plumbing.Reference) error {
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

// CheckoutRef checks out a specific tag, branch, or commit hash in the worktree.
// It also checks out monorepo scoped tags matching the requested semver.
func CheckoutRef(repo *git.Repository, refName string) error {
	w, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to obtain worktree: %w", err)
	}

	// 1. Try checking out by direct tag name
	tagRef, err := repo.Tag(refName)
	if err == nil && tagRef != nil {
		return checkoutTag(repo, w, tagRef)
	}

	// 1b. Check if any tag matches refName as a scoped semver tag
	if tagRefs, err := repo.Tags(); err == nil && tagRefs != nil {
		var matchedRef *plumbing.Reference
		_ = tagRefs.ForEach(func(ref *plumbing.Reference) error {
			short := ref.Name().Short()
			ver, _, ok := ParseScopedTag(short)
			if ok && semverutil.Compare(ver, refName) == 0 {
				matchedRef = ref
			}
			return nil
		})
		if matchedRef != nil {
			return checkoutTag(repo, w, matchedRef)
		}
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
// Subdirectory and reference syntax is stripped from the source first: the ref is selected by the
// caller (see GetOrExtract) and the subdirectory belongs to the returned checkout, not the clone.
func ExtractVersionToDirShallow(repoURLOrPath, refName, destDir string) error {
	repoURLOrPath = ParseGitSource(repoURLOrPath).URL
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
// because there is nothing to download; remote repositories are cloned once and cached. The cache
// is keyed on the repository alone: subdirectory and reference syntax belongs to a module source,
// not to the repository behind it, so every source addressing the same repository shares one clone.
func (c *RepositoryCache) SharedClone(repoURLOrPath string) (string, error) {
	if fi, err := os.Stat(repoURLOrPath); err == nil && fi.IsDir() {
		if !isGitRepo(repoURLOrPath) {
			return "", fmt.Errorf("%s is not a git repository", repoURLOrPath)
		}
		return repoURLOrPath, nil
	}
	cloneURL := ParseGitSource(repoURLOrPath).URL
	return c.resolve("repo\x00"+cloneURL, func() (string, error) {
		dir, err := mkdirTemp("", "tf_git_repo_*")
		if err != nil {
			return "", err
		}
		if _, err := CloneOrOpenWithOptions(cloneURL, dir, CloneConfig{
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
// newDir on a cache miss. A reference the source pins itself ("?ref=") is used when refName is
// empty, and a source naming a repository subdirectory ("//subdir") yields that subdirectory, so
// a module inside a repository is inspected at the revision and location its source names. An
// explicit refName wins over the source's pin. The returned directory is owned by the cache and
// stays valid until Clear is called; callers must not delete it themselves.
func (c *RepositoryCache) GetOrExtract(repoURLOrPath, refName string, newDir DirFactory) (string, error) {
	parts := ParseGitSource(repoURLOrPath)
	effectiveRef := refName
	if effectiveRef == "" {
		effectiveRef = parts.Ref
	}
	root, err := c.resolve(fmt.Sprintf("ref\x00%s\x00%s", repoURLOrPath, effectiveRef), func() (string, error) {
		dir, err := newDir()
		if err != nil {
			return "", err
		}
		if err := c.extractCached(parts.URL, effectiveRef, dir); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
		return dir, nil
	})
	if err != nil {
		return "", err
	}
	if parts.SubDir == "" {
		return root, nil
	}
	// The checkout stays cached as a whole; only the module's directory inside it is handed out.
	sub, err := resolveSubDir(root, parts.SubDir)
	if err != nil {
		return "", fmt.Errorf("module source %s: %w", repoURLOrPath, err)
	}
	return sub, nil
}

// resolveSubDir returns the directory of subDir inside the checkout rooted at dir, rejecting a
// subdirectory that escapes the checkout or does not exist: the module must be read from where
// the source says it is, or not at all.
func resolveSubDir(dir, subDir string) (string, error) {
	cleanRoot := filepath.Clean(dir)
	cleanSub := filepath.Clean(subDir)
	if cleanSub == "." || cleanSub == "" {
		return cleanRoot, nil
	}
	target := filepath.Join(cleanRoot, cleanSub)
	if !strings.HasPrefix(target, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("subdirectory %q escapes the repository checkout", subDir)
	}
	fi, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("subdirectory %q not found in the repository", subDir)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("subdirectory %q in the repository is not a directory", subDir)
	}
	return target, nil
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
