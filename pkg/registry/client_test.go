package registry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestTarGz(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0600,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
	}

	// Add a directory entry
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "subfolder/",
		Typeflag: tar.TypeDir,
		Mode:     0750,
	}))

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

func TestIsRegistrySource(t *testing.T) {
	assert.True(t, IsRegistrySource("terraform-aws-modules/eks/aws"))
	assert.True(t, IsRegistrySource("terraform-google-modules/kubernetes-engine/google"))
	assert.True(t, IsRegistrySource("app.terraform.io/my-org/eks/aws"))

	assert.False(t, IsRegistrySource("./modules/eks"))
	assert.False(t, IsRegistrySource("../modules/eks"))
	assert.False(t, IsRegistrySource("/absolute/path"))
	assert.False(t, IsRegistrySource("git::https://github.com/org/repo.git"))
	assert.False(t, IsRegistrySource("git@github.com:org/repo.git"))
	assert.False(t, IsRegistrySource("https://github.com/org/repo.git"))
	assert.False(t, IsRegistrySource("invalid-source"))
}

func TestParseRegistrySource(t *testing.T) {
	host, ns, name, prov, err := ParseRegistrySource("terraform-aws-modules/eks/aws")
	require.NoError(t, err)
	assert.Equal(t, "registry.terraform.io", host)
	assert.Equal(t, "terraform-aws-modules", ns)
	assert.Equal(t, "eks", name)
	assert.Equal(t, "aws", prov)

	host, ns, name, prov, err = ParseRegistrySource("custom.reg.io/my-org/gke/google")
	require.NoError(t, err)
	assert.Equal(t, "custom.reg.io", host)
	assert.Equal(t, "my-org", ns)
	assert.Equal(t, "gke", name)
	assert.Equal(t, "google", prov)

	_, _, _, _, err = ParseRegistrySource("invalid/two-parts")
	assert.Error(t, err)
}

