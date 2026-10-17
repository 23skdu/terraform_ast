package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/codemod"
)

// Request represents a JSON-RPC 2.0 request or notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response represents a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

// Notification represents a server-to-client JSON-RPC notification.
type Notification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// RPCError is a standard JSON-RPC error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Position in a text document.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range in a text document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Diagnostic reported to the client.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"` // 1: Error, 2: Warning, 3: Info, 4: Hint
	Source   string `json:"source"`
	Message  string `json:"message"`
}

// PublishDiagnosticsParams notification payload.
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// DidSaveTextDocumentParams payload.
type DidSaveTextDocumentParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

// TextDocumentContentChangeEvent is one content revision under Full document sync.
type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

// DidChangeTextDocumentParams is the textDocument/didChange payload. Under Full
// sync the client sends the complete document, so the last change wins.
type DidChangeTextDocumentParams struct {
	TextDocument struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// Last returns the most recent full-document revision, if any. Under Full sync the client sends
// the entire document, so the final change supersedes every earlier one.
func (p *DidChangeTextDocumentParams) Last() *string {
	if len(p.ContentChanges) == 0 {
		if p.TextDocument.Text != "" {
			return &p.TextDocument.Text
		}
		return nil
	}
	text := p.ContentChanges[len(p.ContentChanges)-1].Text
	return &text
}

// ExecuteCommandOptions advertises which commands workspace/executeCommand accepts.
type ExecuteCommandOptions struct {
	Commands []string `json:"commands"`
}

// ExecuteCommandParams is the workspace/executeCommand payload.
type ExecuteCommandParams struct {
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments"`
}

// CodeActionParams payload.
type CodeActionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Range Range `json:"range"`
}

// CodeAction quick-fix action.
type CodeAction struct {
	Title   string   `json:"title"`
	Kind    string   `json:"kind"`
	Command *Command `json:"command,omitempty"`
}

// Command payload.
type Command struct {
	Title     string        `json:"title"`
	Command   string        `json:"command"`
	Arguments []interface{} `json:"arguments,omitempty"`
}

// Command identifiers registered with the client at initialize.
const (
	CommandApplyMovedBlocks = "tfastdiff.applyMovedBlocks"
	CommandAddMissingInputs = "tfastdiff.addMissingInputs"
)

// serverVersion is reported to the client as serverInfo.version. It is a compile-time
// constant because the daemon has no build-stamped version plumbed through yet (roadmap #24);
// it must not be quoted with a v prefix, which the LSP client displays verbatim.
const serverVersion = "0.9.0"

// buffer is the latest in-editor content for an open document.
type buffer struct {
	text string
}

// Server is a Language Server Protocol daemon for terraform_ast.
type Server struct {
	Writer io.Writer

	// buffers holds the most recent document text per absolute file path, keyed by the
	// document path with the file:// scheme stripped. Diagnostics are computed from this
	// buffer when present so they describe what the user is looking at rather than what
	// was last written to disk.
	buffers sync.Map

	// ShuttingDown records that shutdown or exit has been received, so Serve can stop
	// reading. Without it the daemon only ever terminated on stdin EOF, and the LSP
	// shutdown/exit sequence left it waiting.
	ShuttingDown atomic.Bool
}

// NewServer creates a new LSP server instance.
func NewServer(writer io.Writer) *Server {
	return &Server{Writer: writer}
}

// HandleRequest processes an incoming JSON-RPC message.
//
// A notification carries no id and, per JSON-RPC 2.0, must never be answered. The
// default branch used to reply with a -32601 error regardless, so a client
// streaming textDocument/didChange or exit received responses it did not expect.
// Unknown methods are only answered when they actually came in as a request.
func (s *Server) HandleRequest(req Request) (*Response, *Notification) {
	// Only a request has an id. Anything without one is a notification: handle it, but never
	// emit a Response, even for an unknown method.
	isRequest := req.ID != nil

	switch req.Method {
	case "initialize":
		caps := map[string]interface{}{
			"capabilities": map[string]interface{}{
				// Full sync: the client sends the whole document on open and change, so
				// diagnostics can be computed from the buffer rather than from disk.
				"textDocumentSync": 1,
				// Required alongside codeActionProvider. Advertising quick fixes without an
				// executeCommandProvider makes them permanently unexecutable.
				"codeActionProvider": true,
				"executeCommandProvider": &ExecuteCommandOptions{
					Commands: []string{CommandApplyMovedBlocks, CommandAddMissingInputs},
				},
			},
			"serverInfo": map[string]interface{}{
				"name":    "tfastdiff-lsp",
				"version": serverVersion,
			},
		}
		if !isRequest {
			return nil, nil
		}
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: caps}, nil

	case "initialized", "$/setTrace", "$/cancelRequest", "textDocument/didClose":
		return nil, nil

	case "exit":
		s.ShuttingDown.Store(true)
		return nil, nil

	case "shutdown":
		if !isRequest {
			return nil, nil
		}
		s.ShuttingDown.Store(true)
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: nil}, nil

	case "textDocument/didSave", "textDocument/didOpen", "textDocument/didChange":
		var params DidChangeTextDocumentParams
		if err := json.Unmarshal(req.Params, &params); err == nil && params.TextDocument.URI != "" {
			// With Full sync the change event carries the new text, so the buffer is the
			// source of truth. Falling back to disk when it is absent keeps save-only clients
			// working instead of silently dropping their diagnostics.
			if text := params.Last(); text != nil {
				// Key the buffer by the filesystem path, which is what the diagnostics lookup
				// uses. Keying by the raw URI left every buffer stranded behind a "file://"
				// prefix and diagnostics silently fell back to disk.
				s.buffers.Store(strings.TrimPrefix(params.TextDocument.URI, "file://"), buffer{text: *text})
			}
			return nil, s.publish(params.TextDocument.URI)
		}
		return nil, nil

	case "textDocument/codeAction":
		if !isRequest {
			return nil, nil
		}
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: s.codeActions()}, nil

	case "workspace/executeCommand":
		if !isRequest {
			return nil, nil
		}
		var params ExecuteCommandParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &RPCError{Code: -32602, Message: "invalid executeCommand params"},
			}, nil
		}
		result, err := s.executeCommand(params)
		if err != nil {
			return &Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &RPCError{Code: -32000, Message: err.Error()},
			}, nil
		}
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: result}, nil

	default:
		if !isRequest {
			return nil, nil
		}
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    -32601,
				Message: fmt.Sprintf("method '%s' not found", req.Method),
			},
		}, nil
	}
}

