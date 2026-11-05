package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestLSP_NotificationsAreNeverAnswered is the regression test for the JSON-RPC violation:
// the default branch replied with a -32601 error to every unhandled method, including
// notifications. A client streaming didChange or exit received replies it did not ask for.
func TestLSP_NotificationsAreNeverAnswered(t *testing.T) {
	server := NewServer(&bytes.Buffer{})

	for _, method := range []string{
		"textDocument/didChange",
		"exit",
		"$/cancelRequest",
		"$/setTrace",
		"textDocument/didClose",
		"initialized",
		"workspace/somethingUnknown",
		"totally/made/up",
	} {
		resp, _ := server.HandleRequest(Request{JSONRPC: "2.0", Method: method})
		assert.Nil(t, resp, "notification %q must not be answered", method)
	}

	// The same unknown method as a request is still answered with MethodNotFound.
	resp, _ := server.HandleRequest(Request{JSONRPC: "2.0", ID: 7, Method: "totally/made/up"})
	require.NotNil(t, resp)
	require.NotNil(t, resp.Error)
	assert.Equal(t, -32601, resp.Error.Code)
	assert.Equal(t, 7, resp.ID)
}

// TestLSP_DidChangeIsHandled covers roadmap #4: initialize advertises Full sync, which obliges
// the client to send didChange on every keystroke, but the method was unhandled.
func TestLSP_DidChangeIsHandled(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`variable "a" { type = string }`), 0600))
	uri := "file://" + filepath.Join(dir, "main.tf")

	server := NewServer(&bytes.Buffer{})
	params, err := json.Marshal(map[string]any{
		"textDocument":   map[string]string{"uri": uri, "version": "2"},
		"contentChanges": []map[string]string{{"text": "variable \"b\" {\n  type       = string\n  deprecated = \"use c\"\n}\n"}},
	})
	require.NoError(t, err)

	resp, notif := server.HandleRequest(Request{JSONRPC: "2.0", Method: "textDocument/didChange", Params: params})
	assert.Nil(t, resp, "didChange is a notification")
	require.NotNil(t, notif)
	assert.Equal(t, "textDocument/publishDiagnostics", notif.Method)

	published, ok := notif.Params.(PublishDiagnosticsParams)
	require.True(t, ok)
	assert.Equal(t, uri, published.URI)
	require.Len(t, published.Diagnostics, 1, "the unsaved buffer declares one deprecated variable")
	assert.Contains(t, published.Diagnostics[0].Message, "deprecated")
}

// TestLSP_DiagnosticsFollowTheBuffer proves diagnostics are computed from the in-editor text and
// not from the file on disk. Previously the server re-read the file, so a live editing session
// reported on the last saved state.
func TestLSP_DiagnosticsFollowTheBuffer(t *testing.T) {
	dir := t.TempDir()
	// On disk: nothing deprecated.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`variable "a" { type = string }`), 0600))
	uri := "file://" + filepath.Join(dir, "main.tf")
	server := NewServer(&bytes.Buffer{})

	// didSave with no buffer yet reads from disk: no diagnostics.
	_, notif := server.HandleRequest(Request{
		JSONRPC: "2.0", Method: "textDocument/didSave",
		Params: mustJSON(t, map[string]any{"textDocument": map[string]string{"uri": uri}}),
	})
	published := notif.Params.(PublishDiagnosticsParams)
	assert.Empty(t, published.Diagnostics)

	// The user then types a deprecated variable without saving.
	_, notif = server.HandleRequest(Request{
		JSONRPC: "2.0", Method: "textDocument/didChange",
		Params: mustJSON(t, map[string]any{
			"textDocument":   map[string]string{"uri": uri},
			"contentChanges": []map[string]string{{"text": "variable \"a\" {\n  type       = string\n  deprecated = \"gone soon\"\n}\n"}},
		}),
	})
	published = notif.Params.(PublishDiagnosticsParams)
	require.Len(t, published.Diagnostics, 1, "the buffer is deprecated even though disk is not")
	assert.Contains(t, published.Diagnostics[0].Message, "gone soon")

	// A later didSave still reports the buffer, because nothing changed on disk.
	_, notif = server.HandleRequest(Request{
		JSONRPC: "2.0", Method: "textDocument/didSave",
		Params: mustJSON(t, map[string]any{"textDocument": map[string]string{"uri": uri}}),
	})
	published = notif.Params.(PublishDiagnosticsParams)
	assert.Len(t, published.Diagnostics, 1)
}

