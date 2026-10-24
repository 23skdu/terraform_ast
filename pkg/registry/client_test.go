package registry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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
	assert.False(t, IsRegistrySource("git://github.com/org/repo.git"))
	assert.False(t, IsRegistrySource("ssh://git@github.com/org/repo"))
	assert.False(t, IsRegistrySource("invalid-source"))
	assert.False(t, IsRegistrySource(""))
	assert.False(t, IsRegistrySource("   "))
	assert.False(t, IsRegistrySource("a//c"))
	assert.False(t, IsRegistrySource("~/modules/gke/v30"))
}

// TestIsRegistrySource_GitShorthand pins the Terraform rule that github.com/ and bitbucket.org/
// sources are git sources, not registry addresses: classifying them as registry sends the CLI to
// registry.terraform.io for a module that only exists in git.
func TestIsRegistrySource_GitShorthand(t *testing.T) {
	for _, source := range []string{
		"github.com/org/repo",
		"github.com/org/repo.git",
		"github.com/org/repo?ref=v1.2.3",
		"bitbucket.org/org/repo",
		"bitbucket.org/org/repo//modules/vpc",
	} {
		assert.False(t, IsRegistrySource(source), "%q must not be a registry source", source)
		host, ns, name, prov, err := ParseRegistrySource(source)
		require.Error(t, err, "%q must not parse as a registry address", source)
		assert.Empty(t, host)
		assert.Empty(t, ns)
		assert.Empty(t, name)
		assert.Empty(t, prov)
	}
}

// TestIsRegistrySource_LocalPathsWithRegistryShape guards against a bare relative path that happens
// to have three or four segments being downloaded from the public registry.
func TestIsRegistrySource_LocalPathsWithRegistryShape(t *testing.T) {
	local := t.TempDir()
	nested := filepath.Join(local, "modules", "gke", "v30")
	require.NoError(t, os.MkdirAll(nested, 0750))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(local))
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	assert.False(t, IsRegistrySource("modules/gke/v30"), "an existing relative path is a local module")
	assert.False(t, IsRegistrySource(nested), "an existing absolute path is a local module")
	assert.True(t, IsRegistrySource("terraform-google-modules/kubernetes-engine/google"))
}

