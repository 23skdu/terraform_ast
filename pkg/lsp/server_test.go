package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failWriter struct{}

func (f *failWriter) Write(p []byte) (n int, err error) {
	return 0, fmt.Errorf("write error")
}

func TestLSPServer_InitializeAndShutdown(t *testing.T) {
	var buf bytes.Buffer
	server := NewServer(&buf)

	// 1. Initialize
	initReq := Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	}
	resp, notif := server.HandleRequest(initReq)
	assert.Nil(t, notif)
	require.NotNil(t, resp)
	assert.Equal(t, 1, resp.ID)
	assert.Nil(t, resp.Error)

	capsMap, ok := resp.Result.(map[string]interface{})
	require.True(t, ok)
	assert.NotNil(t, capsMap["capabilities"])

	// 2. Initialized (notification)
	respInit, notifInit := server.HandleRequest(Request{
		JSONRPC: "2.0",
		Method:  "initialized",
	})
	assert.Nil(t, respInit)
	assert.Nil(t, notifInit)

	// 3. Shutdown
	respShutdown, _ := server.HandleRequest(Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "shutdown",
	})
	require.NotNil(t, respShutdown)
	assert.Equal(t, 2, respShutdown.ID)
}

func TestLSPServer_DiagnosticsAndCodeActions(t *testing.T) {
	dir := t.TempDir()
	tfFile := filepath.Join(dir, "main.tf")
	tfContent := `
variable "old_token" {
  type        = string
  description = "Deprecated token"
  deprecated  = "Use new_token instead"
}
`
	require.NoError(t, os.WriteFile(tfFile, []byte(tfContent), 0600))

	var buf bytes.Buffer
	server := NewServer(&buf)

	// DidSave notification triggers diagnostics
	saveParams, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]string{
			"uri": "file://" + tfFile,
		},
	})
	resp, notif := server.HandleRequest(Request{
		JSONRPC: "2.0",
		Method:  "textDocument/didSave",
		Params:  saveParams,
	})
	assert.Nil(t, resp)
	require.NotNil(t, notif)
	assert.Equal(t, "textDocument/publishDiagnostics", notif.Method)

	diagParams, ok := notif.Params.(PublishDiagnosticsParams)
	require.True(t, ok)
	require.Len(t, diagParams.Diagnostics, 1)
	assert.Equal(t, 2, diagParams.Diagnostics[0].Severity) // Warning
	assert.Contains(t, diagParams.Diagnostics[0].Message, "Use new_token instead")

	// DidSave with invalid params
	respBad, notifBad := server.HandleRequest(Request{
		JSONRPC: "2.0",
		Method:  "textDocument/didSave",
		Params:  []byte(`{`),
	})
	assert.Nil(t, respBad)
	assert.Nil(t, notifBad)

	// CodeAction request
	actionParams, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]string{
			"uri": "file://" + tfFile,
		},
		"range": Range{
			Start: Position{Line: 1, Character: 0},
			End:   Position{Line: 1, Character: 10},
		},
	})
	respAction, _ := server.HandleRequest(Request{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "textDocument/codeAction",
		Params:  actionParams,
	})
	require.NotNil(t, respAction)
	actions, ok := respAction.Result.([]CodeAction)
	require.True(t, ok)
	require.Len(t, actions, 2)
	assert.Equal(t, "quickfix", actions[0].Kind)

	// Unknown method returns -32601
	respUnknown, _ := server.HandleRequest(Request{
		JSONRPC: "2.0",
		ID:      99,
		Method:  "unknown/method",
	})
	require.NotNil(t, respUnknown)
	require.NotNil(t, respUnknown.Error)
	assert.Equal(t, -32601, respUnknown.Error.Code)
}

func TestLSPServer_ComputeDiagnosticsError(t *testing.T) {
	server := NewServer(&bytes.Buffer{})
	// Non-existent path causes inspection error
	diags := server.computeDiagnostics("file:///non/existent/path/file.tf")
	require.Len(t, diags, 1)
	assert.Equal(t, 1, diags[0].Severity)
	assert.Contains(t, diags[0].Message, "Failed to inspect module AST")
}

func TestLSPServer_ServeEventLoop(t *testing.T) {
	var out bytes.Buffer
	server := NewServer(&out)

	rawReq := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	framedReq := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(rawReq), rawReq)

	saveReq := `{"jsonrpc":"2.0","method":"textDocument/didSave","params":{"textDocument":{"uri":"file:///dev/null"}}}`
	framedSave := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(saveReq), saveReq)

	// Valid request + notification + empty line frame + unparseable json + EOF
	malformedReq := "Content-Length: 4\r\n\r\n{bad"
	inputData := framedReq + "\r\n" + framedSave + "\r\n" + malformedReq

	err := server.Serve(strings.NewReader(inputData))
	require.NoError(t, err)

	assert.Contains(t, out.String(), "Content-Length:")
	assert.Contains(t, out.String(), "tfastdiff-lsp")
}

func TestLSPServer_SendErrors(t *testing.T) {
	server := NewServer(&failWriter{})
	err := server.send(map[string]string{"key": "val"})
	assert.Error(t, err)

	unserializable := map[string]interface{}{
		"bad": make(chan int),
	}
	errBadMarshal := server.send(unserializable)
	assert.Error(t, errBadMarshal)
}

type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("read error")
}

func TestLSPServer_ServeErrorsAndDaemon(t *testing.T) {
	server := NewServer(&bytes.Buffer{})

	// Truncated body error
	errTruncated := server.Serve(strings.NewReader("Content-Length: 100\r\n\r\nshort"))
	assert.Error(t, errTruncated)

	// Reader error during header read
	errReader := server.Serve(&errorReader{})
	assert.Error(t, errReader)

	// RunDaemon with temporary stream
	origIn, origOut := defaultStdin, defaultStdout
	defer func() {
		defaultStdin, defaultStdout = origIn, origOut
	}()
	defaultStdin = strings.NewReader("Content-Length: 45\r\n\r\n{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}")
	defaultStdout = &bytes.Buffer{}

	errDaemon := RunDaemon()
	require.NoError(t, errDaemon)
}