// TestLSP_ExecuteCommandProviderIsAdvertised covers roadmap #51: codeActionProvider was
// advertised with no executeCommandProvider, so the advertised quick fixes could never run.
func TestLSP_ExecuteCommandProviderIsAdvertised(t *testing.T) {
	server := NewServer(&bytes.Buffer{})
	resp, _ := server.HandleRequest(Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	caps := resp.Result.(map[string]any)["capabilities"].(map[string]any)

	provider, ok := caps["executeCommandProvider"]
	require.True(t, ok, "quick fixes are unexecutable without executeCommandProvider")
	options, ok := provider.(*ExecuteCommandOptions)
	require.True(t, ok, "executeCommandProvider must be an object")
	commands := options.Commands
	assert.Contains(t, commands, CommandApplyMovedBlocks)
	assert.Contains(t, commands, CommandAddMissingInputs)

	// Every advertised command is actually handled.
	for _, command := range commands {
		resp, _ := server.HandleRequest(Request{
			JSONRPC: "2.0", ID: 2, Method: "workspace/executeCommand",
			Params: mustJSON(t, ExecuteCommandParams{Command: command}),
		})
		require.NotNil(t, resp)
		assert.NotContains(t, fmt.Sprint(resp.Error), "unknown command",
			"advertised command %q must be implemented", command)
	}
}

// TestLSP_ExecuteCommandApplyMovedBlocks exercises the previously unreachable
// codemod.WriteMovedBlocks through the advertised quick fix.
func TestLSP_ExecuteCommandApplyMovedBlocks(t *testing.T) {
	dir := t.TempDir()
	server := NewServer(&bytes.Buffer{})

	resp, _ := server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 1, Method: "workspace/executeCommand",
		Params: mustJSON(t, ExecuteCommandParams{
			Command:   CommandApplyMovedBlocks,
			Arguments: []json.RawMessage{mustJSON(t, ApplyMovedBlocksArgs{From: "aws_instance.old", To: "aws_instance.new", Dir: dir})},
		}),
	})
	require.NotNil(t, resp)
	require.Nil(t, resp.Error, "command must succeed: %v", resp.Error)

	written, err := os.ReadFile(filepath.Join(dir, "moved.tf"))
	require.NoError(t, err, "the command must write moved.tf")
	assert.Contains(t, string(written), "moved {")
	assert.Contains(t, string(written), "aws_instance.old")
	assert.Contains(t, string(written), "aws_instance.new")

	// Missing arguments are a caller error, reported as an error rather than a panic.
	resp, _ = server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 2, Method: "workspace/executeCommand",
		Params: mustJSON(t, ExecuteCommandParams{Command: CommandApplyMovedBlocks}),
	})
	require.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "object argument")

	// An empty object is a different, equally explicit error.
	resp, _ = server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 3, Method: "workspace/executeCommand",
		Params: mustJSON(t, ExecuteCommandParams{
			Command:   CommandApplyMovedBlocks,
			Arguments: []json.RawMessage{mustJSON(t, ApplyMovedBlocksArgs{})},
		}),
	})
	require.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "non-empty 'from' and 'to'")
}

// TestLSP_ExecuteCommandAddMissingInputs exercises the previously unreachable
// codemod.AddMissingInputs through the advertised quick fix.
func TestLSP_ExecuteCommandAddMissingInputs(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`module "gke" {
  source = "./upstream"
}
`), 0600))
	server := NewServer(&bytes.Buffer{})

	resp, _ := server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 1, Method: "workspace/executeCommand",
		Params: mustJSON(t, ExecuteCommandParams{
			Command:   CommandAddMissingInputs,
			Arguments: []json.RawMessage{mustJSON(t, AddMissingInputsArgs{Module: "gke", Dir: dir, Inputs: []string{"project_id"}})},
		}),
	})
	require.NotNil(t, resp)
	require.Nil(t, resp.Error, "command must succeed: %v", resp.Error)

	updated, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	require.NoError(t, err)
	assert.Contains(t, string(updated), `project_id`)

	// A call with no module name is rejected.
	resp, _ = server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 2, Method: "workspace/executeCommand",
		Params: mustJSON(t, ExecuteCommandParams{
			Command:   CommandAddMissingInputs,
			Arguments: []json.RawMessage{mustJSON(t, AddMissingInputsArgs{Inputs: []string{"a"}})},
		}),
	})
	require.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "non-empty module name")

	// A call naming a module but no inputs is rejected rather than writing an empty change.
	resp, _ = server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 3, Method: "workspace/executeCommand",
		Params: mustJSON(t, ExecuteCommandParams{
			Command:   CommandAddMissingInputs,
			Arguments: []json.RawMessage{mustJSON(t, AddMissingInputsArgs{Module: "gke", Dir: dir})},
		}),
	})
	require.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "at least one input name")
}