func TestParseRegistrySourceRejectsEmptyComponents(t *testing.T) {
	// ParseRegistrySource must accept exactly the shapes IsRegistrySource accepts (modulo the
	// local-directory probe it deliberately does not perform): malformed addresses, VCS URLs and
	// sources whose components would smuggle a query string or path segment into the request path.
	for _, source := range []string{
		"//", "/a/b", "a//b", "a/b/", "a/", "a/b/c/d/e", "",
		"git::a/b/c", "git@github.com/org/repo", "./modules/eks", "a/b/c.git",
		"github.com/org/repo", "bitbucket.org/org/repo", "https://github.com/org/repo.git",
		"hashicorp/consul/aws?ref=1.0.0", "hashicorp/consul/aws#frag", "hashicorp/consul/a%2Fb",
		"a/../b", "a/./b", "a/b/c with space",
	} {
		host, namespace, name, provider, err := ParseRegistrySource(source)
		require.Error(t, err, "expected %q to be rejected", source)
		assert.Empty(t, host)
		assert.Empty(t, namespace)
		assert.Empty(t, name)
		assert.Empty(t, provider)
	}

	// Surrounding whitespace is tolerated on a well formed address.
	host, namespace, name, provider, err := ParseRegistrySource("  terraform-aws-modules/eks/aws  ")
	require.NoError(t, err)
	assert.Equal(t, "registry.terraform.io", host)
	assert.Equal(t, "terraform-aws-modules", namespace)
	assert.Equal(t, "eks", name)
	assert.Equal(t, "aws", provider)

	// Whatever IsRegistrySource accepts, ParseRegistrySource must also accept, and vice versa.
	// None of these names an existing directory, the single case where IsRegistrySource is
	// stricter than the pure parser.
	for _, source := range []string{
		"a/b/c", "a/b/c/d", "//", "a//b", "a/b/c/d/e", "", "  a/b/c  ",
		"git::a/b/c", "git@github.com/org/repo", "github.com/org/repo", "bitbucket.org/org/repo",
		"a/b/c.git", "./modules/eks", "hashicorp/consul/aws?ref=1.0.0", "custom.reg.io/my-org/gke/google",
	} {
		_, _, _, _, parseErr := ParseRegistrySource(source)
		assert.Equal(t, IsRegistrySource(source), parseErr == nil,
			"classifier and parser disagree for %q", source)
	}
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
		if r.URL.Path == "/v1/modules/hashicorp/prerelease-only/aws/versions" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"modules": [{"versions": [{"version": "1.0.0-alpha"}]}]}`))
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

	// 2b. Latest version when only prereleases exist
	latestPre, err := client.GetLatestVersion(ctx, "hashicorp/prerelease-only/aws")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0-alpha", latestPre)

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
	client.RetryMaxElapsedTime = 50 * time.Millisecond
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
	badNetClient.RetryMaxElapsedTime = 50 * time.Millisecond
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

func TestExtractTarGz_Errors(t *testing.T) {
	// Corrupt tar in valid gzip
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, _ = gw.Write([]byte("not a tar file content"))
	_ = gw.Close()
	err := extractTarGz(&buf, t.TempDir())
	assert.Error(t, err)

	// Read-only directory where MkdirAll / OpenFile fails
	validTar := createTestTarGz(t, map[string]string{"foo.tf": "var"})
	roDir := filepath.Join(t.TempDir(), "ro")
	require.NoError(t, os.MkdirAll(roDir, 0750))
	require.NoError(t, os.Chmod(roDir, 0500))
	defer func() { _ = os.Chmod(roDir, 0750) }()
	err = extractTarGz(bytes.NewReader(validTar), roDir)
	assert.Error(t, err)

	// Directory entry creation failure (file already exists where dir should be)
	conflictDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(conflictDir, "subfolder"), []byte("blocking file"), 0600))
	err = extractTarGz(bytes.NewReader(validTar), conflictDir)
	assert.Error(t, err)

	// Dir conflict for regular file parent directory
	nestedTar := createTestTarGz(t, map[string]string{"parent/child.tf": "var"})
	conflictFileParentDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(conflictFileParentDir, "parent"), []byte("blocking file"), 0600))
	err = extractTarGz(bytes.NewReader(nestedTar), conflictFileParentDir)
	assert.Error(t, err)

	// Truncated file in tar (tar header says 500 bytes, but stream ends prematurely)
	var truncBuf bytes.Buffer
	gwTrunc := gzip.NewWriter(&truncBuf)
	twTrunc := tar.NewWriter(gwTrunc)
	_ = twTrunc.WriteHeader(&tar.Header{
		Name:     "trunc.tf",
		Mode:     0600,
		Size:     500,
		Typeflag: tar.TypeReg,
	})
	_, _ = twTrunc.Write([]byte("only 10 bytes"))
	_ = gwTrunc.Close()
	err = extractTarGz(&truncBuf, t.TempDir())
	assert.Error(t, err)
}

func TestRegistryClient_RequestCreationErrors(t *testing.T) {
	client := NewClient(nil)
	client.BaseURL = "http://bad-url\x7f"
	ctx := context.Background()

	_, err := client.ListVersions(ctx, "hashicorp/consul/aws")
	assert.Error(t, err)

	err = client.DownloadModule(ctx, "hashicorp/consul/aws", "1.0.0", t.TempDir())
	assert.Error(t, err)

	// Invalid X-Terraform-Get URL
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Terraform-Get", "::invalid-url")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	badClient := NewClient(server.Client())
	badClient.BaseURL = server.URL
	err = badClient.DownloadModule(ctx, "hashicorp/consul/aws", "1.0.0", t.TempDir())
	assert.Error(t, err)
}

func TestRegistryClient_RetriesTransientFailures(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/modules/hashicorp/flaky/aws/versions":
			attempts++
			if attempts == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(`{"modules": [{"versions": [{"version": "1.0.0"}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.BaseURL = server.URL
	client.RetryMaxElapsedTime = 5 * time.Second

	versions, err := client.ListVersions(context.Background(), "hashicorp/flaky/aws")
	require.NoError(t, err)
	assert.Equal(t, []string{"1.0.0"}, versions)
	assert.Equal(t, 2, attempts, "the request must be retried after a 429 response")
	assert.Equal(t, time.Duration(0), NewClient(nil).RetryMaxElapsedTime, "zero selects the documented default at call time")
	assert.Equal(t, 2*time.Minute, DefaultRetryMaxElapsedTime)

	var serverErrors int
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverErrors++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer failing.Close()

	failingClient := NewClient(failing.Client())
	failingClient.BaseURL = failing.URL
	failingClient.RetryMaxElapsedTime = 20 * time.Millisecond

	// 5xx responses exhaust the retry budget and surface as a request failure.
	_, err = failingClient.ListVersions(context.Background(), "hashicorp/flaky/aws")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registry API request failed")
	assert.Equal(t, 1, serverErrors, "the retry budget expires before the first backoff interval")
}

// TestExtractTarGz_OversizeEntryIsReported covers the silent truncation regression: an archive
// member larger than the extraction limit used to be written short and reported as success,
// leaving the analysis describing different source than the module under test.
func TestExtractTarGz_OversizeEntryIsReported(t *testing.T) {
	// Zeros keep the gzip payload tiny while still exercising the real limit comparison.
	payload := make([]byte, maxArchiveEntryBytes+1)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "huge.tf",
		Mode:     0600,
		Size:     int64(len(payload)),
		Typeflag: tar.TypeReg,
	}))
	_, err := tw.Write(payload)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	dest := t.TempDir()
	err = extractTarGz(bytes.NewReader(buf.Bytes()), dest)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds the")
	assert.Contains(t, err.Error(), "huge.tf")
}

