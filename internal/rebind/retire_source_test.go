package rebind

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Interittus13/cursor-rebind/internal/paths"
)

func TestProjectSlugIsStub(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "mcps"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "canvases"), 0o755)
	if !projectSlugIsStub(dir) {
		t.Fatal("expected stub")
	}
	_ = os.MkdirAll(filepath.Join(dir, "agent-transcripts"), 0o755)
	if projectSlugIsStub(dir) {
		t.Fatal("expected non-stub with transcripts")
	}
}

func TestEnsureMetadataTrackedRepo(t *testing.T) {
	e := map[string]any{}
	to := "/tmp/to-proj"
	if !ensureMetadataTrackedRepo(e, to) {
		t.Fatal("expected add")
	}
	if ensureMetadataTrackedRepo(e, to) {
		t.Fatal("expected no-op on second call")
	}
}

func TestOrphanWorkspaceFolders(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "from-proj")
	keep := "keepid"
	drop := "dropid"
	for _, id := range []string{keep, drop} {
		dir := filepath.Join(root, "ws", id)
		_ = os.MkdirAll(dir, 0o755)
		meta := []byte(`{"folder":"` + paths.FileURI(from) + `"}`)
		if err := os.WriteFile(filepath.Join(dir, "workspace.json"), meta, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := orphanWorkspaceFolders(filepath.Join(root, "ws"), from, keep); err != nil {
		t.Fatal(err)
	}
	keepRaw, _ := os.ReadFile(filepath.Join(root, "ws", keep, "workspace.json"))
	dropRaw, _ := os.ReadFile(filepath.Join(root, "ws", drop, "workspace.json"))
	if !strings.Contains(string(dropRaw), ".__rebind_orphan_") {
		t.Fatalf("drop not orphaned: %s", dropRaw)
	}
	if strings.Contains(string(keepRaw), ".__rebind_orphan_") {
		t.Fatalf("keep was orphaned: %s", keepRaw)
	}
}
