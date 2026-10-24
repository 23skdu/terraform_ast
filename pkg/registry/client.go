package registry

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/23skdu/terraform_ast/pkg/semverutil"
	"github.com/cenkalti/backoff/v4"
)

// ModuleVersionsResponse represents the Terraform Registry response for module versions.
type ModuleVersionsResponse struct {
	Modules []struct {
		Source   string `json:"source"`
		Versions []struct {
			Version string `json:"version"`
		} `json:"versions"`
	} `json:"modules"`
}

// DefaultRetryMaxElapsedTime bounds how long transient registry failures are retried.
const DefaultRetryMaxElapsedTime = 2 * time.Minute

// Client interacts with the Terraform Module Registry API.
type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	// RetryMaxElapsedTime bounds how long transient failures (429 and 5xx responses) are retried.
	// Zero selects DefaultRetryMaxElapsedTime; tests lower it to keep runs fast.
	RetryMaxElapsedTime time.Duration
}

// NewClient creates a new Terraform Registry client.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 30 * time.Second,
		}
	}
	// A shallow copy keeps the caller's client untouched while every request this package
	// makes passes through the address guard.
	guarded := *httpClient
	guarded.Transport = guardedRegistryTransport(httpClient.Transport)
	return &Client{
		HTTPClient: &guarded,
		BaseURL:    "https://registry.terraform.io",
	}
}

// blockedAddressError reports a connection the registry client refused to make. It is a policy
// decision rather than a transient failure, so the backoff layer must not retry it.
type blockedAddressError struct {
	address string
}

func (e *blockedAddressError) Error() string {
	return fmt.Sprintf("refusing to connect to %s: registry requests to link-local and unspecified addresses are blocked", e.address)
}

// blockedAddr reports whether ip must never be dialed by the registry client: IPv4 link-local
// (169.254.0.0/16, which contains the 169.254.169.254 cloud instance-metadata endpoint), IPv6
// link-local (fe80::/10) and the unspecified or "this network" addresses (0.0.0.0/8, ::) that
// resolve back to the local host. Public, RFC1918 and loopback registry hosts stay reachable, so
// private and development registries are unaffected.
func blockedAddr(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 0
	}
	return false
}

// guardedRegistryTransport wraps base so no request this package makes can reach a blocked
// address. Dialing is checked after name resolution, which catches a hostname that resolves into
// a blocked range without leaving a resolve-then-recheck window; the round trip check re-verifies
// literal addresses for transports that dial through a proxy.
func guardedRegistryTransport(base http.RoundTripper) http.RoundTripper {
	var transport *http.Transport
	switch t := base.(type) {
	case nil:
		def, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return &guardedTransport{base: http.DefaultTransport}
		}
		transport = def.Clone()
	case *http.Transport:
		transport = t.Clone()
	default:
		return &guardedTransport{base: base}
	}
	dial := transport.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		// At dial time addr is already resolved, so a name resolving into a blocked range is
		// caught here rather than at request time, where the name could still be rewritten.
		if host, _, err := net.SplitHostPort(addr); err == nil {
			if blockedAddr(net.ParseIP(host)) {
				return nil, &blockedAddressError{address: host}
			}
		}
		return dial(ctx, network, addr)
	}
	return &guardedTransport{base: transport}
}

// guardedTransport refuses a request whose literal target is blocked before dispatching it.
type guardedTransport struct {
	base http.RoundTripper
}

func (t *guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL != nil {
		if host := req.URL.Hostname(); blockedAddr(net.ParseIP(host)) {
			return nil, &blockedAddressError{address: host}
		}
	}
	return t.base.RoundTrip(req)
}

// isVCSOrLocalSource reports whether a module source is a VCS, URL or local-path source rather
// than a registry address. It is a pure string check shared by IsRegistrySource and
// ParseRegistrySource, so the classifier and the parser can never disagree about these shapes.
//
// The shapes mirror Terraform's own source classification: local paths (".", "/", "~"), VCS
// addresses ("git::", "git@", any other "://" such as git:// or ssh://), the github.com/ and
// bitbucket.org/ shorthands Terraform expands into git URLs, and the ".git" suffix of a repository.
func isVCSOrLocalSource(source string) bool {
	if strings.HasPrefix(source, ".") ||
		strings.HasPrefix(source, "/") ||
		strings.HasPrefix(source, "~") ||
		strings.HasPrefix(source, "git::") ||
		strings.HasPrefix(source, "git@") ||
		strings.HasPrefix(source, "github.com/") ||
		strings.HasPrefix(source, "bitbucket.org/") ||
		strings.HasSuffix(source, ".git") {
		return true
	}
	// Every remaining URL scheme (http, https, git, ssh, file, ...) carries "://". Checked
	// explicitly instead of relying on the empty path segment a "://" always introduces in the
	// slash split below, so the rejection does not depend on that accident.
	return strings.Contains(source, "://")
}

