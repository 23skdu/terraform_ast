package ast

import (
	"testing"
)

func TestNormalizeAddressSingleQuote(t *testing.T) {
	if got := normalizeAddress("'a'"); got != "a" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeAddress("  x  "); got != "x" {
		t.Fatalf("got %q", got)
	}
}
