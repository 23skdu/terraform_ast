package recreation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const remoteSchemaURL = "https://schemas.example.com/hashicorp/google/5.0.0.json"

func TestLoadRemoteProviderSchemaFetchAndCache(t *testing.T) {
	cacheDir := t.TempDir()
	calls := 0
	fetch := func(_ context.Context, rawURL string) ([]byte, error) {
		calls++
		assert.Equal(t, remoteSchemaURL, rawURL)
		return []byte(sampleProviderSchema), nil
	}

	ps, err := LoadRemoteProviderSchema(context.Background(), fetch, remoteSchemaURL, cacheDir)
	require.NoError(t, err)
	assert.Contains(t, ps.ProviderSchemas, "registry.terraform.io/hashicorp/google")
	assert.Equal(t, 1, calls)

	// A fresh on-disk entry is served without contacting the endpoint again.
	cached, err := LoadRemoteProviderSchema(context.Background(), fetch, remoteSchemaURL, cacheDir)
	require.NoError(t, err)
	assert.Equal(t, ps, cached)
	assert.Equal(t, 1, calls)

	// An entry older than the bounded TTL is refreshed.
	cacheFile := filepath.Join(cacheDir, "schemas", remoteSchemaCacheKey(remoteSchemaURL)+".json")
	stale := time.Now().Add(-2 * persistentSchemaTTL)
	require.NoError(t, os.Chtimes(cacheFile, stale, stale))
	_, err = LoadRemoteProviderSchema(context.Background(), fetch, remoteSchemaURL, cacheDir)
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
}

func TestLoadRemoteProviderSchemaInvalidCacheIsRefetched(t *testing.T) {
	cacheDir := t.TempDir()
	cacheFile := filepath.Join(cacheDir, "schemas", remoteSchemaCacheKey(remoteSchemaURL)+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(cacheFile), 0o750))
	require.NoError(t, os.WriteFile(cacheFile, []byte("not json"), 0o600))

	calls := 0
	fetch := func(_ context.Context, _ string) ([]byte, error) {
		calls++
		return []byte(sampleProviderSchema), nil
	}
	ps, err := LoadRemoteProviderSchema(context.Background(), fetch, remoteSchemaURL, cacheDir)
	require.NoError(t, err)
	assert.Contains(t, ps.ProviderSchemas, "registry.terraform.io/hashicorp/google")
	assert.Equal(t, 1, calls)
}

func TestLoadRemoteProviderSchemaWithoutCache(t *testing.T) {
	fetch := func(_ context.Context, _ string) ([]byte, error) {
		return []byte(sampleProviderSchema), nil
	}
	ps, err := LoadRemoteProviderSchema(context.Background(), fetch, remoteSchemaURL, "")
	require.NoError(t, err)
	assert.Contains(t, ps.ProviderSchemas, "registry.terraform.io/hashicorp/google")
}

func TestLoadRemoteProviderSchemaErrors(t *testing.T) {
	_, err := LoadRemoteProviderSchema(context.Background(), nil, remoteSchemaURL, t.TempDir())
	assert.ErrorContains(t, err, "fetcher is required")

	_, err = LoadRemoteProviderSchema(context.Background(), func(_ context.Context, _ string) ([]byte, error) {
		return nil, errors.New("network down")
	}, remoteSchemaURL, t.TempDir())
	assert.ErrorContains(t, err, "fetch provider schema")
	assert.ErrorContains(t, err, "network down")

	_, err = LoadRemoteProviderSchema(context.Background(), func(_ context.Context, _ string) ([]byte, error) {
		return []byte("not json"), nil
	}, remoteSchemaURL, t.TempDir())
	assert.ErrorContains(t, err, "decode provider schema JSON")

	_, err = LoadRemoteProviderSchema(context.Background(), func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`{"format_version":"1.0","provider_schemas":{}}`), nil
	}, remoteSchemaURL, t.TempDir())
	assert.ErrorContains(t, err, "no provider_schemas block")

	// A cache directory that is a file cannot be created, so caching fails after the fetch.
	blocked := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o600))
	_, err = LoadRemoteProviderSchema(context.Background(), func(_ context.Context, _ string) ([]byte, error) {
		return []byte(sampleProviderSchema), nil
	}, remoteSchemaURL, blocked)
	assert.ErrorContains(t, err, "create schema cache directory")
}

func TestRemoteSchemaCacheKey(t *testing.T) {
	first := remoteSchemaCacheKey(remoteSchemaURL)
	second := remoteSchemaCacheKey(remoteSchemaURL + "?v=2")
	assert.Equal(t, first, remoteSchemaCacheKey(remoteSchemaURL))
	assert.NotEqual(t, first, second)
	assert.NotContains(t, first, "/")
	assert.NotContains(t, first, ":")
	assert.Contains(t, first, "url-")
}
