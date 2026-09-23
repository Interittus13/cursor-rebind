package discover

import (
	"testing"

	"github.com/Interittus13/cursor-rebind/internal/paths"
)

func TestClassifyWorkspace(t *testing.T) {
	cases := []struct {
		w    Workspace
		want StaleKind
	}{
		{Workspace{ID: "abc", FolderPath: "/home/u/proj"}, StaleNone},
		{Workspace{ID: "empty-window"}, StaleEmptyWindow},
		{Workspace{ID: "dead", FolderPath: "/home/u/proj.__rebind_orphan_deadbeef"}, StaleOrphanWorkspace},
		{Workspace{ID: "dead", FolderURI: "file:///x.__rebind_orphan_abcd"}, StaleOrphanWorkspace},
	}
	for _, tc := range cases {
		if got := ClassifyWorkspace(tc.w); got != tc.want {
			t.Fatalf("ClassifyWorkspace(%+v)=%q want %q", tc.w, got, tc.want)
		}
	}
}

func TestClassifyProject(t *testing.T) {
	cases := []struct {
		p    AgentProject
		want StaleKind
	}{
		{AgentProject{Name: "home-u-Documents-proj", TranscriptCount: 3}, StaleNone},
		{AgentProject{Name: "tmp-abc-def", TranscriptCount: 0}, StaleEphemeralProject},
		{AgentProject{Name: "empty-window", TranscriptCount: 0}, StaleEphemeralProject},
		{AgentProject{Name: "1784008642728", TranscriptCount: 0}, StaleEphemeralProject},
		{AgentProject{Name: "home-u-Documents-proj", TranscriptCount: 0}, StaleEmptyAgentProject},
	}
	for _, tc := range cases {
		if got := ClassifyProject(tc.p); got != tc.want {
			t.Fatalf("ClassifyProject(%+v)=%q want %q", tc.p, got, tc.want)
		}
	}
}

func TestFilterInventory(t *testing.T) {
	inv := &Inventory{
		Workspaces: []Workspace{
			{ID: "keep", FolderPath: "/a", HeaderChats: 2},
			{ID: "empty-window"},
			{ID: "orph", FolderPath: "/a.__rebind_orphan_orph"},
		},
		Projects: []AgentProject{
			{Name: "home-keep", TranscriptCount: 2},
			{Name: "tmp-x", TranscriptCount: 0},
			{Name: "home-empty", TranscriptCount: 0},
		},
	}
	ws, proj, hw, hp := FilterInventory(inv, true)
	if len(ws) != 1 || ws[0].ID != "keep" {
		t.Fatalf("workspaces=%v", ws)
	}
	if len(proj) != 1 || proj[0].Name != "home-keep" {
		t.Fatalf("projects=%v", proj)
	}
	if hw != 2 || hp != 2 {
		t.Fatalf("hidden ws=%d proj=%d", hw, hp)
	}
	wsAll, projAll, _, _ := FilterInventory(inv, false)
	if len(wsAll) != 3 || len(projAll) != 3 {
		t.Fatalf("unfiltered sizes %d %d", len(wsAll), len(projAll))
	}
}

func TestFilterByPath(t *testing.T) {
	want := "/home/u/proj"
	ws := []Workspace{
		{ID: "a", FolderPath: want},
		{ID: "b", FolderPath: want + ".__rebind_orphan_bbbbbbbb"},
		{ID: "c", FolderPath: "/home/u/other"},
	}
	proj := []AgentProject{
		{Name: "home-u-proj", InferredPath: want, TranscriptCount: 1},
		{Name: "home-u-other", InferredPath: "/home/u/other", TranscriptCount: 1},
	}
	wsOut, projOut := FilterByPath(ws, proj, want)
	if len(wsOut) != 2 {
		t.Fatalf("workspaces=%d", len(wsOut))
	}
	if len(projOut) != 1 || projOut[0].Name != "home-u-proj" {
		t.Fatalf("projects=%v", projOut)
	}
}

func TestCollectPruneCandidates(t *testing.T) {
	inv := &Inventory{
		Roots: paths.Roots{WorkspaceStorage: "/ws", ProjectsDir: "/proj"},
		Workspaces: []Workspace{
			{ID: "live", FolderPath: "/home/u/a"},
			{ID: "orph1", FolderPath: "/home/u/a.__rebind_orphan_orph1"},
			{ID: "empty-window"},
		},
		Projects: []AgentProject{
			{Name: "tmp-dead", Dir: "/proj/tmp-dead", TranscriptCount: 0},
			{Name: "tmp-alive", Dir: "/proj/tmp-alive", TranscriptCount: 2},
			{Name: "home-u-old", Dir: "/proj/home-u-old", TranscriptCount: 0},
		},
	}
	cands := CollectPruneCandidates(inv, false)
	if len(cands) != 3 { // orph1, empty-window, tmp-dead
		t.Fatalf("safe cands=%d %#v", len(cands), cands)
	}
	for _, c := range cands {
		if !c.Safe {
			t.Fatalf("unexpected unsafe %#v", c)
		}
	}
	cands = CollectPruneCandidates(inv, true)
	if len(cands) != 4 {
		t.Fatalf("with empty projects cands=%d", len(cands))
	}
}
