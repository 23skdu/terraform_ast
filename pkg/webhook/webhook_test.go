package webhook

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotifySuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := Notify(server.URL, Payload{ModuleName: "m", TotalBreaking: 1, Summary: "s"})
	assert.NoError(t, err)
}

func TestNotifyNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	err := Notify(server.URL, Payload{})
	assert.ErrorContains(t, err, "returned status 500")
}

func TestClientNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var c *Client
	err := c.Notify(server.URL, Payload{})
	assert.NoError(t, err)
}

func TestClientNotifyEncodeError(t *testing.T) {
	// Payload is always encodable; instead test HTTP client error via bad URL.
	c := NewClient()
	err := c.Notify("://bad", Payload{})
	assert.Error(t, err)
}

func TestClientNilHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := &Client{}
	err := c.Notify(server.URL, Payload{})
	assert.NoError(t, err)
}
func TestNotifyClientHTTPError(t *testing.T) {
	c := &Client{HTTPClient: &http.Client{}}
	err := c.Notify("http://127.0.0.1:1", Payload{})
	assert.Error(t, err)
}

func TestConfigurableTimeout(t *testing.T) {
	c := NewClientWithTimeout(3 * time.Second)
	require.NotNil(t, c.HTTPClient)
	assert.Equal(t, 3*time.Second, c.HTTPClient.Timeout)
	assert.Equal(t, 3*time.Second, c.Timeout)

	// Non-positive values fall back to the default instead of disabling the deadline.
	assert.Equal(t, defaultTimeout, NewClientWithTimeout(0).Timeout)

	// A client with only a Timeout still applies it when no HTTPClient is supplied.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	partial := &Client{Timeout: 2 * time.Second}
	require.NoError(t, partial.Notify(server.URL, Payload{}))
	require.NotNil(t, partial.HTTPClient)
	assert.Equal(t, 2*time.Second, partial.HTTPClient.Timeout)

	SetDefaultTimeout(4 * time.Second)
	assert.Equal(t, 4*time.Second, defaultClient.Timeout)
	require.NoError(t, Notify(server.URL, Payload{}))
}
