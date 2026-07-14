package paths_test

import (
	"testing"

	"github.com/Interittus13/cursor-rebind/internal/paths"
)

func TestSanitizeProjectPath(t *testing.T) {
	got := paths.SanitizeProjectPath("/home/ulap92/Documents/Arpit/_Others/GitHub/mover")
	want := "home-ulap92-Documents-Arpit-Others-GitHub-mover"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFileURI(t *testing.T) {
	got := paths.FileURI("/home/ulap92/proj")
	want := "file:///home/ulap92/proj"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPathFromFileURI(t *testing.T) {
	got := paths.PathFromFileURI("file:///home/ulap92/proj")
	want := "/home/ulap92/proj"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