// publish recomputes and sends diagnostics for uri.
func (s *Server) publish(uri string) *Notification {
	return &Notification{
		JSONRPC: "2.0",
		Method:  "textDocument/publishDiagnostics",
		Params: PublishDiagnosticsParams{
			URI:         uri,
			Diagnostics: s.computeDiagnostics(uri),
		},
	}
}

// codeActions builds the quick fixes the server can actually perform. Each command is
// registered in executeCommandProvider, so a client can run it.
func (s *Server) codeActions() []CodeAction {
	actions := []CodeAction{}
	for _, def := range []struct{ title, description, command string }{
		{
			title:       "Apply Terraform moved {} block migrations",
			description: "Write a moved {} block file for the selected state addresses",
			command:     CommandApplyMovedBlocks,
		},
		{
			title:       "Inject missing required module inputs",
			description: "Add the required upstream inputs that this module call does not pass",
			command:     CommandAddMissingInputs,
		},
	} {
		// Arguments stay empty: the editor must fill in the state addresses, module name, and
		// input names it knows about. A server that guessed them would write to the wrong place.
		actions = append(actions, CodeAction{
			Title: def.title,
			Kind:  "quickfix",
			Command: &Command{
				Title:     def.description,
				Command:   def.command,
				Arguments: []interface{}{},
			},
		})
	}
	return actions
}

func (s *Server) computeDiagnostics(uri string) []Diagnostic {
	filePath := strings.TrimPrefix(uri, "file://")
	dir := filepath.Dir(filePath)

	info, err := s.inspect(filePath, dir)
	if err != nil {
		return []Diagnostic{
			{
				Range:    Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 1}},
				Severity: 1, // Error
				Source:   "tfastdiff",
				Message:  fmt.Sprintf("Failed to inspect module AST: %v", err),
			},
		}
	}

	var diags []Diagnostic
	for name, v := range info.Variables {
		if v.Deprecated != "" {
			line := clampLine(v.Pos.Line)
			diags = append(diags, Diagnostic{
				Range:    Range{Start: Position{Line: line, Character: 0}, End: Position{Line: line, Character: 10}},
				Severity: 2, // Warning
				Source:   "tfastdiff",
				Message:  fmt.Sprintf("Variable '%s' is deprecated: %s", name, v.Deprecated),
			})
		}
	}
	return diags
}

// inspect prefers the in-editor buffer and falls back to disk.
//
// Under Full sync the client has already sent the document text, so reading the
// file from disk would report on stale content while the user is mid-edit. A
// save-only client never populates the buffer, so the disk read still applies.
func (s *Server) inspect(filePath, dir string) (*ast.ModuleInfo, error) {
	if content, ok := s.buffers.Load(filePath); ok {
		buffered, isBuffer := content.(buffer)
		if isBuffer {
			info, err := ast.InspectSource(dir, filePath, buffered.text)
			if err == nil {
				return info, nil
			}
			// An unsaved buffer that cannot be parsed is expected mid-edit; fall through to
			// disk so the user keeps the last good diagnostics instead of an error on every
			// keystroke.
		}
	}
	return ast.Inspect(dir)
}

// clampLine converts a 1-based source line into a 0-based LSP position. A missing
// position (line 0) previously produced a negative line number, which is invalid
// per the LSP specification.
func clampLine(line int) int {
	if line <= 0 {
		return 0
	}
	return line - 1
}

// maxMessageBytes bounds a single JSON-RPC message. The framing header is attacker-controlled, so
// without a ceiling a Content-Length of 1e14 turns into an allocation of 100 TB and hangs the
// daemon (found by FuzzLSPServe). 64 MiB is far above any legitimate document, including a full
// sync of a very large Terraform file.
const maxMessageBytes = 64 << 20

