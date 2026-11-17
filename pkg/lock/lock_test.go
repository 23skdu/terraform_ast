package lock

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.lock")

	l, err := Acquire(path)
	require.NoError(t, err)
	require.NotNil(t, l)

	// Second acquire fails while held.
	_, err = Acquire(path)
	assert.Error(t, err)

	require.NoError(t, l.Release())
	// Re-acquire succeeds after release.
	l2, err := Acquire(path)
	require.NoError(t, err)
	require.NoError(t, l2.Release())

	// Release nil is safe.
	var nilLock *Lock
	assert.NoError(t, nilLock.Release())
}

func TestAcquireValidation(t *testing.T) {
	_, err := Acquire("")
	assert.ErrorContains(t, err, "lock path is required")

	// Directory creation error.
	_, err = Acquire(filepath.Join("/non/existent/dir", "lock"))
	assert.Error(t, err)
}

func TestReleaseRemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.lock")
	l, err := Acquire(path)
	require.NoError(t, err)
	require.NoError(t, l.Release())
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

func TestReleaseAlreadyReleased(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.lock")
	l, err := Acquire(path)
	require.NoError(t, err)
	require.NoError(t, l.Release())
	// second release is safe
	assert.NoError(t, l.Release())
}
