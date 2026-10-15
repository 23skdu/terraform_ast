package semverutil

import (
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

// BumpType defines the classification of a version change according to Semantic Versioning.
type BumpType string

const (
	BumpMajor     BumpType = "MAJOR"     // Breaking change
	BumpMinor     BumpType = "MINOR"     // New backward-compatible feature
	BumpPatch     BumpType = "PATCH"     // Backward-compatible bug fix
	BumpDowngrade BumpType = "DOWNGRADE" // Version decreased
	BumpIdentical BumpType = "IDENTICAL" // Versions are the same
	BumpInvalid   BumpType = "INVALID"   // Version string does not conform to semver
)

// Normalize ensures a version string starts with 'v' and is trimmed.
// If the version string is empty, it returns an empty string.
func Normalize(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "v") {
		trimmed = "v" + trimmed
	}
	// Canonicalize semver if valid
	if semver.IsValid(trimmed) {
		return semver.Canonical(trimmed)
	}
	return trimmed
}

// IsValid checks whether a version string is a valid semantic version.
func IsValid(v string) bool {
	norm := Normalize(v)
	return norm != "" && semver.IsValid(norm)
}

// Compare compares two version strings (with or without 'v' prefix).
// Returns -1 if v1 < v2, 0 if v1 == v2, and 1 if v1 > v2.
func Compare(v1, v2 string) int {
	return semver.Compare(Normalize(v1), Normalize(v2))
}

// Sort sorts a slice of version strings in ascending semver order.
// Non-semver items are sorted lexicographically at the front.
func Sort(versions []string) []string {
	result := make([]string, len(versions))
	copy(result, versions)
	sort.Slice(result, func(i, j int) bool {
		vi, vj := Normalize(result[i]), Normalize(result[j])
		validI, validJ := semver.IsValid(vi), semver.IsValid(vj)
		if validI && validJ {
			return semver.Compare(vi, vj) < 0
		}
		if validI {
			return false
		}
		if validJ {
			return true
		}
		return result[i] < result[j]
	})
	return result
}

// FilterValid returns only valid semver versions from the given slice.
func FilterValid(versions []string) []string {
	var valid []string
	for _, v := range versions {
		if IsValid(v) {
			valid = append(valid, v)
		}
	}
	return valid
}

// DetermineBumpType analyzes the transition from oldVer to newVer.
func DetermineBumpType(oldVer, newVer string) BumpType {
	nOld, nNew := Normalize(oldVer), Normalize(newVer)
	if !semver.IsValid(nOld) || !semver.IsValid(nNew) {
		return BumpInvalid
	}

	cmp := semver.Compare(nOld, nNew)
	if cmp == 0 {
		return BumpIdentical
	}
	if cmp > 0 {
		return BumpDowngrade
	}

	majOld := semver.Major(nOld)
	majNew := semver.Major(nNew)
	if majOld != majNew {
		return BumpMajor
	}

	// Compare prefixes to check minor vs patch
	// e.g., v1.2.3 -> v1.2 vs v1.3
	oldParts := strings.Split(strings.TrimPrefix(nOld, "v"), ".")
	newParts := strings.Split(strings.TrimPrefix(nNew, "v"), ".")
	if len(oldParts) > 1 && len(newParts) > 1 && oldParts[1] != newParts[1] {
		return BumpMinor
	}

	return BumpPatch
}
