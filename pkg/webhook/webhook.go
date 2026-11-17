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

// NewClient creates a webhook client.
func NewClient() *Client {
	return &Client{HTTPClient: &http.Client{Timeout: 10 * time.Second}, Timeout: 10 * time.Second}
}

// Notify sends payload as JSON to url. It returns an error on non-2xx responses.
func (c *Client) Notify(url string, payload Payload) error {
	if c == nil {
		c = NewClient()
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 10 * time.Second}
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

// Notify is a package-level convenience using a default client.
func Notify(url string, payload Payload) error {
	return NewClient().Notify(url, payload)
}