// TestLSP_ExecuteCommandErrors covers the error paths that keep the daemon from crashing on a
// malformed or unknown command.
func TestLSP_ExecuteCommandErrors(t *testing.T) {
	server := NewServer(&bytes.Buffer{})

	// Unknown command.
	resp, _ := server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 1, Method: "workspace/executeCommand",
		Params: mustJSON(t, ExecuteCommandParams{Command: "tfastdiff.nope"}),
	})
	require.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "unknown command")

	// Malformed params.
	resp, _ = server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 2, Method: "workspace/executeCommand",
		Params: json.RawMessage(`"not-an-object"`),
	})
	require.NotNil(t, resp.Error)
	assert.Equal(t, -32602, resp.Error.Code)

	// A positional argument list is rejected: the contract is a single object, so an older
	// shape cannot silently write the wrong file.
	resp, _ = server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 3, Method: "workspace/executeCommand",
		Params: mustJSON(t, ExecuteCommandParams{
			Command:   CommandApplyMovedBlocks,
			Arguments: []json.RawMessage{json.RawMessage(`"a.b"`)},
		}),
	})
	require.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "object argument")

	// No arguments at all is also a caller error rather than a panic.
	resp, _ = server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 4, Method: "workspace/executeCommand",
		Params: mustJSON(t, ExecuteCommandParams{Command: CommandAddMissingInputs}),
	})
	require.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "object argument")

	// Empty input names are filtered rather than written as empty attributes.
	resp, _ = server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 5, Method: "workspace/executeCommand",
		Params: mustJSON(t, ExecuteCommandParams{
			Command:   CommandAddMissingInputs,
			Arguments: []json.RawMessage{mustJSON(t, AddMissingInputsArgs{Module: "gke", Inputs: []string{"", "   "}})},
		}),
	})
	require.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "at least one input name")
}

// TestLSP_ExitAndShutdownAreHandled covers roadmap #4: the daemon only ever stopped on stdin EOF,
// so a client following the LSP lifecycle waited for the editor to close the pipe.
func TestLSP_ExitAndShutdownAreHandled(t *testing.T) {
	var buf bytes.Buffer
	server := NewServer(&buf)

	// shutdown as a request replies and records the state.
	resp, _ := server.HandleRequest(Request{JSONRPC: "2.0", ID: 1, Method: "shutdown"})
	require.NotNil(t, resp)
	assert.True(t, server.ShuttingDown.Load())

	// exit terminates Serve even though the reader still has data available.
	var out bytes.Buffer
	daemon := NewServer(&out)
	stream := strings.NewReader(
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`) +
			frame(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`) +
			frame(`{"jsonrpc":"2.0","method":"exit"}`) +
			// Trailing content that must never be processed once exit is seen.
			frame(`{"jsonrpc":"2.0","id":3,"method":"initialize"}`))
	require.NoError(t, daemon.Serve(stream))
	assert.NotContains(t, out.String(), `"id":3`, "messages after exit must not be handled")
}

// TestLSP_CodeActionNotificationIsNotAnswered keeps the request/notification distinction applied
// to the quick-fix endpoints too.
func TestLSP_CodeActionNotificationIsNotAnswered(t *testing.T) {
	server := NewServer(&bytes.Buffer{})

	resp, _ := server.HandleRequest(Request{JSONRPC: "2.0", Method: "initialize"})
	assert.Nil(t, resp)
	resp, _ = server.HandleRequest(Request{JSONRPC: "2.0", Method: "shutdown"})
	assert.Nil(t, resp)
	resp, _ = server.HandleRequest(Request{JSONRPC: "2.0", Method: "textDocument/codeAction"})
	assert.Nil(t, resp)
	resp, _ = server.HandleRequest(Request{JSONRPC: "2.0", Method: "workspace/executeCommand"})
	assert.Nil(t, resp)
}

