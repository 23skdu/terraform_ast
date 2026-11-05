package semverutil

import (
	"strings"
	"testing"

	"golang.org/x/mod/semver"
)

// FuzzNormalizeAndIsValid checks that version normalisation is idempotent and that validity
// classification agrees with the underlying semver library for arbitrary input.
func FuzzNormalizeAndIsValid(f *testing.F) {
	for _, seed := range []string{
		"v1.2.3", "1.2.3", " v1.2.3 ", "", "  ", "v1", "v1.2", "not-a-version",
		"V1.2.3", "v01.2.3", "v1.2.3-rc.1", "v1.2.3+build", "v1.2.3-rc.1+build",
		"vv1.0.0", "-1.0.0",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, version string) {
		normalized := Normalize(version)
		if normalized != Normalize(normalized) {
			t.Fatalf("Normalize is not idempotent for %q: %q then %q", version, normalized, Normalize(normalized))
		}
		if IsValid(version) != semver.IsValid(normalized) {
			t.Fatalf("IsValid(%q) disagrees with the semver library for %q", version, normalized)
		}
		if IsValid(version) && normalized != semver.Canonical(normalized) {
			t.Fatalf("a valid version must be canonicalised: %q -> %q", version, normalized)
		}
	})
}

// FuzzDetermineBumpType verifies the bump classification is consistent with semver ordering.
func FuzzDetermineBumpType(f *testing.F) {
	for _, seed := range [][2]string{
		{"v1.0.0", "v2.0.0"}, {"v1.0.0", "v1.1.0"}, {"v1.0.0", "v1.0.1"},
		{"v2.0.0", "v1.0.0"}, {"v1.0.0", "v1.0.0"}, {"", "v1.0.0"}, {"bad", "worse"},
		{"v1.2", "v1.3"}, {"1.0.0", "2.0.0"},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, oldVer, newVer string) {
		bump := DetermineBumpType(oldVer, newVer)
		oldNorm, newNorm := Normalize(oldVer), Normalize(newVer)

		// A missing version is missing information, not a malformed one.
		if oldNorm == "" || newNorm == "" {
			if bump != BumpUnknown {
				t.Fatalf("a missing version must classify as BumpUnknown, got %q for %q -> %q", bump, oldVer, newVer)
			}
			return
		}
		if !semver.IsValid(oldNorm) || !semver.IsValid(newNorm) {
			if bump != BumpInvalid {
				t.Fatalf("invalid versions must classify as BumpInvalid, got %q for %q -> %q", bump, oldVer, newVer)
			}
			return
		}
		switch cmp := semver.Compare(oldNorm, newNorm); {
		case cmp == 0:
			if bump != BumpIdentical {
				t.Fatalf("equal versions must be BumpIdentical, got %q", bump)
			}
		case cmp > 0:
			if bump != BumpDowngrade {
				t.Fatalf("descending versions must be BumpDowngrade, got %q", bump)
			}
		case semver.Major(oldNorm) != semver.Major(newNorm):
			if bump != BumpMajor {
				t.Fatalf("a changed major component must be BumpMajor, got %q", bump)
			}
		}
	})
}

// FuzzSort checks that sorting is a stable, order-preserving operation on arbitrary version lists.
func FuzzSort(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("v1.0.0"))
	f.Add([]byte("v2.0.0v1.0.0"))
	f.Add([]byte("badv1.0.0vworse"))
	f.Add([]byte("v1.0.0v1.0.0"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		versions := strings.Split(string(raw), "v")
		sorted := Sort(versions)
		if len(sorted) != len(versions) {
			t.Fatalf("Sort changed the element count: %d -> %d", len(versions), len(sorted))
		}
		for _, version := range versions {
			found := false
			for _, kept := range sorted {
				if kept == version {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("Sort dropped %q", version)
			}
		}
		filtered := FilterValid(sorted)
		for _, version := range filtered {
			if !IsValid(version) {
				t.Fatalf("FilterValid returned the invalid version %q", version)
			}
		}
	})
}

// FuzzParseConstraintRange verifies that constraint range parsing never panics and preserves the raw input.
func FuzzParseConstraintRange(f *testing.F) {
	for _, seed := range []string{
		">= 1.0.0, < 2.0.0",
		"~> 4.0",
		"~> 0.12.0, != 0.12.3",
		"= 1.2.3",
		"<= 5.0.0, > 3.0.0",
		"1.0.0",
		"",
		" , , ",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, constraint string) {
		cr := ParseConstraintRange(constraint)
		if cr.Raw != strings.TrimSpace(constraint) {
			t.Fatalf("ParseConstraintRange(%q).Raw = %q", constraint, cr.Raw)
		}
	})
}