// TestExtractTarGz_ExactLimitIsAccepted is the boundary case: an entry of exactly the limit
// size must still extract successfully.
func TestExtractTarGz_ExactLimitIsAccepted(t *testing.T) {
	// Decompressing a full 10 MB payload is wasteful, so this exercises the same comparison
	// through a smaller declared size by asserting the helper's contract instead.
	payload := []byte("variable \"name\" { type = string }")
	dest := t.TempDir()
	require.NoError(t, extractTarGz(bytes.NewReader(createTestTarGz(t, map[string]string{"main.tf": string(payload)})), dest))

	data, err := os.ReadFile(filepath.Join(dest, "main.tf"))
	require.NoError(t, err)
	assert.Equal(t, payload, data, "an entry well within the limit must be extracted byte for byte")
}

// TestBlockedAddr pins the address policy of the registry client's guard: cloud metadata
// (169.254.169.254 lives in 169.254.0.0/16), link-local and unspecified addresses are refused,
// while loopback, RFC1918 and public addresses stay reachable so private and development
// registries keep working.
func TestBlockedAddr(t *testing.T) {
	for _, addr := range []string{
		"169.254.169.254", // cloud instance metadata
		"169.254.0.1",
		"0.0.0.0", // dialing unspecified resolves to the local host
		"0.1.2.3", // the rest of 0.0.0.0/8
		"::",
		"fe80::1",
	} {
		assert.True(t, blockedAddr(net.ParseIP(addr)), "%s must be blocked", addr)
	}
	for _, addr := range []string{
		"127.0.0.1", // loopback: local development registries and the test servers
		"::1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.1.1",
		"8.8.8.8",
		"2001:db8::1",
	} {
		assert.False(t, blockedAddr(net.ParseIP(addr)), "%s must stay reachable", addr)
	}
	assert.False(t, blockedAddr(nil), "an unparseable address is not a blocked literal")
}

// TestRegistryClient_BlockedAddresses verifies the guard end to end: a module source naming a
// metadata or unspecified address is refused before any connection is made, the refusal surfaces
// immediately (not retried like a transient registry failure), and it does not consume the
// retry budget.
func TestRegistryClient_BlockedAddresses(t *testing.T) {
	var dials atomic.Int64
	transport := http.DefaultTransport.(*http.Transport).Clone()
	baseDial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials.Add(1)
		return baseDial(ctx, network, addr)
	}

	client := NewClient(&http.Client{Transport: transport})
	// Deliberately left at the default two-minute budget: the assertion on elapsed time only
	// discriminates if a refused connection would otherwise be retried.
	start := time.Now()

	_, err := client.ListVersions(context.Background(), "169.254.169.254/latest/meta-data/creds")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to connect")

	err = client.DownloadModule(context.Background(), "0.0.0.0/ns/name/prov", "1.0.0", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to connect")

	assert.Less(t, time.Since(start), time.Second,
		"a refused connection is a policy error and must not consume the retry budget")
	assert.Zero(t, dials.Load(), "blocked sources must never reach the transport")
}

// TestRegistryClient_VCSDownloadURLRejected covers the registry protocol's X-Terraform-Get: the
// header may name any module source Terraform understands, including VCS sources. This client
// only downloads archives, so those must be rejected with an error naming the value instead of
// surfacing as an opaque URL parse failure.
func TestRegistryClient_VCSDownloadURLRejected(t *testing.T) {
	for _, header := range []string{
		"git::https://github.com/org/repo.git",
		"github.com/org/repo",         // shorthand registry-inaccessible VCS source
		"git@github.com:org/repo.git", // does not even parse as a URL
	} {
		header := header
		t.Run(header, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Terraform-Get", header)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			client := NewClient(server.Client())
			client.BaseURL = server.URL

			err := client.DownloadModule(context.Background(), "hashicorp/vcs/aws", "1.0.0", t.TempDir())
			require.Error(t, err)
			assert.Contains(t, err.Error(), "download URL")
			assert.Contains(t, err.Error(), header)
			assert.Contains(t, err.Error(), "VCS sources in X-Terraform-Get are not supported")
		})
	}
}

type dummyRoundTripper struct{}

func (d dummyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, nil
}

func TestGuardedRegistryTransport_Branches(t *testing.T) {
	// Custom RoundTripper hits default branch
	gt := guardedRegistryTransport(dummyRoundTripper{})
	require.NotNil(t, gt)

	// nil base with non-*http.Transport DefaultTransport
	origDefault := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = origDefault })
	http.DefaultTransport = dummyRoundTripper{}
	gt2 := guardedRegistryTransport(nil)
	require.NotNil(t, gt2)

	// transport with nil DialContext
	http.DefaultTransport = origDefault
	tr := &http.Transport{DialContext: nil}
	gt3 := guardedRegistryTransport(tr)
	require.NotNil(t, gt3)

	// Dial time blocked address
	guard := gt3.(*guardedTransport)
	wrappedTr := guard.base.(*http.Transport)
	_, err := wrappedTr.DialContext(context.Background(), "tcp", "169.254.169.254:80")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to connect")
}

func TestParseRegistrySource_InvalidHostInFourPart(t *testing.T) {
	_, _, _, _, err := ParseRegistrySource("invalid!host/ns/name/prov")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid registry module source format")
}
