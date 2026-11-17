// Package lock provides advisory file locks for coordinating concurrent analysis runs.
package lock

import (
	"fmt"
	"os"
	"path/filepath"
)

// Lock represents an acquired advisory lock.
type Lock struct {
	path *os.File
}

// Acquire creates or opens path and acquires an exclusive lock using O_EXCL creation.
// The lock is released by calling Release.
func Acquire(path string) (*Lock, error) {
	if path == "" {
		return nil, fmt.Errorf("lock path is required")
	}
	cleanPath := filepath.Clean(path)
	dir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	f, err := os.OpenFile(cleanPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("acquire lock %s: %w", cleanPath, err)
	}
	return &Lock{path: f}, nil
}

// Release removes the lock file.
func (l *Lock) Release() error {
	if l == nil || l.path == nil {
		return nil
	}
	name := l.path.Name()
	_ = l.path.Close()
	l.path = nil
	_ = os.Remove(name)
	return nil
}
