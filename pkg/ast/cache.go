package ast

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const moduleCacheCapacity = 128

type moduleCacheEntry struct {
	fingerprint string
	data        []byte
}

var moduleCache = struct {
	sync.Mutex
	entries map[string]moduleCacheEntry
	order   []string
}{entries: make(map[string]moduleCacheEntry)}

func moduleFingerprint(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("failed to fingerprint module directory %s: %w", dir, err)
	}
	hash := sha256.New()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (!strings.HasSuffix(name, ".tf") && !strings.HasSuffix(name, ".tf.json")) {
			continue
		}
		path := filepath.Join(dir, name)
		// #nosec G304 -- path is joined from a directory entry read via os.ReadDir
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("failed to fingerprint module file %s: %w", path, err)
		}
		_, _ = hash.Write([]byte(name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

var (
	persistentCacheMu  sync.RWMutex
	persistentCacheDir string
)

// SetPersistentCacheDir sets the directory path for L2 persistent disk caching.
func SetPersistentCacheDir(dir string) {
	persistentCacheMu.Lock()
	defer persistentCacheMu.Unlock()
	if strings.TrimSpace(dir) == "" {
		persistentCacheDir = ""
		return
	}
	persistentCacheDir = filepath.Clean(dir)
}

// GetPersistentCacheDir returns the current L2 persistent disk cache directory.
func GetPersistentCacheDir() string {
	persistentCacheMu.RLock()
	defer persistentCacheMu.RUnlock()
	return persistentCacheDir
}

// cacheEntryPattern is the naming scheme of every file this cache writes.
const cacheEntryPattern = ".json"

// ClearPersistentCache removes the cached module inspections.
//
// It deliberately deletes only the cache artefacts, by pattern, and refuses to act on a directory
// that does not look like one. The previous implementation called os.RemoveAll on whatever path
// the operator passed to --cache-dir, so `--cache-dir .` destroyed the repository, and a typo in
// the path destroyed an unrelated directory.
func ClearPersistentCache() error {
	dir := GetPersistentCacheDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read cache directory %s: %w", dir, err)
	}

	// Refuse to clear a directory holding anything this cache did not write.
	var candidates []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, cacheEntryPattern) || !isCacheFingerprint(name) {
			return fmt.Errorf("refusing to clear %s: it contains %q, which is not a cache entry", dir, name)
		}
		candidates = append(candidates, name)
	}

	sort.Strings(candidates)
	for _, name := range candidates {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("failed to remove cache entry %s: %w", name, err)
		}
	}
	return nil
}

// isCacheFingerprint reports whether a filename is exactly a hex SHA-256 followed by ".json",
// which is the only shape cacheModule writes.
func isCacheFingerprint(name string) bool {
	base := strings.TrimSuffix(name, cacheEntryPattern)
	if base == name || len(base) != sha256.Size*2 {
		return false
	}
	for _, r := range base {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

func cachedModule(path, fingerprint string) (*ModuleInfo, bool, error) {
	moduleCache.Lock()
	entry, exists := moduleCache.entries[path]
	moduleCache.Unlock()
	if exists && entry.fingerprint == fingerprint {
		var info ModuleInfo
		if err := json.Unmarshal(entry.data, &info); err != nil {
			return nil, false, fmt.Errorf("failed to decode cached module inspection for %s: %w", path, err)
		}
		return &info, true, nil
	}

	diskDir := GetPersistentCacheDir()
	if diskDir != "" {
		diskFile := filepath.Join(diskDir, fingerprint+".json")
		// #nosec G304 -- disk cache file path
		data, err := os.ReadFile(diskFile)
		if err == nil {
			var info ModuleInfo
			if err := json.Unmarshal(data, &info); err == nil {
				_ = cacheModule(path, fingerprint, &info)
				return &info, true, nil
			}
		}
	}

	return nil, false, nil
}

// forgetModuleCacheEntry drops a cached inspection so the next request re-reads the module.
func forgetModuleCacheEntry(path string) {
	moduleCache.Lock()
	defer moduleCache.Unlock()

	delete(moduleCache.entries, path)
	for i, key := range moduleCache.order {
		if key == path {
			moduleCache.order = append(moduleCache.order[:i], moduleCache.order[i+1:]...)
			break
		}
	}
}

func cacheModule(path, fingerprint string, info *ModuleInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to cache module inspection for %s: %w", path, err)
	}

	moduleCache.Lock()
	defer moduleCache.Unlock()
	if _, exists := moduleCache.entries[path]; !exists {
		moduleCache.order = append(moduleCache.order, path)
	}
	moduleCache.entries[path] = moduleCacheEntry{fingerprint: fingerprint, data: data}

	if len(moduleCache.order) > moduleCacheCapacity {
		oldest := moduleCache.order[0]
		moduleCache.order = moduleCache.order[1:]
		delete(moduleCache.entries, oldest)
	}

	diskDir := GetPersistentCacheDir()
	if diskDir != "" {
		if err := os.MkdirAll(diskDir, 0o750); err == nil {
			diskFile := filepath.Join(diskDir, fingerprint+".json")
			_ = os.WriteFile(diskFile, data, 0o600)
		}
	}
	return nil
}
