package rebind

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Interittus13/cursor-rebind/internal/backup"
	"github.com/Interittus13/cursor-rebind/internal/discover"
	"github.com/Interittus13/cursor-rebind/internal/guard"
)

// PruneResult summarizes a prune dry-run or apply.
type PruneResult struct {
	Candidates []discover.PruneCandidate
	Removed    []string
	BackupID   string
	DryRun     bool
}

// PruneStale deletes safe stale Cursor storage leftovers (orphaned workspaceStorage
// dirs and empty ephemeral ~/.cursor/projects slugs). Always backs up trees when
// applying. Does not touch global chat headers or project source folders.
func PruneStale(inv *discover.Inventory, includeEmptyProjects, dryRun bool) (*PruneResult, error) {
	if inv == nil {
		return nil, fmt.Errorf("nil inventory")
	}
	cands := discover.CollectPruneCandidates(inv, includeEmptyProjects)
	res := &PruneResult{Candidates: cands, DryRun: dryRun}
	if dryRun || len(cands) == 0 {
		return res, nil
	}
	if err := guard.EnsureCursorClosed(); err != nil {
		return nil, err
	}

	id, absDir, man, err := backup.Create("prune stale Cursor storage")
	if err != nil {
		return nil, err
	}
	res.BackupID = id

	for _, c := range cands {
		if _, err := os.Stat(c.Path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return res, err
		}
		var logical string
		if c.Kind == discover.StaleOrphanWorkspace || c.Kind == discover.StaleEmptyWindow {
			logical = filepath.Join("prune", "workspaceStorage", c.Label)
		} else {
			logical = filepath.Join("prune", "projects", c.Label)
		}
		if err := backup.CopyTree(absDir, man, logical, c.Path); err != nil {
			return res, fmt.Errorf("backup %s: %w", c.Path, err)
		}
		if err := os.RemoveAll(c.Path); err != nil {
			return res, fmt.Errorf("remove %s: %w", c.Path, err)
		}
		res.Removed = append(res.Removed, c.Path)
	}
	if err := backup.WriteManifest(absDir, man); err != nil {
		return res, err
	}
	return res, nil
}