// TestLSP_ServerInfoVersionIsUnquoted guards the version string: the LSP client displays
// serverInfo.version verbatim, so a v prefix showed up in the UI. It was also stale at v0.8.0
// while the documented release line is v0.9.0.
func TestLSP_ServerInfoVersionIsUnquoted(t *testing.T) {
	server := NewServer(&bytes.Buffer{})
	resp, _ := server.HandleRequest(Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	info := resp.Result.(map[string]any)["serverInfo"].(map[string]any)
	version, _ := info["version"].(string)
	assert.NotEmpty(t, version)
	assert.NotContains(t, version, "v", "the LSP client shows the version verbatim: %q", version)
	assert.Equal(t, serverVersion, version)
}

// TestLSP_DiagnosticsLineIsNeverNegative covers the invalid position the old code produced when a
// variable had no recorded source line.
func TestLSP_DiagnosticsLineIsNeverNegative(t *testing.T) {
	// A module whose variables carry no usable position is hard to construct through HCL, so
	// clampLine is exercised directly, which is where the arithmetic lived.
	assert.Equal(t, 0, clampLine(0))
	assert.Equal(t, 0, clampLine(-5))
	assert.Equal(t, 0, clampLine(1))
	assert.Equal(t, 9, clampLine(10))
}

// TestLSP_UnparsableBufferFallsBackToDisk keeps a half-typed file from turning into an error
// diagnostic on every keystroke.
func TestLSP_UnparsableBufferFallsBackToDisk(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`variable "a" { type = string }`), 0600))
	uri := "file://" + filepath.Join(dir, "main.tf")
	server := NewServer(&bytes.Buffer{})

	_, notif := server.HandleRequest(Request{
		JSONRPC: "2.0", Method: "textDocument/didChange",
		Params: mustJSON(t, map[string]any{
			"textDocument":   map[string]string{"uri": uri},
			"contentChanges": []map[string]string{{"text": "variable \"a\" { type = "}},
		}),
	})
	published := notif.Params.(PublishDiagnosticsParams)
	assert.Empty(t, published.Diagnostics,
		"an incomplete buffer must fall back to the last good state, not error on every keystroke")
}

// TestLSP_DidChangeFallsBackToTextDocument covers a client that sends the document text inline
// rather than in contentChanges.
func TestLSP_DidChangeFallsBackToTextDocument(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`variable "a" { type = string }`), 0600))
	uri := "file://" + filepath.Join(dir, "main.tf")
	server := NewServer(&bytes.Buffer{})

	_, notif := server.HandleRequest(Request{
		JSONRPC: "2.0", Method: "textDocument/didChange",
		Params: mustJSON(t, map[string]any{
			"textDocument": map[string]string{
				"uri":  uri,
				"text": "variable \"a\" {\n  type       = string\n  deprecated = \"legacy\"\n}\n",
			},
		}),
	})
	published := notif.Params.(PublishDiagnosticsParams)
	require.Len(t, published.Diagnostics, 1)
	assert.Contains(t, published.Diagnostics[0].Message, "legacy")
}

// TestLSP_InvalidDocumentParamsAreIgnored confirms malformed payloads do not panic or answer.
func TestLSP_InvalidDocumentParamsAreIgnored(t *testing.T) {
	server := NewServer(&bytes.Buffer{})

	resp, notif := server.HandleRequest(Request{JSONRPC: "2.0", Method: "textDocument/didSave", Params: json.RawMessage(`"bad"`)})
	assert.Nil(t, resp)
	assert.Nil(t, notif)

	resp, notif = server.HandleRequest(Request{
		JSONRPC: "2.0", Method: "textDocument/didSave",
		Params: mustJSON(t, map[string]any{"textDocument": map[string]string{"uri": ""}}),
	})
	assert.Nil(t, resp)
	assert.Nil(t, notif)
}

// frame builds one LSP base-protocol frame.
func frame(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(v)
	require.NoError(t, err)
	return encoded
}

// TestLSP_ExecuteCommandDefaultsToWorkingDirectory covers the two default-to-"." branches, which
// a client that omits an explicit directory will hit.
func TestLSP_ExecuteCommandDefaultsToWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	server := NewServer(&bytes.Buffer{})

	// applyMovedBlocks with no directory writes moved.tf into the working directory.
	resp, _ := server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 1, Method: "workspace/executeCommand",
		Params: mustJSON(t, ExecuteCommandParams{
			Command:   CommandApplyMovedBlocks,
			Arguments: []json.RawMessage{mustJSON(t, ApplyMovedBlocksArgs{From: "a.b", To: "c.d"})},
		}),
	})
	require.Nil(t, resp.Error, "%v", resp.Error)
	written, err := os.ReadFile(filepath.Join(dir, "moved.tf"))
	require.NoError(t, err)
	assert.Contains(t, string(written), "a.b")

	// addMissingInputs with no directory edits the working directory's module block.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte("module \"m\" {\n  source = \"./up\"\n}\n"), 0600))
	resp, _ = server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 2, Method: "workspace/executeCommand",
		Params: mustJSON(t, ExecuteCommandParams{
			Command:   CommandAddMissingInputs,
			Arguments: []json.RawMessage{mustJSON(t, AddMissingInputsArgs{Module: "m", Inputs: []string{"region"}})},
		}),
	})
	require.Nil(t, resp.Error, "%v", resp.Error)
	updated, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	require.NoError(t, err)
	assert.Contains(t, string(updated), "region")
}

