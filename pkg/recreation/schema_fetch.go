package recreation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SchemaFetcher retrieves a provider schema document from a URL. The CLI supplies a registry
// client implementation; tests inject a local function so no network is needed.
type SchemaFetcher func(ctx context.Context, rawURL string) ([]byte, error)

// LoadRemoteProviderSchema obtains a provider schema document from a registry-like URL and keeps
// it in the bounded, expiring on-disk schema cache. Only the `provider_schemas` block is used:
// ParseProviderSchema ignores unrelated top-level fields, and a document without that block is
// rejected rather than cached.
func LoadRemoteProviderSchema(ctx context.Context, fetch SchemaFetcher, rawURL, cacheDir string) (*ProviderSchemas, error) {
	if fetch == nil {
		return nil, fmt.Errorf("provider schema fetcher is required")
	}

	cacheFile := ""
	if cacheDir != "" {
		cacheFile = filepath.Join(cacheDir, "schemas", remoteSchemaCacheKey(rawURL)+".json")
		if fi, err := os.Stat(cacheFile); err == nil && time.Since(fi.ModTime()) < persistentSchemaTTL {
			if cached, err := LoadProviderSchema(cacheFile); err == nil {
				return cached, nil
			}
		}
	}

	data, err := fetch(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch provider schema %s: %w", rawURL, err)
	}
	ps, err := ParseProviderSchema(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if ps == nil || len(ps.ProviderSchemas) == 0 {
		return nil, fmt.Errorf("provider schema %s has no provider_schemas block", rawURL)
	}
	if err := CacheProviderSchema(cacheDir, remoteSchemaCacheKey(rawURL), ps); err != nil {
		return nil, err
	}
	return ps, nil
}

// remoteSchemaCacheKey is a stable, filesystem-safe cache key for a schema URL. A hash avoids
// collisions between URLs whose punctuation would otherwise sanitise to the same filename.
func remoteSchemaCacheKey(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return "url-" + hex.EncodeToString(sum[:8])
}
