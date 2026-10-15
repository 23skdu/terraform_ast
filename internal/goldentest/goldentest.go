// Package goldentest provides golden-file comparison helpers for the
// repository's end-to-end tests. It is kept out of the production packages so
// that shipping binaries never link the testing or assertion libraries.
package goldentest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AssertGoldenMatch verifies that actual output matches the canonical golden file at goldenPath.
// If updateGolden is true or the UPDATE_GOLDEN environment variable is set to true, it writes or
// updates the golden file instead. Absolute workspace paths are normalised to <WORKSPACE> so
// goldens stay portable across machines.
func AssertGoldenMatch(t *testing.T, goldenPath string, actual []byte, updateGolden bool) {
	t.Helper()
	if os.Getenv("UPDATE_GOLDEN") == "true" {
		updateGolden = true
	}

	root := getRepoRoot()
	actualStr := string(actual)
	if root != "" {
		// Replace both native OS path and slash-normalized path
		actualStr = strings.ReplaceAll(actualStr, root, "<WORKSPACE>")
		actualStr = strings.ReplaceAll(actualStr, filepath.ToSlash(root), "<WORKSPACE>")
	}
	actualSanitized := []byte(actualStr)

	if updateGolden {
		err := os.MkdirAll(filepath.Dir(goldenPath), 0750)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Clean(goldenPath), actualSanitized, 0600)
		require.NoError(t, err)
		return
	}

	// #nosec G304 -- test golden verification path
	expected, err := os.ReadFile(filepath.Clean(goldenPath))
	require.NoError(t, err, "golden file %s missing. Run with UPDATE_GOLDEN=true to generate", goldenPath)
	assert.Equal(t, string(expected), string(actualSanitized), "golden file mismatch for %s", goldenPath)
}

func getRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
