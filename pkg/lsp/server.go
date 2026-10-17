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

	"github.com/23skdu/terraform_ast/pkg/ast"
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

// Server is a Language Server Protocol daemon for terraform_ast.
type Server struct {
	Writer io.Writer
}

// NewServer creates a new LSP server instance.
func NewServer(writer io.Writer) *Server {
	return &Server{Writer: writer}
}

// HandleRequest processes an incoming JSON-RPC request.
func (s *Server) HandleRequest(req Request) (*Response, *Notification) {
	switch req.Method {
	case "initialize":
		caps := map[string]interface{}{
			"capabilities": map[string]interface{}{
				"textDocumentSync":   1, // Full
				"codeActionProvider": true,
			},
			"serverInfo": map[string]interface{}{
				"name":    "tfastdiff-lsp",
				"version": "v0.8.0",
			},
		}
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: caps}, nil

	case "initialized":
		return nil, nil

	case "shutdown":
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: nil}, nil

	case "textDocument/didSave", "textDocument/didOpen":
		var params DidSaveTextDocumentParams
		if err := json.Unmarshal(req.Params, &params); err == nil && params.TextDocument.URI != "" {
			diags := s.computeDiagnostics(params.TextDocument.URI)
			notif := &Notification{
				JSONRPC: "2.0",
				Method:  "textDocument/publishDiagnostics",
				Params: PublishDiagnosticsParams{
					URI:         params.TextDocument.URI,
					Diagnostics: diags,
				},
			}
			return nil, notif
		}
		return nil, nil

	case "textDocument/codeAction":
		actions := []CodeAction{
			{
				Title: "Apply Terraform moved {} block migrations",
				Kind:  "quickfix",
				Command: &Command{
					Title:   "Apply moved blocks",
					Command: "tfastdiff.applyMovedBlocks",
				},
			},
			{
				Title: "Inject missing required module inputs",
				Kind:  "quickfix",
				Command: &Command{
					Title:   "Add missing inputs",
					Command: "tfastdiff.addMissingInputs",
				},
			},
		}
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: actions}, nil

	default:
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

func (s *Server) computeDiagnostics(uri string) []Diagnostic {
	filePath := strings.TrimPrefix(uri, "file://")
	dir := filepath.Dir(filePath)
	info, err := ast.Inspect(dir)
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
			diags = append(diags, Diagnostic{
				Range:    Range{Start: Position{Line: v.Pos.Line - 1, Character: 0}, End: Position{Line: v.Pos.Line - 1, Character: 10}},
				Severity: 2, // Warning
				Source:   "tfastdiff",
				Message:  fmt.Sprintf("Variable '%s' is deprecated: %s", name, v.Deprecated),
			})
		}
	}
	return diags
}

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
					if err == nil {
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
