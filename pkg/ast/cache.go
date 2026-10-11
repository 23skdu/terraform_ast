package ast

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

func cachedModule(path, fingerprint string) (*ModuleInfo, bool, error) {
	moduleCache.Lock()
	entry, exists := moduleCache.entries[path]
	moduleCache.Unlock()
	if !exists || entry.fingerprint != fingerprint {
		return nil, false, nil
	}

	var info ModuleInfo
	if err := json.Unmarshal(entry.data, &info); err != nil {
		return nil, false, fmt.Errorf("failed to decode cached module inspection for %s: %w", path, err)
	}
	return &info, true, nil
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
	return nil
}
