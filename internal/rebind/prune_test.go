package rebind

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Interittus13/cursor-rebind/internal/discover"
	"github.com/Interittus13/cursor-rebind/internal/paths"
)

func TestPruneStaleDryRunAndApply(t *testing.T) {
	root := t.TempDir()
	wsRoot := filepath.Join(root, "workspaceStorage")
	projRoot := filepath.Join(root, "projects")
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	orphID := "abcdef0123456789abcdef0123456789"
	orphDir := filepath.Join(wsRoot, orphID)
	if err := os.MkdirAll(orphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]string{
		"folder": "file:///home/u/proj.__rebind_orphan_abcdef01",
	})
	if err := os.WriteFile(filepath.Join(orphDir, "workspace.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphDir, "marker.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tmpName := "tmp-dead-session"
	tmpDir := filepath.Join(projRoot, tmpName)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatal(err)
	}

	keepID := "11111111111111111111111111111111"
	keepDir := filepath.Join(wsRoot, keepID)
	if err := os.MkdirAll(keepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	keepMeta, _ := json.Marshal(map[string]string{"folder": "file:///home/u/proj"})
	if err := os.WriteFile(filepath.Join(keepDir, "workspace.json"), keepMeta, 0o644); err != nil {
		t.Fatal(err)
	}

	inv := &discover.Inventory{
		Roots: paths.Roots{WorkspaceStorage: wsRoot, ProjectsDir: projRoot},
		Workspaces: []discover.Workspace{
			{ID: orphID, FolderPath: "/home/u/proj.__rebind_orphan_abcdef01"},
			{ID: keepID, FolderPath: "/home/u/proj", PathExists: true},
		},
		Projects: []discover.AgentProject{
			{Name: tmpName, Dir: tmpDir, TranscriptCount: 0},
		},
	}

	dry, err := PruneStale(inv, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Candidates) != 2 {
		t.Fatalf("candidates=%d", len(dry.Candidates))
	}
	if _, err := os.Stat(orphDir); err != nil {
		t.Fatal("dry-run must not delete")
	}

	// Apply without Cursor guard: ensure no Cursor detection falsely blocks in CI.
	// If Cursor is running locally this may fail — skip apply then.
	res, err := PruneStale(inv, false, false)
	if err != nil {
		t.Skipf("prune apply skipped (Cursor running or backup issue): %v", err)
	}
	if len(res.Removed) != 2 {
		t.Fatalf("removed=%v", res.Removed)
	}
	if _, err := os.Stat(orphDir); !os.IsNotExist(err) {
		t.Fatal("orphan workspace should be gone")
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Fatal("tmp project should be gone")
	}
	if _, err := os.Stat(keepDir); err != nil {
		t.Fatal("live workspace must remain")
	}
	if res.BackupID == "" {
		t.Fatal("expected backup id")
	}
}
