package rebind

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Interittus13/cursor-rebind/internal/paths"
)

func TestPurgeSourceStorage(t *testing.T) {
	root := t.TempDir()
	wsRoot := filepath.Join(root, "ws")
	projects := filepath.Join(root, "projects")
	from := filepath.Join(root, "from-proj")
	to := filepath.Join(root, "to-proj")

	sourceID := "sourcesrc1"
	targetID := "targettgt1"
	aliveID := "alivealive1" // matches from path but not orphaned — must keep

	for _, id := range []string{sourceID, targetID, aliveID} {
		dir := filepath.Join(wsRoot, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Orphaned source
	orphanURI := paths.FileURI(from + ".__rebind_orphan_" + sourceID[:8])
	if err := os.WriteFile(filepath.Join(wsRoot, sourceID, "workspace.json"),
		[]byte(`{"folder":"`+orphanURI+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Target points at --to
	if err := os.WriteFile(filepath.Join(wsRoot, targetID, "workspace.json"),
		[]byte(`{"folder":"`+paths.FileURI(to)+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Non-orphan leftover still on from path (should not purge)
	if err := os.WriteFile(filepath.Join(wsRoot, aliveID, "workspace.json"),
		[]byte(`{"folder":"`+paths.FileURI(from)+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	slug := paths.SanitizeProjectPath(from)
	slugDir := filepath.Join(projects, slug)
	if err := os.MkdirAll(filepath.Join(slugDir, "mcps"), 0o755); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		From:        from,
		To:          to,
		ProjectFrom: slug,
		ProjectTo:   paths.SanitizeProjectPath(to),
		TargetWSID:  targetID,
		SourceWSIDs: []string{sourceID, aliveID, targetID},
	}

	n, removed, err := purgeSourceStorage(wsRoot, projects, plan)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("purged=%d want 2 (source ws + project slug); removed=%v", n, removed)
	}
	if _, err := os.Stat(filepath.Join(wsRoot, sourceID)); !os.IsNotExist(err) {
		t.Fatalf("source storage still present")
	}
	if _, err := os.Stat(filepath.Join(wsRoot, aliveID)); err != nil {
		t.Fatalf("non-orphan source was deleted")
	}
	if _, err := os.Stat(filepath.Join(wsRoot, targetID)); err != nil {
		t.Fatalf("target storage was deleted")
	}
	if _, err := os.Stat(slugDir); !os.IsNotExist(err) {
		t.Fatalf("project slug still present")
	}
}
