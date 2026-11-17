package hooks

import (
	"bytes"
	"testing"
)

func TestRunSuccess(t *testing.T) {
	var out, errb bytes.Buffer
	err := Run(Hook{Name: "echo", Command: "echo", Args: []string{"hi"}}, &out, &errb)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("hi")) {
		t.Fatalf("expected hi, got %s", out.String())
	}
}

func TestRunEmptyCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if err := Run(Hook{Name: "x", Command: " "}, &out, &errb); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunFailure(t *testing.T) {
	var out, errb bytes.Buffer
	if err := Run(Hook{Name: "false", Command: "false"}, &out, &errb); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunAllSuccess(t *testing.T) {
	var out, errb bytes.Buffer
	err := RunAll([]Hook{
		{Name: "echo", Command: "echo", Args: []string{"a"}},
	}, &out, &errb)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunAllStops(t *testing.T) {
	var out, errb bytes.Buffer
	err := RunAll([]Hook{
		{Name: "echo", Command: "echo", Args: []string{"a"}},
		{Name: "false", Command: "false"},
		{Name: "echo2", Command: "echo", Args: []string{"b"}},
	}, &out, &errb)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunPhase(t *testing.T) {
	var out, errb bytes.Buffer
	hooks := []Hook{
		{Name: "pre", Command: "echo", Args: []string{"pre"}, Phase: "pre-analysis"},
		{Name: "post", Command: "echo", Args: []string{"post"}, Phase: "post-analysis"},
	}
	if err := RunPhase(hooks, "pre-analysis", &out, &errb); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("pre")) || bytes.Contains(out.Bytes(), []byte("post")) {
		t.Fatalf("unexpected output: %s", out.String())
	}

	out.Reset()
	errb.Reset()
	if err := RunPhase(hooks, "post-analysis", &out, &errb); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("post")) {
		t.Fatalf("unexpected output: %s", out.String())
	}

	// Default phase.
	defaultHooks := []Hook{{Name: "d", Command: "echo", Args: []string{"default"}}}
	out.Reset()
	if err := RunPhase(defaultHooks, "pre-analysis", &out, &errb); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("default")) {
		t.Fatalf("unexpected output: %s", out.String())
	}

	// No matching phase yields no-op success.
	out.Reset()
	if err := RunPhase(hooks, "nonexistent", &out, &errb); err != nil {
		t.Fatal(err)
	}
}

func TestPhaseOf(t *testing.T) {
	if phaseOf(Hook{}) != "pre-analysis" {
		t.Fatal("expected default phase")
	}
	if phaseOf(Hook{Phase: "post-analysis"}) != "post-analysis" {
		t.Fatal("expected explicit phase")
	}
}
