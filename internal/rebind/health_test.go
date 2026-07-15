package rebind_test

import (
	"strings"
	"testing"

	"github.com/Interittus13/cursor-rebind/internal/discover"
	"github.com/Interittus13/cursor-rebind/internal/paths"
	"github.com/Interittus13/cursor-rebind/internal/rebind"
)

func TestAssessPathHealthSplitBrain(t *testing.T) {
	to := "/home/ulap92/Documents/Arpit/_Others/GitHub/Stambha"
	inv := &discover.Inventory{
		Roots: paths.Roots{},
		Workspaces: []discover.Workspace{
			{ID: "shellws", FolderPath: to, ModTime: mustParse("2026-07-15T17:00:00Z")},
			{ID: "dataws", FolderPath: to, ModTime: mustParse("2026-07-10T12:00:00Z")},
		},
		Headers: discover.HeaderIndex{
			Entries: []discover.HeaderEntry{
				{ComposerID: "a", Name: "Stambha Discord", WorkspaceID: "dataws", WorkspacePath: to},
				{ComposerID: "b", Name: "Export chats", WorkspaceID: "dataws", WorkspacePath: to},
				{ComposerID: "c", Name: "", WorkspaceID: "shellws", WorkspacePath: to},
			},
		},
	}
	h := rebind.AssessPathHealth(inv, to, "shellws")
	if h.OK || !h.SplitBrain {
		t.Fatalf("expected split-brain, got ok=%v issues=%v", h.OK, h.Issues)
	}
	if h.OffTargetNamed != 2 {
		t.Fatalf("offTarget=%d", h.OffTargetNamed)
	}
	if len(h.LiveWorkspaceIDs) != 2 {
		t.Fatalf("live=%v", h.LiveWorkspaceIDs)
	}
	if !strings.Contains(h.RepairHint, "repair --to") {
		t.Fatalf("hint=%q", h.RepairHint)
	}
}

func TestAssessPathHealthHealthy(t *testing.T) {
	to := "/home/ulap92/Documents/Arpit/_Others/GitHub/Stambha"
	inv := &discover.Inventory{
		Workspaces: []discover.Workspace{
			{ID: "shellws", FolderPath: to},
			{ID: "old", FolderPath: to + ".__rebind_orphan_dataws"},
		},
		Headers: discover.HeaderIndex{
			Entries: []discover.HeaderEntry{
				{ComposerID: "a", Name: "Stambha Discord", WorkspaceID: "shellws", WorkspacePath: to},
			},
		},
	}
	h := rebind.AssessPathHealth(inv, to, "shellws")
	if !h.OK {
		t.Fatalf("expected healthy, got %v", h.Issues)
	}
	if h.SplitBrain {
		t.Fatal("unexpected split-brain")
	}
}

func TestBuildPlanPrefersEmptyShellViaNamedHeaders(t *testing.T) {
	to := "/home/ulap92/Documents/Arpit/_Others/GitHub/cursor-rebind"
	from := "/home/ulap92/Documents/Arpit/_Others/GitHub/mover"
	inv := &discover.Inventory{
		Roots: paths.Roots{ProjectsDir: "/tmp/projects", WorkspaceStorage: "/tmp/ws-missing"},
		Headers: discover.HeaderIndex{
			Entries: []discover.HeaderEntry{
				{ComposerID: "chat1", Name: "Real chat", WorkspaceID: "dataws", WorkspacePath: to},
			},
		},
		Workspaces: []discover.Workspace{
			{ID: "dataws", FolderPath: to, ModTime: mustParse("2026-07-14T15:00:00Z")},
			{ID: "shellws", FolderPath: to, ModTime: mustParse("2026-07-14T12:00:00Z")},
		},
	}
	plan, err := rebind.BuildPlan(inv, from, to, rebind.ModeExact)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetWSID != "shellws" {
		t.Fatalf("target=%s want shellws (fewest named headers)", plan.TargetWSID)
	}
}

func TestUnhealthyErrorMessage(t *testing.T) {
	err := &rebind.UnhealthyError{
		BackupID: "bak1",
		Report: &rebind.HealthReport{
			Path:       "/tmp/p",
			Issues:     []string{"2 live ids"},
			RepairHint: "cursor-rebind repair --to /tmp/p --yes",
		},
	}
	msg := err.Error()
	if !strings.Contains(msg, "split-brain") || !strings.Contains(msg, "bak1") {
		t.Fatalf("msg=%q", msg)
	}
}
