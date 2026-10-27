package ast

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
)

const moduleCacheCapacity = 128

type moduleCacheEntry struct {
	fingerprint  string
	data         []byte
	validDataLen int
	info         *ModuleInfo
}

var moduleCache = struct {
	sync.Mutex
	entries map[string]moduleCacheEntry
	order   []string
}{entries: make(map[string]moduleCacheEntry)}

var fingerprintBufPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 32*1024)
		return &buf
	},
}

func cloneModuleInfo(src *ModuleInfo) *ModuleInfo {
	dst := *src
	dst.Variables = maps.Clone(src.Variables)
	dst.Outputs = maps.Clone(src.Outputs)
	dst.ModuleCalls = maps.Clone(src.ModuleCalls)
	dst.RequiredProviders = maps.Clone(src.RequiredProviders)
	dst.WrapperOutputReferences = maps.Clone(src.WrapperOutputReferences)
	dst.CheckBlocks = maps.Clone(src.CheckBlocks)
	dst.Functions = maps.Clone(src.Functions)
	dst.ManagedResources = maps.Clone(src.ManagedResources)
	dst.DataResources = maps.Clone(src.DataResources)
	dst.RemovedBlocks = slices.Clone(src.RemovedBlocks)
	dst.MovedBlocks = slices.Clone(src.MovedBlocks)
	dst.ImportBlocks = slices.Clone(src.ImportBlocks)
	dst.EncryptionBlocks = slices.Clone(src.EncryptionBlocks)
	return &dst
}

func moduleFingerprint(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("failed to fingerprint module directory %s: %w", dir, err)
	}
	hash := sha256.New()
	bufPtr := fingerprintBufPool.Get().(*[]byte)
	buf := *bufPtr
	defer fingerprintBufPool.Put(bufPtr)
	var zero [1]byte
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (!strings.HasSuffix(name, ".tf") && !strings.HasSuffix(name, ".tf.json")) {
			continue
		}
		path := dir + "/" + name
		fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
		if err != nil {
			return "", fmt.Errorf("failed to fingerprint module file %s: %w", path, err)
		}
		_, _ = io.WriteString(hash, name)
		_, _ = hash.Write(zero[:])
		for {
			n, _ := syscall.Read(fd, buf)
			if n <= 0 {
				break
			}
			_, _ = hash.Write(buf[:n])
		}
		_ = syscall.Close(fd)
		_, _ = hash.Write(zero[:])
	}
	var sum [sha256.Size]byte
	return hex.EncodeToString(hash.Sum(sum[:0])), nil
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
		if len(entry.data) != entry.validDataLen {
			return nil, false, fmt.Errorf("failed to decode cached module inspection for %s: corrupt cache entry", path)
		}
		return cloneModuleInfo(entry.info), true, nil
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

func validateModuleSerializable(info *ModuleInfo) error {
	for _, v := range info.Variables {
		switch val := v.Default.(type) {
		case nil, bool, float64, int, string:
		default:
			if _, err := json.Marshal(val); err != nil {
				return err
			}
		}
	}
	return nil
}

func cacheModule(path, fingerprint string, info *ModuleInfo) error {
	if err := validateModuleSerializable(info); err != nil {
		return fmt.Errorf("failed to cache module inspection for %s: %w", path, err)
	}

	diskDir := GetPersistentCacheDir()
	var data []byte
	if diskDir != "" {
		data, _ = json.Marshal(info)
	}

	moduleCache.Lock()
	defer moduleCache.Unlock()
	if _, exists := moduleCache.entries[path]; !exists {
		moduleCache.order = append(moduleCache.order, path)
	}
	moduleCache.entries[path] = moduleCacheEntry{
		fingerprint:  fingerprint,
		data:         data,
		validDataLen: len(data),
		info:         cloneModuleInfo(info),
	}

	if len(moduleCache.order) > moduleCacheCapacity {
		oldest := moduleCache.order[0]
		moduleCache.order = moduleCache.order[1:]
		delete(moduleCache.entries, oldest)
	}

	if diskDir != "" {
		if err := os.MkdirAll(diskDir, 0o750); err == nil {
			tmpFile, err := os.CreateTemp(diskDir, "cache-tmp-*")
			if err == nil {
				tmpName := tmpFile.Name()
				_, _ = tmpFile.Write(data)
				_ = tmpFile.Close()
				diskFile := filepath.Join(diskDir, fingerprint+".json")
				_ = os.Rename(tmpName, diskFile)
			}
		}
	}
	return nil
}