func TestRegistryClient_ListVersions_And_GetLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/modules/hashicorp/consul/aws/versions" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"modules": [
					{
						"source": "hashicorp/consul/aws",
						"versions": [
							{"version": "0.1.0"},
							{"version": "v1.2.0"},
							{"version": "v2.0.0"}
						]
					}
				]
			}`))
			return
		}
		if r.URL.Path == "/v1/modules/hashicorp/empty/aws/versions" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"modules": []}`))
			return
		}
		if r.URL.Path == "/v1/modules/hashicorp/no-valid/aws/versions" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"modules": [{"versions": [{"version": "not-semver"}]}]}`))
			return
		}
		if r.URL.Path == "/v1/modules/hashicorp/bad-json/aws/versions" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{invalid-json}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.BaseURL = server.URL

	ctx := context.Background()

	// 1. Success list
	versions, err := client.ListVersions(ctx, "hashicorp/consul/aws")
	require.NoError(t, err)
	assert.Equal(t, []string{"0.1.0", "v1.2.0", "v2.0.0"}, versions)

	// 2. Latest version
	latest, err := client.GetLatestVersion(ctx, "hashicorp/consul/aws")
	require.NoError(t, err)
	assert.Equal(t, "v2.0.0", latest)

	// 3. Error 404
	_, err = client.ListVersions(ctx, "hashicorp/missing/aws")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 404")

	// 4. Empty modules list
	_, err = client.ListVersions(ctx, "hashicorp/empty/aws")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no module information")

	// 5. No valid versions in latest
	_, err = client.GetLatestVersion(ctx, "hashicorp/no-valid/aws")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no valid semantic versions")

	// 6. Bad JSON
	_, err = client.ListVersions(ctx, "hashicorp/bad-json/aws")
	assert.Error(t, err)

	// 7. Invalid source format
	_, err = client.ListVersions(ctx, "invalid")
	assert.Error(t, err)

	// 8. Default client initialization
	defaultClient := NewClient(nil)
	assert.NotNil(t, defaultClient.HTTPClient)
}

func TestRegistryClient_DownloadModule(t *testing.T) {
	tarData := createTestTarGz(t, map[string]string{
		"main.tf":      `variable "foo" {}`,
		"variables.tf": `variable "bar" { default = 1 }`,
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/modules/hashicorp/vault/aws/1.0.0/download":
			w.Header().Set("X-Terraform-Get", "/archive.tar.gz")
			w.WriteHeader(http.StatusNoContent)
		case "/archive.tar.gz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(tarData)
		case "/v1/modules/hashicorp/direct/aws/1.0.0/download", "/v1/modules/hashicorp/direct/aws/download":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(tarData)
		case "/v1/modules/hashicorp/bad-dl/aws/1.0.0/download":
			w.Header().Set("X-Terraform-Get", "/missing-archive.tar.gz")
			w.WriteHeader(http.StatusNoContent)
		case "/missing-archive.tar.gz":
			w.WriteHeader(http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.BaseURL = server.URL
	ctx := context.Background()

	// 1. Download via X-Terraform-Get header
	destDir := t.TempDir()
	err := client.DownloadModule(ctx, "hashicorp/vault/aws", "1.0.0", destDir)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(destDir, "main.tf"))
	assert.FileExists(t, filepath.Join(destDir, "variables.tf"))

	// 2. Direct body response
	destDir2 := t.TempDir()
	err = client.DownloadModule(ctx, "hashicorp/direct/aws", "v1.0.0", destDir2)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(destDir2, "main.tf"))

	// 3. Archive fetch failure (404 on redirect)
	err = client.DownloadModule(ctx, "hashicorp/bad-dl/aws", "1.0.0", t.TempDir())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "archive fetch returned status 404")

	// 4. Invalid source format
	err = client.DownloadModule(ctx, "invalid-src", "1.0.0", t.TempDir())
	assert.Error(t, err)

	// 6. Non-200/204 response on initial download
	err = client.DownloadModule(ctx, "hashicorp/not-found/aws", "1.0.0", t.TempDir())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "module download returned status 404")

	// 7. Network error on second download request
	badNetworkServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Terraform-Get", "http://127.0.0.1:0/unreachable")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer badNetworkServer.Close()
	badNetClient := NewClient(badNetworkServer.Client())
	badNetClient.BaseURL = badNetworkServer.URL
	err = badNetClient.DownloadModule(ctx, "hashicorp/bad-net/aws", "1.0.0", t.TempDir())
	assert.Error(t, err)

	// 8. buildBaseURL with standard default BaseURL
	stdClient := NewClient(nil)
	assert.Equal(t, "https://registry.terraform.io", stdClient.buildBaseURL("registry.terraform.io"))

	// 9. GetLatestVersion error when ListVersions fails
	_, err = client.GetLatestVersion(ctx, "invalid-source")
	assert.Error(t, err)

	// 10. ListVersions and DownloadModule with cancelled context
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.ListVersions(canceledCtx, "hashicorp/consul/aws")
	assert.Error(t, err)

	err = client.DownloadModule(canceledCtx, "hashicorp/direct/aws", "1.0.0", t.TempDir())
	assert.Error(t, err)

	// 11. Empty version in DownloadModule
	err = client.DownloadModule(ctx, "hashicorp/direct/aws", "", destDir)
	require.NoError(t, err)

	// 12. extractTarGz with invalid gzip data
	err = extractTarGz(bytes.NewReader([]byte("not-gzip")), t.TempDir())
	assert.Error(t, err)
}

func TestExtractTarGz_PathTraversalProtection(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Attempt path traversal
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "../../etc/passwd",
		Mode:     0600,
		Size:     4,
		Typeflag: tar.TypeReg,
	}))
	_, err := tw.Write([]byte("root"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	err = extractTarGz(&buf, t.TempDir())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "illegal file path in tar archive")
}
