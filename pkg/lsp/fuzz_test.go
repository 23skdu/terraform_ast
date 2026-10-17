package lsp

import (
	"bytes"
	"strings"
	"testing"
)

func FuzzLSPServe(f *testing.F) {
	seeds := []string{
		"Content-Length: 45\r\n\r\n{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}",
		"Content-Length: 35\r\n\r\n{\"jsonrpc\":\"2.0\",\"method\":\"shutdown\"}",
		"Content-Length: 0\r\n\r\n",
		"invalid framing\r\n\r\n",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		server := NewServer(&bytes.Buffer{})
		// Invariant: Serve must never panic on arbitrary input stream
		_ = server.Serve(strings.NewReader(input))
	})
}