// TestLSP_ExecuteCommandWriteFailuresAreReported covers the two filesystem error paths: the
// failure must surface as an RPC error rather than being swallowed or panicking.
func TestLSP_ExecuteCommandWriteFailuresAreReported(t *testing.T) {
	server := NewServer(&bytes.Buffer{})

	// A read-only working directory makes both writes fail.
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	require.NoError(t, os.Mkdir(blocked, 0o500))
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o700) })

	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}

	resp, _ := server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 1, Method: "workspace/executeCommand",
		Params: mustJSON(t, ExecuteCommandParams{
			Command:   CommandApplyMovedBlocks,
			Arguments: []json.RawMessage{mustJSON(t, ApplyMovedBlocksArgs{From: "a.b", To: "c.d", Dir: blocked})},
		}),
	})
	require.NotNil(t, resp.Error, "an unwritable directory must be reported")
	assert.NotEmpty(t, resp.Error.Message)

	// A directory that does not exist makes addMissingInputs fail to read it.
	resp, _ = server.HandleRequest(Request{
		JSONRPC: "2.0", ID: 2, Method: "workspace/executeCommand",
		Params: mustJSON(t, ExecuteCommandParams{
			Command:   CommandAddMissingInputs,
			Arguments: []json.RawMessage{mustJSON(t, AddMissingInputsArgs{Module: "m", Inputs: []string{"region"}, Dir: filepath.Join(dir, "absent")})},
		}),
	})
	require.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "read wrapper directory")
}

// TestLSP_OversizedContentLengthIsRejected is the regression test for a denial-of-service found by
// FuzzLSPServe: an attacker-controlled Content-Length of 1e14 reached make([]byte, n) and
// attempted a 100 TB allocation, hanging the daemon.
func TestLSP_OversizedContentLengthIsRejected(t *testing.T) {
	for _, length := range []string{
		"100000000000000",
		"99999999999999999999999",
		"9223372036854775807", // max int64 on 64-bit
		"-1",
	} {
		done := make(chan struct{})
		go func(length string) {
			defer close(done)
			server := NewServer(&bytes.Buffer{})
			// With no valid length the daemon ignores the header and keeps reading, so the
			// trailing newline makes Serve return instead of blocking forever.
			input := "Content-Length:" + length + "\r\n\r\n" +
				frame(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
			_ = server.Serve(strings.NewReader(input))
		}(length)

		select {
		case <-done:
			// Returned promptly: the length was rejected, not allocated.
		case <-time.After(5 * time.Second):
			t.Fatalf("Content-Length %s was not rejected; Serve hung trying to allocate", length)
		}
	}
}

// TestLSP_ContentLengthAtTheLimitIsAccepted confirms the ceiling is not so low that a legitimate
// message is dropped.
func TestLSP_ContentLengthAtTheLimitIsAccepted(t *testing.T) {
	var out bytes.Buffer
	server := NewServer(&out)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"pad":""}}`
	require.NoError(t, server.Serve(strings.NewReader(frame(body))))
	assert.Contains(t, out.String(), `"id":1`)
	assert.Equal(t, 64<<20, maxMessageBytes)
}

func TestLSPServer_ExtendedDiagnosticsAndCodeActions(t *testing.T) {
	server := NewServer(&bytes.Buffer{})

	// 1. Direct codeActionsFor(empty) call
	ca := server.codeActionsFor(CodeActionParams{})
	require.Len(t, ca, 2)
	assert.Equal(t, ".", ca[0].Command.Arguments[0].(ApplyMovedBlocksArgs).Dir)

	// 2. codeActionsFor with invalid file path
	caBad := server.codeActionsFor(CodeActionParams{
		TextDocument: struct {
			URI string `json:"uri"`
		}{URI: "file:///does/not/exist/main.tf"},
	})
	require.Len(t, caBad, 2)
	assert.Equal(t, "/does/not/exist", caBad[0].Command.Arguments[0].(ApplyMovedBlocksArgs).Dir)

	// 3. Single module call pre-populates targetMod even if cursor is on different line
	dirSingle := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dirSingle, "main.tf"), []byte(`