// Serve reads JSON-RPC framing and dispatches requests until EOF.
func (s *Server) Serve(reader io.Reader) error {
	bufReader := bufio.NewReader(reader)
	for {
		contentLength := 0
		for {
			line, err := bufReader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					cl, err := strconv.Atoi(strings.TrimSpace(parts[1]))
					// A malformed, negative, or oversized length must not reach make([]byte, n):
					// it would allocate an arbitrary amount of memory from one header.
					if err == nil && cl > 0 && cl <= maxMessageBytes {
						contentLength = cl
					}
				}
			}
		}

		if contentLength == 0 {
			continue
		}

		body := make([]byte, contentLength)
		_, err := io.ReadFull(bufReader, body)
		if err != nil {
			return err
		}

		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			continue
		}

		resp, notif := s.HandleRequest(req)
		if resp != nil {
			_ = s.send(resp)
		}
		if notif != nil {
			_ = s.send(notif)
		}

		// exit is terminal per the LSP lifecycle: reply to any pending request first, then stop.
		// Previously only stdin EOF ended the daemon, so a client that shut down cleanly waited
		// for the editor to close the pipe.
		if req.Method == "exit" {
			return nil
		}
	}
}

// ApplyMovedBlocksArgs is the payload of tfastdiff.applyMovedBlocks.
type ApplyMovedBlocksArgs struct {
	From string `json:"from"`
	To   string `json:"to"`
	Dir  string `json:"dir"`
}

// AddMissingInputsArgs is the payload of tfastdiff.addMissingInputs.
type AddMissingInputsArgs struct {
	Module string   `json:"module"`
	Dir    string   `json:"dir"`
	Inputs []string `json:"inputs"`
}

// executeCommand performs one of the advertised quick fixes.
//
// Both commands mutate files in the workspace, which is exactly why they are exposed as
// workspace/executeCommand rather than running implicitly when the client merely lists them.
//
// Arguments are a single JSON object rather than a positional list: a positional contract made
// "add input X to module M in dir D" ambiguous, because the input names and the directory were
// both trailing strings and neither could be validated.
func (s *Server) executeCommand(params ExecuteCommandParams) (any, error) {
	firstArgument := func() json.RawMessage {
		if len(params.Arguments) == 0 {
			return nil
		}
		return params.Arguments[0]
	}

	switch params.Command {
	case CommandApplyMovedBlocks:
		var args ApplyMovedBlocksArgs
		if err := json.Unmarshal(firstArgument(), &args); err != nil {
			return nil, fmt.Errorf("%s requires an object argument with 'from' and 'to'", params.Command)
		}
		if strings.TrimSpace(args.From) == "" || strings.TrimSpace(args.To) == "" {
			return nil, fmt.Errorf("%s requires a non-empty 'from' and 'to' state address", params.Command)
		}
		dir := args.Dir
		if dir == "" {
			dir = "."
		}
		target := filepath.Join(dir, "moved.tf")
		if err := codemod.WriteMovedBlocks(target, []codemod.MovedBlock{{From: args.From, To: args.To}}); err != nil {
			return nil, err
		}
		return map[string]any{"written": target, "from": args.From, "to": args.To}, nil

	case CommandAddMissingInputs:
		var args AddMissingInputsArgs
		if err := json.Unmarshal(firstArgument(), &args); err != nil {
			return nil, fmt.Errorf("%s requires an object argument with 'module' and 'inputs'", params.Command)
		}
		if strings.TrimSpace(args.Module) == "" {
			return nil, fmt.Errorf("%s requires a non-empty module name", params.Command)
		}
		names := make([]string, 0, len(args.Inputs))
		for _, name := range args.Inputs {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				names = append(names, trimmed)
			}
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("%s requires at least one input name to add", params.Command)
		}
		dir := args.Dir
		if dir == "" {
			dir = "."
		}
		// Only names are accepted, never values. codemod.AddMissingInputs writes every insertion
		// as a string literal regardless of the declared type, so injecting one unprompted would
		// write invalid Terraform for bool, list, or object inputs (roadmap #7). The editor is
		// the right place for the operator to supply the value.
		values := make(map[string]string, len(names))
		for _, name := range names {
			values[name] = ""
		}
		modified, err := codemod.AddMissingInputs(dir, args.Module, values)
		if err != nil {
			return nil, err
		}
		return map[string]any{"modified": modified}, nil

	default:
		return nil, fmt.Errorf("unknown command %q", params.Command)
	}
}

func (s *Server) send(payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := s.Writer.Write([]byte(header)); err != nil {
		return err
	}
	_, err = s.Writer.Write(data)
	return err
}

var defaultStdin io.Reader = os.Stdin
var defaultStdout io.Writer = os.Stdout

// RunDaemon starts the LSP server reading from stdin and writing to stdout.
func RunDaemon() error {
	server := NewServer(defaultStdout)
	return server.Serve(defaultStdin)
}
