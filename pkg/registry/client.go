package registry

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	return &Client{
		HTTPClient: httpClient,
		BaseURL:    "https://registry.terraform.io",
	}
}

// IsRegistrySource determines whether a module source string is a Terraform Registry identifier.
func IsRegistrySource(source string) bool {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, ".") ||
		strings.HasPrefix(trimmed, "/") ||
		strings.HasPrefix(trimmed, "~") ||
		strings.HasPrefix(trimmed, "git::") ||
		strings.HasPrefix(trimmed, "git@") ||
		strings.HasPrefix(trimmed, "http://") ||
		strings.HasPrefix(trimmed, "https://") ||
		strings.HasSuffix(trimmed, ".git") {
		return false
	}
	// A source that resolves to a directory on disk is always a local module: a bare relative
	// path such as "modules/gke/v30" has the same shape as a registry address and must not be
	// mistaken for one.
	if fi, err := os.Stat(trimmed); err == nil && fi.IsDir() {
		return false
	}
	parts := strings.Split(trimmed, "/")
	// Standard 3-part registry format: namespace/name/provider
	// Or hostname/namespace/name/provider (4 parts)
	if len(parts) != 3 && len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	return true
}

// ParseRegistrySource parses a registry source into hostname, namespace, name, and provider.
// It accepts exactly the shapes IsRegistrySource recognises: namespace/name/provider, optionally
// prefixed with a hostname. Empty components are rejected so a malformed address can never be
// turned into a request against a partially empty registry path.
func ParseRegistrySource(source string) (host, namespace, name, provider string, err error) {
	trimmed := strings.TrimSpace(source)
	parts := strings.Split(trimmed, "/")
	if len(parts) != 3 && len(parts) != 4 {
		return "", "", "", "", fmt.Errorf("invalid registry module source format: %s", source)
	}
	for _, part := range parts {
		if part == "" {
			return "", "", "", "", fmt.Errorf("invalid registry module source format: %s", source)
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
		dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
		if err != nil {
			return fmt.Errorf("failed to create archive fetch request: %w", err)
		}
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
			// Limit bytes read to prevent decompression bombs (G110)
			if _, err := io.Copy(outFile, io.LimitReader(tarReader, 10*1024*1024)); err != nil {
				_ = outFile.Close()
				return err
			}
			_ = outFile.Close()
		}
	}

	return nil
}
