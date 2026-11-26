package lock

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestAcquireOpenError(t *testing.T) {
	dir := t.TempDir()
	// A directory at the lock path cannot be opened as a file.
	path := filepath.Join(dir, "run.lock")
	require.NoError(t, os.Mkdir(path, 0o750))
	_, err := Acquire(path)
	assert.ErrorContains(t, err, "open lock")
}

func TestReadInfo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.lock")
	l, err := Acquire(path)
	require.NoError(t, err)

	info, err := ReadInfo(path)
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), info.PID)
	assert.NotEmpty(t, info.Host)
	assert.False(t, info.AcquiredAt.IsZero())
	require.NoError(t, l.Release())

	// The file stays in place after release so contenders always lock the same inode;
	// its metadata is informational and may name the previous owner.
	info, err = ReadInfo(path)
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), info.PID)

	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))
	_, err = ReadInfo(path)
	assert.ErrorContains(t, err, "decode lock")

	_, err = ReadInfo(filepath.Join(dir, "missing.lock"))
	assert.ErrorContains(t, err, "read lock")
}

func TestAcquireWithTimeoutWaitsForHolder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.lock")
	held, err := Acquire(path)
	require.NoError(t, err)

	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = held.Release()
	}()
	waiting, err := AcquireWithTimeout(path, time.Second)
	require.NoError(t, err)
	require.NotNil(t, waiting)
	require.NoError(t, waiting.Release())
}

func TestAcquireWithTimeoutExpires(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.lock")
	held, err := Acquire(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, held.Release()) }()

	_, err = AcquireWithTimeout(path, 20*time.Millisecond)
	assert.ErrorContains(t, err, "acquire lock")

	// The original holder's lock is still valid after a failed contender timed out.
	info, err := ReadInfo(path)
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), info.PID)
}

func TestReleaseKeepsFileForStableFlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.lock")
	l, err := Acquire(path)
	require.NoError(t, err)
	require.NoError(t, l.Release())
	_, err = os.Stat(path)
	require.NoError(t, err)

	// Contenders keep locking the same inode across handoff.
	first, err := Acquire(path)
	require.NoError(t, err)
	_, err = Acquire(path)
	assert.Error(t, err)
	require.NoError(t, first.Release())
	second, err := Acquire(path)
	require.NoError(t, err)
	require.NoError(t, second.Release())
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
