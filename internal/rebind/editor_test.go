package rebind

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Interittus13/cursor-rebind/internal/discover"
	"github.com/Interittus13/cursor-rebind/internal/paths"
)

func TestRewriteEditorStateComposerIDsKeepsGrid(t *testing.T) {
	raw := []byte(`{"serializedGrid":{"root":{"type":"branch","data":[{"type":"leaf","data":{"id":1,"editors":[{"id":"workbench.editor.composer.input","value":"{\"composerId\":\"old\",\"restoreInRegularEditorGroup\":true}"}],"mru":[0]},"size":679}],"size":444},"orientation":0,"width":444,"height":679},"activeGroup":1,"mostRecentActiveGroups":[1]}`)
	out := rewriteEditorStateComposerIDs(raw, "new-id")
	var state map[string]any
	if err := json.Unmarshal(out, &state); err != nil {
		t.Fatal(err)
	}
	leaf := state["serializedGrid"].(map[string]any)["root"].(map[string]any)["data"].([]any)[0].(map[string]any)
	editors := leaf["data"].(map[string]any)["editors"].([]any)
	if len(editors) != 1 {
		t.Fatalf("editors=%d", len(editors))
	}
	val := editors[0].(map[string]any)["value"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(val), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["composerId"] != "new-id" {
		t.Fatalf("composerId=%v", payload["composerId"])
	}
}

func TestFindSourceIncludesOrphans(t *testing.T) {
	to := "/home/ulap92/Documents/Arpit/_Others/GitHub/cursor-rebind"
	from := "/home/ulap92/Documents/Arpit/_Others/GitHub/mover"
	inv := &discover.Inventory{
		Roots: paths.Roots{ProjectsDir: "/tmp/projects", WorkspaceStorage: "/tmp/ws-missing"},
		Workspaces: []discover.Workspace{
			{ID: "shell", FolderPath: to},
			{ID: "orphan", FolderPath: to + ".__rebind_orphan_b3ebc758"},
		},
	}
	sources := findSourceWorkspaceIDs(inv, from, to, "shell", ModeExact)
	found := false
	for _, id := range sources {
		if id == "orphan" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected orphan in sources, got %v", sources)
	}
}

func TestDisplayPathFor(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	got := displayPathFor(filepath.Join(home, "Documents", "proj"))
	if !strings.HasPrefix(got, "~/") {
		t.Fatalf("displayPath=%q", got)
	}
}

func TestExtractComposerIDsFromEditorValue(t *testing.T) {
	raw := []byte(`{"serializedGrid":{"root":{"type":"branch","data":[{"type":"leaf","data":{"id":1,"editors":[{"id":"workbench.editor.composer.input","value":"{\"composerId\":\"df8087ae-c788-4a3e-82f9-33a9ef6ab8b7\",\"restoreInRegularEditorGroup\":true}"}],"mru":[0]},"size":679}],"size":444},"orientation":0},"activeGroup":1}`)
	var got []string
	extractComposerIDsFromEditorValue(raw, func(id string) {
		got = append(got, id)
	})
	if len(got) != 1 || got[0] != "df8087ae-c788-4a3e-82f9-33a9ef6ab8b7" {
		t.Fatalf("got %v", got)
	}
}
