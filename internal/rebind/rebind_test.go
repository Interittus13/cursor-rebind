package rebind_test

import (
	"path/filepath"
	"testing"

	"github.com/Interittus13/cursor-rebind/internal/discover"
	"github.com/Interittus13/cursor-rebind/internal/paths"
	"github.com/Interittus13/cursor-rebind/internal/rebind"
)

func TestBuildPlanExact(t *testing.T) {
	from := "/home/ulap177/Documents/Arpit/_Others/GitHub/Stambha"
	to := "/home/ulap92/Documents/Arpit/_Others/GitHub/Stambha"
	inv := &discover.Inventory{
		Roots: paths.Roots{ProjectsDir: "/tmp/projects"},
		Headers: discover.HeaderIndex{
			Entries: []discover.HeaderEntry{
				{ComposerID: "a", WorkspacePath: from},
				{ComposerID: "b", WorkspacePath: from},
				{ComposerID: "c", WorkspacePath: "/other"},
			},
		},
		Workspaces: []discover.Workspace{
			{ID: "newid", FolderPath: to},
		},
	}
	plan, err := rebind.BuildPlan(inv, from, to, rebind.ModeExact)
	if err != nil {
		t.Fatal(err)
	}
	if plan.HeadersMatched != 2 {
		t.Fatalf("matched=%d", plan.HeadersMatched)
	}
	if plan.TargetWSID != "newid" {
		t.Fatalf("target id=%s", plan.TargetWSID)
	}
}

func TestBuildPlanPrefix(t *testing.T) {
	inv := &discover.Inventory{
		Roots: paths.Roots{},
		Headers: discover.HeaderIndex{
			Entries: []discover.HeaderEntry{
				{WorkspacePath: "/home/ulap177/a"},
				{WorkspacePath: "/home/ulap177/b/c"},
				{WorkspacePath: "/home/ulap92/x"},
			},
		},
	}
	plan, err := rebind.BuildPlan(inv, "/home/ulap177", "/home/ulap92", rebind.ModePrefix)
	if err != nil {
		t.Fatal(err)
	}
	if plan.HeadersMatched != 2 {
		t.Fatalf("matched=%d want 2", plan.HeadersMatched)
	}
}

func TestSanitizeRoundtripDirs(t *testing.T) {
	p := "/home/ulap92/Documents/Arpit/_Others/GitHub/mover"
	got := paths.SanitizeProjectPath(p)
	want := "home-ulap92-Documents-Arpit-Others-GitHub-mover"
	if got != want {
		t.Fatalf("got %q", got)
	}
	_ = filepath.Separator
}
