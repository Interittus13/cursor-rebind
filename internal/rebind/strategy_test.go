package rebind

import "testing"

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
}
