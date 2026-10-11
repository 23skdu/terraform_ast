package policy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodePolicy(t *testing.T) {
	got, err := Decode(strings.NewReader(`{"block_breaking":true,"max_bump":"minor"}`))
	require.NoError(t, err)
	assert.True(t, got.BlockBreaking)
	assert.Equal(t, "MINOR", got.MaxBump)

	_, err = Decode(strings.NewReader(`{"unknown":true}`))
	assert.ErrorContains(t, err, "unknown field")
	_, err = Decode(strings.NewReader(`{"max_bump":"release"}`))
	assert.ErrorContains(t, err, "invalid max_bump")
	_, err = Decode(strings.NewReader(`{} {}`))
	assert.ErrorContains(t, err, "multiple JSON values")
}

func TestEvaluatePolicy(t *testing.T) {
	got := Evaluate(&Policy{
		BlockBreaking:   true,
		BlockWarnings:   true,
		BlockDowngrades: true,
		MaxBump:         "MINOR",
	}, Input{
		BreakingChanges: 2,
		Warnings:        1,
		IsDowngrade:     true,
		BumpType:        "MAJOR",
	})
	require.Len(t, got, 4)
	assert.Equal(t, []string{"block_breaking", "block_warnings", "block_downgrades", "max_bump"}, []string{
		got[0].Rule, got[1].Rule, got[2].Rule, got[3].Rule,
	})
	assert.Empty(t, Evaluate(nil, Input{BreakingChanges: 1}))
	assert.Empty(t, Evaluate(&Policy{MaxBump: "MAJOR"}, Input{BumpType: "MAJOR"}))
	assert.Empty(t, Evaluate(&Policy{MaxBump: "PATCH"}, Input{BumpType: "IDENTICAL"}))

	unknownBump := Evaluate(&Policy{MaxBump: "MAJOR"}, Input{})
	require.Len(t, unknownBump, 1)
	assert.Equal(t, "max_bump", unknownBump[0].Rule)
	assert.Contains(t, unknownBump[0].Message, "version information unavailable")
}
