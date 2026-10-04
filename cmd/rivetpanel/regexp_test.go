package main

import (
	"testing"

	"github.com/xenycx/rivetpanel/internal/lazyre"
)

// Package-level patterns compile on first use (see internal/lazyre); compile
// every one linked into the panel here so an invalid pattern fails a test
// instead of a request.
func TestAllLazyPatternsCompile(t *testing.T) {
	if n := lazyre.CompileAll(); n < 50 {
		t.Fatalf("only %d lazily compiled patterns are registered; expected the panel's package-level patterns", n)
	}
}
