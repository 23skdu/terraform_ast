package report

import (
	"bytes"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/diff"
)

func TestRenderToWriters(t *testing.T) {
	r := &diff.DiffReport{ModuleName: "m", OldVersion: "1.0", NewVersion: "1.1", BumpType: "minor", Verdict: "OK", Summary: "s"}
	var b bytes.Buffer
	if err := RenderTextTo(&b, r, Findings{}); err != nil || b.Len() == 0 {
		t.Fatalf("text: %v", err)
	}
	b.Reset()
	if err := RenderMarkdownTo(&b, r, Findings{}); err != nil || b.Len() == 0 {
		t.Fatalf("md: %v", err)
	}
	b.Reset()
	if err := RenderJSONTo(&b, r, Findings{}); err != nil || b.Len() == 0 {
		t.Fatalf("json: %v", err)
	}

	// Exercise error paths.
	fw := &failingWriter{failAfter: 0}
	if err := RenderTextTo(fw, r, Findings{}); err == nil {
		t.Fatal("expected error")
	}
	fw2 := &failingWriter{failAfter: 0}
	if err := RenderMarkdownTo(fw2, r, Findings{}); err == nil {
		t.Fatal("expected error")
	}
	fw3 := &failingWriter{failAfter: 0}
	if err := RenderJSONTo(fw3, r, Findings{}); err == nil {
		t.Fatal("expected error")
	}
}

type failingWriter struct {
	failAfter int
	written   int
}

func (f *failingWriter) Write(p []byte) (int, error) {
	if f.written >= f.failAfter {
		return 0, errWriteFailed
	}
	f.written += len(p)
	return len(p), nil
}

var errWriteFailed = &writeErr{}

type writeErr struct{}

func (e *writeErr) Error() string { return "write failed" }
