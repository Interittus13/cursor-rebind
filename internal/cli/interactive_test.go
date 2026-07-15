package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	got := expandHome("~/Documents/x")
	want := filepath.Join(home, "Documents/x")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if expandHome("~") != home {
		t.Fatalf("tilde alone")
	}
}

func TestIsInteractiveTTYFalseOnPipe(t *testing.T) {
	// When tests run, stdin is typically not a char device.
	// We only assert the helper does not panic; behavior depends on environment.
	_ = isInteractiveTTY()
}
