package rebind

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/Interittus13/cursor-rebind/internal/paths"
	"github.com/Interittus13/cursor-rebind/internal/vscdb"
)

// workspaceURIMap is the VS Code-style URI object Cursor stores in maps (glass,
// composerData, workspaceMetadata). Prefer this over rebuilding the literals.
func workspaceURIMap(abs string) map[string]any {
	clean := filepath.Clean(abs)
	return map[string]any{
		"$mid":     1,
		"fsPath":   clean,
		"external": paths.FileURI(abs),
		"path":     filepath.ToSlash(clean),
		"scheme":   "file",
	}
}

func buildURI(absPath string) *vscdb.WorkspaceURI {
	clean := filepath.ToSlash(filepath.Clean(absPath))
	return &vscdb.WorkspaceURI{
		Scheme:   "file",
		Path:     clean,
		FsPath:   filepath.Clean(absPath),
		External: paths.FileURI(absPath),
	}
}

func displayPathFor(abs string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return abs
	}
	clean := filepath.Clean(abs)
	home = filepath.Clean(home)
	if clean == home {
		return "~"
	}
	if strings.HasPrefix(clean, home+string(filepath.Separator)) {
		return "~/" + filepath.ToSlash(clean[len(home)+1:])
	}
	return filepath.ToSlash(clean)
}

func uriFsPath(uri *vscdb.WorkspaceURI) string {
	if uri == nil {
		return ""
	}
	if uri.FsPath != "" {
		return uri.FsPath
	}
	return uri.Path
}

func mapFsPath(uri map[string]any) string {
	if uri == nil {
		return ""
	}
	if fp, _ := uri["fsPath"].(string); fp != "" {
		return fp
	}
	fp, _ := uri["path"].(string)
	return fp
}

// clearStuckFlagsInBlob resets aborted/generating composer state. Returns whether
// the blob was modified.
func clearStuckFlagsInBlob(blob map[string]any) bool {
	changed := false
	if st, _ := blob["status"].(string); st == "aborted" || st == "generating" || st == "error" {
		blob["status"] = "none"
		changed = true
	}
	if _, ok := blob["generatingBubbleIds"]; ok {
		blob["generatingBubbleIds"] = []any{}
		changed = true
	}
	return changed
}

// glassKeyPrefixes are ItemTable keys scoped to a workspace id in Agents Window.
func glassKeyPrefixes(wsID string) []string {
	return []string{
		"cursor/glass.tabs.v2/" + wsID,
		"cursor/glass.fileTab.viewState/" + wsID,
		"agentData.cacheStorage.agentEnvironment.slashMenuItems.v2.local.glass." + wsID,
	}
}

// glassTransferPrefixes are keys that should move (not only delete) when retiring
// a source workspace id onto the target.
func glassTransferPrefixes(wsID string) []string {
	return []string{
		"cursor/glass.tabs.v2/" + wsID,
		"agentData.cacheStorage.agentEnvironment.slashMenuItems.v2.local.glass." + wsID,
	}
}

func listItemKeysExactOrLike(db *sql.DB, prefix string) ([]string, error) {
	keys, err := vscdb.ListItemKeysLike(db, prefix+"%")
	if err != nil {
		return nil, err
	}
	if _, ok, _ := vscdb.GetItemRaw(db, prefix); ok {
		found := false
		for _, k := range keys {
			if k == prefix {
				found = true
				break
			}
		}
		if !found {
			keys = append(keys, prefix)
		}
	}
	return keys, nil
}
