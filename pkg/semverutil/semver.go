package semverutil

import (
	"fmt"
	"sort"
	"strconv"
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
	// BumpUnknown means at least one version was not supplied, so no transition could be
	// classified. It is deliberately distinct from BumpInvalid: an absent version is missing
	// information, whereas an invalid one is information the caller got wrong.
	BumpUnknown BumpType = "UNKNOWN"
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
	var nonSemver, validSemver []string
	for _, v := range versions {
		if IsValid(v) {
			validSemver = append(validSemver, v)
		} else {
			nonSemver = append(nonSemver, v)
		}
	}
	sort.Strings(nonSemver)
	sort.Slice(validSemver, func(i, j int) bool {
		cmp := semver.Compare(Normalize(validSemver[i]), Normalize(validSemver[j]))
		if cmp != 0 {
			return cmp < 0
		}
		return validSemver[i] < validSemver[j]
	})
	result := make([]string, 0, len(versions))
	result = append(result, nonSemver...)
	result = append(result, validSemver...)
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

// IsPrerelease returns true if v is a valid semver with a prerelease component.
func IsPrerelease(v string) bool {
	norm := Normalize(v)
	return IsValid(norm) && semver.Prerelease(norm) != ""
}

// FilterPrereleases filters out any prerelease versions.
func FilterPrereleases(versions []string) []string {
	var stable []string
	for _, v := range versions {
		if !IsPrerelease(v) {
			stable = append(stable, v)
		}
	}
	return stable
}

// DetermineBumpType analyzes the transition from oldVer to newVer.
func DetermineBumpType(oldVer, newVer string) BumpType {
	nOld, nNew := Normalize(oldVer), Normalize(newVer)
	// A missing version is missing information, not a malformed one. Reporting BumpInvalid here
	// made every version-less comparison (the documented "compare two local directories" flow)
	// look like an unrecognised transition to policy gates.
	if nOld == "" || nNew == "" {
		return BumpUnknown
	}
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

// ConstraintRange describes the version bounds and exclusions of a constraint string.
type ConstraintRange struct {
	Raw        string   `json:"raw"`
	MinVersion string   `json:"min_version,omitempty"`
	MaxVersion string   `json:"max_version,omitempty"`
	Excluded   []string `json:"excluded,omitempty"`
}

// ParseConstraintRange computes the minimum, maximum, and excluded versions from a constraint.
func ParseConstraintRange(constraint string) ConstraintRange {
	cr := ConstraintRange{Raw: strings.TrimSpace(constraint)}
	parts := strings.Split(constraint, ",")
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "!=") {
			ver := strings.TrimSpace(strings.TrimPrefix(trimmed, "!="))
			cr.Excluded = append(cr.Excluded, ver)
		} else if strings.HasPrefix(trimmed, ">=") {
			cr.MinVersion = strings.TrimSpace(strings.TrimPrefix(trimmed, ">="))
		} else if strings.HasPrefix(trimmed, ">") {
			cr.MinVersion = strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
		} else if strings.HasPrefix(trimmed, "<=") {
			cr.MaxVersion = strings.TrimSpace(strings.TrimPrefix(trimmed, "<="))
		} else if strings.HasPrefix(trimmed, "<") {
			cr.MaxVersion = strings.TrimSpace(strings.TrimPrefix(trimmed, "<"))
		} else if strings.HasPrefix(trimmed, "~>") {
			spec := strings.TrimSpace(strings.TrimPrefix(trimmed, "~>"))
			cr.MinVersion = spec
			sub := strings.Split(spec, ".")
			if len(sub) == 2 {
				if maj, err := strconv.Atoi(sub[0]); err == nil {
					cr.MaxVersion = fmt.Sprintf("%d.0.0", maj+1)
				}
			} else if len(sub) >= 3 {
				if maj, err1 := strconv.Atoi(sub[0]); err1 == nil {
					if min, err2 := strconv.Atoi(sub[1]); err2 == nil {
						cr.MaxVersion = fmt.Sprintf("%d.%d.0", maj, min+1)
					}
				}
			}
		} else if strings.HasPrefix(trimmed, "=") {
			ver := strings.TrimSpace(strings.TrimPrefix(trimmed, "="))
			cr.MinVersion = ver
			cr.MaxVersion = ver
		} else if IsValid(trimmed) {
			cr.MinVersion = trimmed
			cr.MaxVersion = trimmed
		}
	}
	return cr
}
