// Package webhook sends HTTP notifications for hazardous diffs.
package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Payload is the JSON body sent to a webhook endpoint.
type Payload struct {
	ModuleName    string `json:"module_name"`
	TotalBreaking int    `json:"total_breaking"`
	Summary       string `json:"summary"`
}

// Client sends webhook notifications.
type Client struct {
	HTTPClient *http.Client
	Timeout    time.Duration
}

const defaultTimeout = 10 * time.Second

// NewClient creates a webhook client with the default timeout.
func NewClient() *Client {
	return NewClientWithTimeout(defaultTimeout)
}

// NewClientWithTimeout creates a webhook client with a caller supplied timeout.
func NewClientWithTimeout(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{HTTPClient: &http.Client{Timeout: timeout}, Timeout: timeout}
}

// Notify sends payload as JSON to url. It returns an error on non-2xx responses.
func (c *Client) Notify(url string, payload Payload) error {
	if c == nil {
		c = NewClient()
	}
	if c.HTTPClient == nil {
		timeout := c.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		c.HTTPClient = &http.Client{Timeout: timeout}
	}
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook %s returned status %d", url, resp.StatusCode)
	}
	return nil
}

var defaultClient = NewClient()

// SetDefaultTimeout reconfigures the package-level client used by Notify. It is
// called by the CLI so --webhook-timeout actually controls request deadlines.
func SetDefaultTimeout(timeout time.Duration) {
	defaultClient = NewClientWithTimeout(timeout)
}

// Notify is a package-level convenience using a default client.
func Notify(url string, payload Payload) error {
	return defaultClient.Notify(url, payload)
}
