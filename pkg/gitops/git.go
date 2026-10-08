package gitops

import (
	"fmt"
	"os"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/semverutil"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// CloneOrOpen opens a local git repository in-place if targetDir is empty or matches path,
// or clones the repository into targetDir.
func CloneOrOpen(repoURLOrPath, targetDir string) (*git.Repository, error) {
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

	repo, err := git.PlainClone(targetDir, false, &git.CloneOptions{
		URL: repoURLOrPath,
	})
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
	repo, err := CloneOrOpen(repoURLOrPath, destDir)
	if err != nil {
		return err
	}
	if strings.TrimSpace(refName) == "" {
		return nil
	}
	return CheckoutRef(repo, refName)
}
