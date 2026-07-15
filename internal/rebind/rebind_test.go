package rebind_test

import (
	"testing"
	"time"

	"github.com/Interittus13/cursor-rebind/internal/discover"
	"github.com/Interittus13/cursor-rebind/internal/paths"
	"github.com/Interittus13/cursor-rebind/internal/rebind"
)

func mustParse(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestBuildPlanExact(t *testing.T) {
	from := "/home/ulap177/Documents/Arpit/_Others/GitHub/Stambha"
	to := "/home/ulap92/Documents/Arpit/_Others/GitHub/Stambha"
	inv := &discover.Inventory{
		Roots: paths.Roots{ProjectsDir: "/tmp/projects"},
		Headers: discover.HeaderIndex{
			Entries: []discover.HeaderEntry{
				{ComposerID: "a", WorkspacePath: from, WorkspaceID: "old"},
				{ComposerID: "b", WorkspacePath: from, WorkspaceID: "old"},
				{ComposerID: "c", WorkspacePath: "/other"},
			},
		},
		Workspaces: []discover.Workspace{
			{ID: "old", FolderPath: from},
			{ID: "newid", FolderPath: to, ModTime: mustParse("2026-07-14T12:00:00Z")},
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
	if len(plan.SourceWSIDs) != 1 || plan.SourceWSIDs[0] != "old" {
		t.Fatalf("sources=%v", plan.SourceWSIDs)
	}
}

func TestBuildPlanPrefersEmptyShellTarget(t *testing.T) {
	to := "/home/ulap92/Documents/Arpit/_Others/GitHub/cursor-rebind"
	from := "/home/ulap92/Documents/Arpit/_Others/GitHub/mover"
	inv := &discover.Inventory{
		Roots: paths.Roots{ProjectsDir: "/tmp/projects", WorkspaceStorage: "/tmp/ws-missing"},
		Headers: discover.HeaderIndex{
			Entries: []discover.HeaderEntry{
				{ComposerID: "chat1", WorkspaceID: "dataws", WorkspacePath: to},
			},
		},
		Workspaces: []discover.Workspace{
			// Data holder rewritten onto `to` (more composers would be discovered if DB existed).
			{ID: "dataws", FolderPath: to, ModTime: mustParse("2026-07-14T15:00:00Z")},
			// Empty shell Cursor minted for the renamed folder.
			{ID: "shellws", FolderPath: to, ModTime: mustParse("2026-07-14T12:00:00Z")},
		},
	}
	plan, err := rebind.BuildPlan(inv, from, to, rebind.ModeExact)
	if err != nil {
		t.Fatal(err)
	}
	// Without readable DBs, contentful counts are 0 for both → newest mtime wins.
	if plan.TargetWSID != "dataws" {
		t.Fatalf("target=%s want dataws (newest when contentful tied)", plan.TargetWSID)
	}
	if len(plan.SourceWSIDs) == 0 {
		t.Fatalf("expected sources, got none; target=%s", plan.TargetWSID)
	}
}

func TestBuildPlanWithForcedTarget(t *testing.T) {
	to := "/home/ulap92/Documents/Arpit/_Others/GitHub/cursor-rebind"
	from := "/home/ulap92/Documents/Arpit/_Others/GitHub/mover"
	inv := &discover.Inventory{
		Roots: paths.Roots{ProjectsDir: "/tmp/projects", WorkspaceStorage: "/tmp/ws-missing"},
		Headers: discover.HeaderIndex{
			Entries: []discover.HeaderEntry{
				{ComposerID: "chat1", WorkspaceID: "dataws", WorkspacePath: to},
			},
		},
		Workspaces: []discover.Workspace{
			{ID: "dataws", FolderPath: to, ModTime: mustParse("2026-07-14T15:00:00Z")},
			{ID: "shellws", FolderPath: to, ModTime: mustParse("2026-07-14T16:00:00Z")},
		},
	}
	plan, err := rebind.BuildPlanWithTarget(inv, from, to, rebind.ModeExact, "shellws")
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetWSID != "shellws" {
		t.Fatalf("target=%s", plan.TargetWSID)
	}
	found := false
	for _, id := range plan.SourceWSIDs {
		if id == "dataws" {
			found = true
		}
		if id == "shellws" {
			t.Fatalf("forced target still listed as source: %v", plan.SourceWSIDs)
		}
	}
	if !found {
		t.Fatalf("expected dataws in sources, got %v", plan.SourceWSIDs)
	}
}

func TestBuildPlanPrefix(t *testing.T) {
	inv := &discover.Inventory{
		Roots: paths.Roots{},
		Headers: discover.HeaderIndex{
			Entries: []discover.HeaderEntry{
				{ComposerID: "1", WorkspacePath: "/home/ulap177/a"},
				{ComposerID: "2", WorkspacePath: "/home/ulap177/b/c"},
				{ComposerID: "3", WorkspacePath: "/home/ulap92/x"},
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
