package rebind

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Interittus13/cursor-rebind/internal/paths"
)

// purgeSourceStorage removes orphaned source workspaceStorage directories and any
// leftover ~/.cursor/projects/--from slug after a successful exact migrate/repair.
// It never deletes TargetWSID or the on-disk project folder.
func purgeSourceStorage(wsRoot, projectsDir string, plan *Plan) (purged int, removed []string, err error) {
	if plan == nil {
		return 0, nil, nil
	}
	for _, sid := range plan.SourceWSIDs {
		if sid == "" || (plan.TargetWSID != "" && sid == plan.TargetWSID) {
			continue
		}
		dir := filepath.Join(wsRoot, sid)
		if _, statErr := os.Stat(dir); statErr != nil {
			continue
		}
		if !workspaceJSONIsOrphaned(dir) {
			continue
		}
		if workspaceJSONFolderEquals(dir, plan.To) {
			continue
		}
		if remErr := os.RemoveAll(dir); remErr != nil {
			return purged, removed, remErr
		}
		purged++
		removed = append(removed, dir)
	}

	if projectsDir != "" && plan.ProjectFrom != "" && plan.ProjectFrom != plan.ProjectTo {
		src := filepath.Join(projectsDir, plan.ProjectFrom)
		if _, statErr := os.Stat(src); statErr == nil {
			if remErr := os.RemoveAll(src); remErr != nil {
				return purged, removed, remErr
			}
			purged++
			removed = append(removed, src)
		}
	}
	return purged, removed, nil
}

func workspaceJSONIsOrphaned(wsDir string) bool {
	raw, err := os.ReadFile(filepath.Join(wsDir, "workspace.json"))
	if err != nil {
		return false
	}
	var meta struct {
		Folder string `json:"folder"`
	}
	if json.Unmarshal(raw, &meta) != nil || meta.Folder == "" {
		return false
	}
	fp := paths.PathFromFileURI(meta.Folder)
	return strings.Contains(meta.Folder, ".__rebind_orphan_") || strings.Contains(fp, ".__rebind_orphan_")
}

func workspaceJSONFolderEquals(wsDir, folder string) bool {
	raw, err := os.ReadFile(filepath.Join(wsDir, "workspace.json"))
	if err != nil {
		return false
	}
	var meta struct {
		Folder string `json:"folder"`
	}
	if json.Unmarshal(raw, &meta) != nil || meta.Folder == "" {
		return false
	}
	return filepath.Clean(paths.PathFromFileURI(meta.Folder)) == filepath.Clean(folder)
}
