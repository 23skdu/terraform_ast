package semverutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalize(t *testing.T) {
	assert.Equal(t, "", Normalize(""))
	assert.Equal(t, "", Normalize("   "))
	assert.Equal(t, "v1.2.3", Normalize("1.2.3"))
	assert.Equal(t, "v1.2.3", Normalize("v1.2.3"))
	assert.Equal(t, "vinvalid", Normalize("invalid"))
}

func TestIsValid(t *testing.T) {
	assert.True(t, IsValid("1.2.3"))
	assert.True(t, IsValid("v1.2.3"))
	assert.True(t, IsValid("v2.0.0-beta.1"))
	assert.False(t, IsValid(""))
	assert.False(t, IsValid("invalid"))
	assert.False(t, IsValid("v1.invalid"))
}

func TestCompare(t *testing.T) {
	assert.Equal(t, -1, Compare("1.0.0", "1.1.0"))
	assert.Equal(t, 0, Compare("v1.2.3", "1.2.3"))
	assert.Equal(t, 1, Compare("2.0.0", "1.9.9"))
}

func TestSort(t *testing.T) {
	input := []string{"v2.0.0", "invalid-2", "1.0.0", "v1.5.0", "invalid-1"}
	sorted := Sort(input)
	expected := []string{"invalid-1", "invalid-2", "1.0.0", "v1.5.0", "v2.0.0"}
	assert.Equal(t, expected, sorted)
}

func TestFilterValid(t *testing.T) {
	input := []string{"v1.0.0", "not-a-ver", "2.1.0", "bad"}
	filtered := FilterValid(input)
	assert.Equal(t, []string{"v1.0.0", "2.1.0"}, filtered)
}

func TestDetermineBumpType(t *testing.T) {
	tests := []struct {
		oldVer   string
		newVer   string
		expected BumpType
	}{
		{"invalid", "1.0.0", BumpInvalid},
		{"1.0.0", "invalid", BumpInvalid},
		{"", "", BumpUnknown},
		{"", "1.0.0", BumpUnknown},
		{"1.0.0", "", BumpUnknown},
		{"  ", "1.0.0", BumpUnknown},
		{"1.0.0", "1.0.0", BumpIdentical},
		{"2.0.0", "1.9.0", BumpDowngrade},
		{"1.0.0", "2.0.0", BumpMajor},
		{"1.2.0", "1.3.0", BumpMinor},
		{"1.2.3", "1.2.4", BumpPatch},
		{"1.0", "1.0", BumpIdentical},
	}

	for _, tt := range tests {
		actual := DetermineBumpType(tt.oldVer, tt.newVer)
		assert.Equal(t, tt.expected, actual, "BumpType(%s, %s)", tt.oldVer, tt.newVer)
	}
}
