// Package hooks provides pre/post execution hook support for the CLI, similar to
// Terragrunt hooks.
package hooks

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Hook describes a named shell command to run.
type Hook struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// Run executes a hook command, streaming output to stdout/stderr.
func Run(h Hook, stdout, stderr io.Writer) error {
	if strings.TrimSpace(h.Command) == "" {
		return fmt.Errorf("hook %q has empty command", h.Name)
	}
	cmd := exec.Command(h.Command, h.Args...) // #nosec G204 -- hook command is operator-supplied
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hook %q failed: %w", h.Name, err)
	}
	return nil
}

// RunAll runs a list of hooks in order, stopping on first failure.
func RunAll(hooks []Hook, stdout, stderr io.Writer) error {
	for _, h := range hooks {
		if err := Run(h, stdout, stderr); err != nil {
			return err
		}
	}
	return nil
}
