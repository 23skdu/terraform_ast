package lsp

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCoverage_DidClose(t *testing.T) {
	server := NewServer(&bytes.Buffer{})
	params, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": "file:///tmp/x.tf"}})
	server.HandleRequest(Request{JSONRPC: "2.0", Method: "textDocument/didClose", Params: params})
}