// registryComponentPattern is the character set of a registry namespace, module name or provider.
// URL metacharacters (?, #, %, @, whitespace and control bytes) are excluded so a component can
// never smuggle a query string, fragment or credentials into the request path built from it.
var registryComponentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// registryHostPattern additionally allows the ":" that separates a registry hostname from its
// port. This is the only component of a four part address that may contain one.
var registryHostPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+(:[0-9]+)?$`)

// IsRegistrySource determines whether a module source string is a Terraform Registry identifier.
//
// It is true only for sources that are neither a VCS/local-path source nor an existing directory
// on disk and that ParseRegistrySource accepts. The directory probe is the only filesystem access,
// so for any source that does not name an existing directory the classifier and the parser agree
// exactly.
func IsRegistrySource(source string) bool {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" || isVCSOrLocalSource(trimmed) {
		return false
	}
	// A source that resolves to a directory on disk is always a local module: a bare relative
	// path such as "modules/gke/v30" has the same shape as a registry address and must not be
	// mistaken for one.
	if fi, err := os.Stat(trimmed); err == nil && fi.IsDir() {
		return false
	}
	_, _, _, _, err := ParseRegistrySource(trimmed)
	return err == nil
}

// ParseRegistrySource parses a registry source into hostname, namespace, name, and provider.
// It accepts the shapes IsRegistrySource accepts, minus the local-directory probe only
// IsRegistrySource performs: the source must not be a VCS URL, local path or github.com /
// bitbucket.org shorthand, and its 3 or 4 components must be non-empty and free of URL
// metacharacters, so a malformed address can never be turned into a request against a
// partially empty or query-extended registry path.
func ParseRegistrySource(source string) (host, namespace, name, provider string, err error) {
	trimmed := strings.TrimSpace(source)
	invalid := func() (string, string, string, string, error) {
		return "", "", "", "", fmt.Errorf("invalid registry module source format: %s", source)
	}
	if trimmed == "" || isVCSOrLocalSource(trimmed) {
		return invalid()
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) != 3 && len(parts) != 4 {
		return invalid()
	}
	for i, part := range parts {
		// The first component of a four part address is the registry hostname; in the three part
		// form every component is a name component.
		if i == 0 && len(parts) == 4 {
			if !registryHostPattern.MatchString(part) {
				return invalid()
			}
			continue
		}
		// "." and ".." would silently change the meaning of the request path built below.
		if part == "." || part == ".." || !registryComponentPattern.MatchString(part) {
			return invalid()
		}
	}
	if len(parts) == 3 {
		return "registry.terraform.io", parts[0], parts[1], parts[2], nil
	}
	return parts[0], parts[1], parts[2], parts[3], nil
}

func (c *Client) doRequestWithBackoff(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	operation := func() error {
		var err error
		resp, err = c.HTTPClient.Do(req)
		if err != nil {
			// A refused connection is a policy decision, not a transient failure: retrying it
			// would only delay the error the caller needs to see.
			var blocked *blockedAddressError
			if errors.As(err, &blocked) {
				return backoff.Permanent(err)
			}
			return err
		}
		if resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode >= 500 && resp.StatusCode <= 599) {
			// The body is discarded so the connection can be reused for the retry.
			_ = resp.Body.Close()
			return fmt.Errorf("temporary registry error: %d", resp.StatusCode)
		}
		return nil
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = c.RetryMaxElapsedTime
	if b.MaxElapsedTime <= 0 {
		b.MaxElapsedTime = DefaultRetryMaxElapsedTime
	}

	err := backoff.Retry(operation, backoff.WithContext(b, req.Context()))
	return resp, err
}

// ListVersions retrieves all available semantic versions for the given module from the registry.
func (c *Client) ListVersions(ctx context.Context, source string) ([]string, error) {
	host, namespace, name, provider, err := ParseRegistrySource(source)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/v1/modules/%s/%s/%s/versions", c.buildBaseURL(host), namespace, name, provider)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.doRequestWithBackoff(req)
	if err != nil {
		return nil, fmt.Errorf("registry API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned status %d for module %s", resp.StatusCode, source)
	}

	var res ModuleVersionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed to parse registry versions response: %w", err)
	}

	if len(res.Modules) == 0 {
		return nil, fmt.Errorf("no module information returned for %s", source)
	}

	var rawVersions []string
	for _, v := range res.Modules[0].Versions {
		rawVersions = append(rawVersions, v.Version)
	}

	valid := semverutil.FilterValid(rawVersions)
	return semverutil.Sort(valid), nil
}

// GetLatestVersion returns the highest semantic version available for the module.
func (c *Client) GetLatestVersion(ctx context.Context, source string) (string, error) {
	versions, err := c.ListVersions(ctx, source)
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no valid semantic versions found for module %s", source)
	}
	stable := semverutil.FilterPrereleases(versions)
	if len(stable) > 0 {
		return stable[len(stable)-1], nil
	}
	return versions[len(versions)-1], nil
}

// DownloadModule downloads and extracts a module version archive into destDir.
func (c *Client) DownloadModule(ctx context.Context, source, version, destDir string) error {
	host, namespace, name, provider, err := ParseRegistrySource(source)
	if err != nil {
		return err
	}

	normVer := strings.TrimPrefix(semverutil.Normalize(version), "v")
	if normVer == "" {
		normVer = version
	}

	var endpoint string
	if normVer == "" {
		endpoint = fmt.Sprintf("%s/v1/modules/%s/%s/%s/download", c.buildBaseURL(host), namespace, name, provider)
	} else {
		endpoint = fmt.Sprintf("%s/v1/modules/%s/%s/%s/%s/download", c.buildBaseURL(host), namespace, name, provider, normVer)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := c.doRequestWithBackoff(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("module download returned status %d", resp.StatusCode)
	}

	downloadURL := resp.Header.Get("X-Terraform-Get")
	if downloadURL == "" {
		downloadURL = resp.Header.Get("Location")
	}

	if downloadURL != "" && strings.HasPrefix(downloadURL, "/") {
		downloadURL = c.buildBaseURL(host) + downloadURL
	}

	var bodyReader io.Reader = resp.Body
	if downloadURL != "" && (resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther) {
		if err := validateArchiveURL(downloadURL); err != nil {
			return err
		}
		dlReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
		dlResp, err := c.doRequestWithBackoff(dlReq)
		if err != nil {
			return fmt.Errorf("failed to fetch archive from %s: %w", downloadURL, err)
		}
		defer func() { _ = dlResp.Body.Close() }()
		if dlResp.StatusCode != http.StatusOK {
			return fmt.Errorf("archive fetch returned status %d", dlResp.StatusCode)
		}
		bodyReader = dlResp.Body
	}

	// Extract tar.gz into destDir
	return extractTarGz(bodyReader, destDir)
}

func (c *Client) buildBaseURL(host string) string {
	if c.BaseURL != "" && c.BaseURL != "https://registry.terraform.io" {
		return c.BaseURL
	}
	return "https://" + host
}

// validateArchiveURL rejects a download URL this client cannot fetch, naming the value the
// registry returned. The registry protocol allows X-Terraform-Get to carry any module source
// Terraform understands, including VCS sources; only archives can be downloaded here, so those
// must be reported as such instead of surfacing as an opaque URL parse error.
func validateArchiveURL(downloadURL string) error {
	const hint = "only http(s) archive URLs can be fetched; VCS sources in X-Terraform-Get are not supported"
	parsed, err := url.Parse(downloadURL)
	if err != nil {
		return fmt.Errorf("registry returned an unusable download URL %q (%v): %s", downloadURL, err, hint)
	}
	if parsed.Scheme == "http" || parsed.Scheme == "https" {
		return nil
	}
	return fmt.Errorf("registry returned an unsupported download URL %q: %s", downloadURL, hint)
}

// maxArchiveEntryBytes bounds a single extracted archive member. Exceeding it is reported as an
// error rather than silently truncating the module under analysis.
const maxArchiveEntryBytes = 10 * 1024 * 1024

func extractTarGz(r io.Reader, destDir string) error {
	gzReader, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("failed to initialize gzip reader: %w", err)
	}
	defer func() { _ = gzReader.Close() }()

	tarReader := tar.NewReader(gzReader)
	cleanDest := filepath.Clean(destDir)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar entry: %w", err)
		}

		target := filepath.Join(cleanDest, filepath.Clean(header.Name))
		if !strings.HasPrefix(target, cleanDest+string(filepath.Separator)) && target != cleanDest {
			return fmt.Errorf("illegal file path in tar archive: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0750); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
				return err
			}
			// #nosec G304 -- target is verified safe against zip-slip
			outFile, err := os.OpenFile(filepath.Clean(target), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
			if err != nil {
				return err
			}
			// Limit bytes read to prevent decompression bombs (G110). A short read must fail
			// loudly: silently writing a truncated module would make the analysis describe
			// different source than the one being analysed.
			written, err := io.Copy(outFile, io.LimitReader(tarReader, maxArchiveEntryBytes))
			if err != nil {
				_ = outFile.Close()
				return err
			}
			if written != header.Size {
				_ = outFile.Close()
				return fmt.Errorf("archive entry %s is %d bytes, which exceeds the %d byte extraction limit", header.Name, header.Size, maxArchiveEntryBytes)
			}
			_ = outFile.Close()
		}
	}

	return nil
}
