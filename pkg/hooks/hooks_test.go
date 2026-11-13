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
