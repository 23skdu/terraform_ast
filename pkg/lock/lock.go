// Package lock provides advisory OS file locks for coordinating concurrent analysis runs.
// The kernel releases an advisory lock automatically when a process exits, so a crash cannot
// leave a stale lock that blocks every later run.
package lock

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Info is the owner metadata written into the lock file after acquisition.
type Info struct {
	PID        int       `json:"pid"`
	Host       string    `json:"host,omitempty"`
	AcquiredAt time.Time `json:"acquired_at"`
}

// Lock represents an acquired advisory lock.
type Lock struct {
	path *os.File
}

// Acquire attempts to acquire the advisory lock without waiting.
func Acquire(path string) (*Lock, error) {
	return AcquireWithTimeout(path, 0)
}

// AcquireWithTimeout attempts to acquire the advisory lock, retrying until timeout elapses.
// A zero or negative timeout means a single non-blocking attempt.
func AcquireWithTimeout(path string, timeout time.Duration) (*Lock, error) {
	if path == "" {
		return nil, fmt.Errorf("lock path is required")
	}
	cleanPath := filepath.Clean(path)
	dir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		// #nosec G304 -- lock path is operator supplied by design.
		f, err := os.OpenFile(cleanPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open lock %s: %w", cleanPath, err)
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			host, _ := os.Hostname()
			info := Info{PID: os.Getpid(), Host: host, AcquiredAt: time.Now()}
			data, _ := json.Marshal(info)
			_ = f.Truncate(0)
			_, _ = f.Seek(0, 0)
			_, _ = f.Write(data)
			return &Lock{path: f}, nil
		}
		_ = f.Close()
		if timeout <= 0 || !time.Now().Before(deadline) {
			return nil, fmt.Errorf("acquire lock %s: %w", cleanPath, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ReadInfo returns the owner metadata recorded in the lock file. It is informational:
// advisory locks are released by the kernel, so stale metadata never needs recovery.
func ReadInfo(path string) (Info, error) {
	// #nosec G304 -- lock path is operator supplied by design.
	data, err := os.ReadFile(path)
	if err != nil {
		return Info{}, fmt.Errorf("read lock %s: %w", path, err)
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return Info{}, fmt.Errorf("decode lock %s: %w", path, err)
	}
	return info, nil
}

// Release unlocks and closes the lock file. It is safe to call more than once. The file is left
// in place: deleting it after unlock would let a waiting contender lock an unlinked inode while a
// new contender locks the replacement file, defeating the coordination guarantee. The kernel
// releases the lock on process exit, and the remaining owner metadata is informational only.
func (l *Lock) Release() error {
	if l == nil || l.path == nil {
		return nil
	}
	_ = syscall.Flock(int(l.path.Fd()), syscall.LOCK_UN)
	err := l.path.Close()
	l.path = nil
	return err
}
