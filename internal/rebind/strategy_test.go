package rebind

import (
	"strings"
	"testing"
)

func TestChooseStrategy(t *testing.T) {
	if got := chooseStrategy("", SideInventory{}); got != StrategyCreate {
		t.Fatalf("empty target id → create, got %s", got)
	}
	if got := chooseStrategy("abc", SideInventory{IDEEmpty: 2}); got != StrategyReplaceEmpty {
		t.Fatalf("empty target chats → replace-empty, got %s", got)
	}
	if got := chooseStrategy("abc", SideInventory{IDEContentful: 1}); got != StrategyMerge {
		t.Fatalf("contentful IDE → merge, got %s", got)
	}
	if got := chooseStrategy("abc", SideInventory{AgentContentful: 3}); got != StrategyMerge {
		t.Fatalf("contentful Agent → merge, got %s", got)
	}
}

func TestGlassProjectMatchesFrom(t *testing.T) {
	from := "/home/ulap92/Documents/Arpit/_Others/GitHub/mover"
	if !glassProjectMatchesFrom(from, from, ModeExact) {
		t.Fatal("exact path should match")
	}
	if !glassProjectMatchesFrom("/tmp/foo/mover", from, ModeExact) {
		t.Fatal("basename mover should match")
	}
	if glassProjectMatchesFrom("/tmp/cursor-rebind", from, ModeExact) {
		t.Fatal("unrelated path should not match")
	}
	ai := "/home/ulap92/Documents/Arpit/_Others/GitHub/ai"
	if !glassProjectMatchesFrom(ai, ai, ModeExact) {
		t.Fatal("exact ai path should match")
	}
	if glassProjectMatchesFrom("/tmp/other/ai", ai, ModeExact) {
		t.Fatal("short basename ai must not match other folders")
	}
}

func TestPatchComposerHeaderValueWorkspace(t *testing.T) {
	raw := `{"composerId":"x","name":"Basic","workspaceIdentifier":{"id":"old","uri":{"fsPath":"/home/ulap177/Documents/Arpit/_Others/GitHub/ai","path":"/home/ulap177/Documents/Arpit/_Others/GitHub/ai","scheme":"file"}},"hasUnreadMessages":true}`
	out, changed, err := patchComposerHeaderValueWorkspace(raw, "newid", "/home/ulap92/Documents/Arpit/_Others/GitHub/ai")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if !strings.Contains(out, "/home/ulap92/Documents/Arpit/_Others/GitHub/ai") {
		t.Fatalf("path not rewritten: %s", out)
	}
	if !strings.Contains(out, `"branches"`) {
		t.Fatalf("expected branches on trackedGitRepos: %s", out)
	}
	if !strings.Contains(out, `"id":"newid"`) {
		t.Fatalf("id not rewritten: %s", out)
	}
	if !strings.Contains(out, `"unifiedMode":"agent"`) {
		t.Fatalf("not promoted to agent: %s", out)
	}
	if !strings.Contains(out, `"hasUnreadMessages":true`) {
		t.Fatal("lost extra fields")
	}
}

func TestPromoteBlobForAgentsWindow(t *testing.T) {
	blob := map[string]any{"unifiedMode": "chat", "forceMode": "chat", "isAgentic": false}
	promoteBlobForAgentsWindow(blob)
	if blob["unifiedMode"] != "agent" || blob["isAgentic"] != true || blob["forceMode"] != "edit" {
		t.Fatalf("%v", blob)
	}
}

func TestComposerIsEmptyNamedNoBubbles(t *testing.T) {
	// Guard: empty-name stubs are empty; we rely on rewriteHeaders dropping
	// source-side empty stubs so old paths do not linger.
	if !composerIsEmpty(nil, "", "") {
		t.Fatal("missing id should be empty")
	}
}