module "cluster" {
  source = "./cluster"
}
`), 0600))
	caSingle := server.codeActionsFor(CodeActionParams{
		TextDocument: struct {
			URI string `json:"uri"`
		}{URI: "file://" + filepath.Join(dirSingle, "main.tf")},
		Range: Range{Start: Position{Line: 10, Character: 0}},
	})
	require.Len(t, caSingle, 2)
	assert.Equal(t, "cluster", caSingle[1].Command.Arguments[0].(AddMissingInputsArgs).Module)

	// 4. Extended diagnostics: missing source, unpinned registry, and removed block destroy
	dirExt := t.TempDir()
	extTF := filepath.Join(dirExt, "main.tf")
	require.NoError(t, os.WriteFile(extTF, []byte(`
module "broken" {
}

module "unpinned" {
  source = "hashicorp/consul/aws"
}

removed {
  from = aws_s3_bucket.old
  lifecycle {
    destroy = true
  }
}
`), 0600))

	diags := server.computeDiagnostics("file://" + extTF)
	require.Len(t, diags, 3)

	var hasMissingSource, hasUnpinned, hasDestroy bool
	for _, d := range diags {
		if d.Severity == 1 && strings.Contains(d.Message, "missing a source attribute") {
			hasMissingSource = true
		}
		if d.Severity == 2 && strings.Contains(d.Message, "unpinned registry source") {
			hasUnpinned = true
		}
		if d.Severity == 2 && strings.Contains(d.Message, "destroy = true") {
			hasDestroy = true
		}
	}
	assert.True(t, hasMissingSource, "expected missing source diagnostic")
	assert.True(t, hasUnpinned, "expected unpinned registry source diagnostic")
	assert.True(t, hasDestroy, "expected removed block destroy diagnostic")

	// 5. Cursor on unpinned module line targets "unpinned" module
	caExt := server.codeActionsFor(CodeActionParams{
		TextDocument: struct {
			URI string `json:"uri"`
		}{URI: "file://" + extTF},
		Range: Range{Start: Position{Line: 4, Character: 0}},
	})
	require.Len(t, caExt, 2)
	assert.Equal(t, "unpinned", caExt[1].Command.Arguments[0].(AddMissingInputsArgs).Module)
}

func TestLSP_DidChangeDebounceAndOverlayCache(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.tf")
	require.NoError(t, os.WriteFile(mainPath, []byte(`variable "a" { type = string }`), 0o600))
	uri := "file://" + mainPath

	server := NewServer(&bytes.Buffer{})
	resp, _ := server.HandleRequest(Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  mustJSON(t, map[string]any{"initializationOptions": map[string]any{"debounceMs": 500}}),
	})
	require.NotNil(t, resp)
	assert.Equal(t, 500*time.Millisecond, server.DebounceWindow)

	// Burst of 100 didChange notifications for the same document.
	for i := 0; i < 100; i++ {
		text := fmt.Sprintf("variable \"a_%d\" {\n  type = string\n  deprecated = \"dep-%d\"\n}\n", i, i)
		_, _ = server.HandleRequest(Request{
			JSONRPC: "2.0",
			Method:  "textDocument/didChange",
			Params: mustJSON(t, map[string]any{
				"textDocument":   map[string]string{"uri": uri},
				"contentChanges": []map[string]string{{"text": text}},
			}),
		})
	}
	assert.LessOrEqual(t, server.Inspections.Load(), int64(5), "burst of 100 didChange notifications must result in <= 5 inspections")

	// Saving flushes and inspects the final buffer revision.
	_, notif := server.HandleRequest(Request{
		JSONRPC: "2.0",
		Method:  "textDocument/didSave",
		Params:  mustJSON(t, map[string]any{"textDocument": map[string]string{"uri": uri}}),
	})
	require.NotNil(t, notif)
	published := notif.Params.(PublishDiagnosticsParams)
	require.Len(t, published.Diagnostics, 1)
	assert.Contains(t, published.Diagnostics[0].Message, "dep-99")

	// Re-inspecting with unchanged buffer text hits the overlay AST cache without a new inspection.
	before := server.Inspections.Load()
	_ = server.codeActionsFor(CodeActionParams{
		TextDocument: struct {
			URI string `json:"uri"`
		}{URI: uri},
	})
	assert.Equal(t, before, server.Inspections.Load())
}
