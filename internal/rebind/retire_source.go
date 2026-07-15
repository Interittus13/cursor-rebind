package rebind

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Interittus13/cursor-rebind/internal/paths"
	"github.com/Interittus13/cursor-rebind/internal/vscdb"
)

// retireSourceIdentity removes leftover --from surface so Agents Window cannot
// keep listing chats under the old folder name ("On mover") after headers already
// point at --to. Cursor recreates that bucket whenever:
//   - workspaceStorage still has folder == --from
//   - workspaceMetadata.entries still advertise --from
//   - recentlyOpenedPathsList still contains --from
//   - ~/.cursor/projects/<slug-from> still exists while --from is on disk
func retireSourceIdentity(globalDB, wsRoot, projectsDir string, plan *Plan) error {
	if plan == nil || plan.Mode != ModeExact {
		return nil
	}
	// Always discover leftover --from / orphan workspace ids, even when headers
	// already point at --to (common repair case).
	plan.SourceWSIDs = mergeWorkspaceIDs(plan.SourceWSIDs, findFolderWorkspaceIDs(wsRoot, plan.From))
	if err := orphanWorkspaceFolders(wsRoot, plan.From, plan.TargetWSID); err != nil {
		return fmt.Errorf("orphan --from workspaces: %w", err)
	}
	db, err := vscdb.OpenReadWrite(globalDB)
	if err != nil {
		return err
	}
	defer func() {
		_ = vscdb.CheckpointWAL(db)
		_ = db.Close()
	}()
	if err := rewriteWorkspaceMetadataEntries(db, plan); err != nil {
		return fmt.Errorf("workspaceMetadata: %w", err)
	}
	if err := scrubRecentlyOpened(db, plan); err != nil {
		return fmt.Errorf("recentlyOpened: %w", err)
	}
	if err := scrubGlassSourceKeys(db, plan); err != nil {
		return fmt.Errorf("glass source keys: %w", err)
	}
	_ = retireProjectsSlug(projectsDir, plan)
	return nil
}

func mergeWorkspaceIDs(a, b []string) []string {
	seen := toSet(a)
	out := append([]string{}, a...)
	for _, id := range b {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func findFolderWorkspaceIDs(wsRoot, folder string) []string {
	folder = filepath.Clean(folder)
	entries, err := os.ReadDir(wsRoot)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(wsRoot, e.Name(), "workspace.json"))
		if err != nil {
			continue
		}
		var meta map[string]any
		if json.Unmarshal(raw, &meta) != nil {
			continue
		}
		cur, _ := meta["folder"].(string)
		fp := filepath.Clean(paths.PathFromFileURI(cur))
		if fp == folder || strings.HasPrefix(fp, folder+".__rebind_orphan_") {
			out = append(out, e.Name())
		}
	}
	return out
}

// orphanWorkspaceFolders rewrites workspace.json for every workspaceStorage
// entry whose folder URI matches `folder` (except keepID). Point them at a
// non-existent path so Cursor stops treating them as the live project root.
func orphanWorkspaceFolders(wsRoot, folder, keepID string) error {
	folder = filepath.Clean(folder)
	entries, err := os.ReadDir(wsRoot)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == keepID {
			continue
		}
		metaPath := filepath.Join(wsRoot, e.Name(), "workspace.json")
		raw, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var meta map[string]any
		if json.Unmarshal(raw, &meta) != nil {
			continue
		}
		cur, _ := meta["folder"].(string)
		if filepath.Clean(paths.PathFromFileURI(cur)) != folder {
			continue
		}
		if strings.Contains(cur, ".__rebind_orphan_") {
			continue
		}
		orphan := folder + ".__rebind_orphan_" + e.Name()[:min(8, len(e.Name()))]
		meta["folder"] = paths.FileURI(orphan)
		out, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(metaPath, out, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func rewriteWorkspaceMetadataEntries(db *sql.DB, plan *Plan) error {
	raw, ok, err := vscdb.GetItemRaw(db, "workspaceMetadata.entries")
	if err != nil || !ok {
		return err
	}
	var wrap map[string]any
	if json.Unmarshal(raw, &wrap) != nil {
		return nil
	}
	entries, _ := wrap["entries"].([]any)
	if len(entries) == 0 {
		return nil
	}
	sourceSet := toSet(plan.SourceWSIDs)
	fromClean := filepath.Clean(plan.From)
	toClean := filepath.Clean(plan.To)
	toDisplay := displayPathFor(plan.To)
	fromBase := filepath.Base(plan.From)
	changed := false
	out := make([]any, 0, len(entries))
	seenTarget := false
	for _, item := range entries {
		e, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		wid, _ := e["workspaceId"].(string)
		folderURI, _ := e["folderUri"].(string)
		fp := paths.PathFromFileURI(folderURI)
		entryDisplay, _ := e["displayPath"].(string)
		fpClean := filepath.Clean(fp)
		// Keep --to first so short basenames ("ai") cannot drop the target via
		// display-path segment matching.
		if wid == plan.TargetWSID || fpClean == toClean {
			seenTarget = true
			if ensureMetadataTrackedRepo(e, plan.To) {
				changed = true
			}
			out = append(out, e)
			continue
		}
		// Drop metadata that still advertises --from as a live Agents root.
		if sourceSet[wid] || matches(fp, plan.From, plan.Mode) || fpClean == fromClean ||
			strings.HasPrefix(fpClean, fromClean+".__rebind_orphan_") {
			changed = true
			continue
		}
		// Basename segment match only for longer names ("mover"); "ai" matches
		// both from and to displays and would thrash the target entry.
		if basenameLongEnough(fromBase) && displayPathHasSegment(entryDisplay, fromBase) {
			changed = true
			continue
		}
		out = append(out, e)
	}
	if !seenTarget && plan.TargetWSID != "" {
		out = append(out, map[string]any{
			"workspaceId": plan.TargetWSID,
			"displayPath": toDisplay,
			"folderUri":   paths.FileURI(plan.To),
			"paths": []any{
				map[string]any{
					"uri":         workspaceURIMap(plan.To),
					"displayPath": toDisplay,
				},
			},
			"trackedGitRepos": []any{
				map[string]any{"repoPath": toClean, "branches": []any{}},
			},
			"worktreeInfo": map[string]any{"isWorktree": false},
		})
		changed = true
	}
	if !changed {
		return nil
	}
	wrap["entries"] = out
	return vscdb.SetItemJSON(db, "workspaceMetadata.entries", wrap)
}

// displayPathHasSegment reports whether displayPath contains fromBase as a full
// path segment (not a substring). "ai" must not match "Arpit".
func displayPathHasSegment(display, segment string) bool {
	if display == "" || segment == "" {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(display), "/") {
		if part == segment {
			return true
		}
	}
	return false
}

func ensureMetadataTrackedRepo(e map[string]any, abs string) bool {
	abs = filepath.Clean(abs)
	repos, _ := e["trackedGitRepos"].([]any)
	for _, r := range repos {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if filepath.Clean(fmt.Sprint(m["repoPath"])) == abs {
			return false
		}
	}
	e["trackedGitRepos"] = append(repos, map[string]any{
		"repoPath": abs,
		"branches": []any{},
	})
	return true
}

func scrubRecentlyOpened(db *sql.DB, plan *Plan) error {
	raw, ok, err := vscdb.GetItemRaw(db, "history.recentlyOpenedPathsList")
	if err != nil || !ok {
		return err
	}
	var wrap map[string]any
	if json.Unmarshal(raw, &wrap) != nil {
		return nil
	}
	entries, _ := wrap["entries"].([]any)
	fromURI := paths.FileURI(plan.From)
	toURI := paths.FileURI(plan.To)
	out := make([]any, 0, len(entries))
	hasTo := false
	changed := false
	for _, item := range entries {
		e, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		folder, _ := e["folderUri"].(string)
		if folder == fromURI || matches(paths.PathFromFileURI(folder), plan.From, plan.Mode) {
			changed = true
			continue
		}
		if folder == toURI {
			hasTo = true
		}
		out = append(out, e)
	}
	if !hasTo {
		out = append([]any{map[string]any{"folderUri": toURI}}, out...)
		changed = true
	}
	if !changed {
		return nil
	}
	wrap["entries"] = out
	return vscdb.SetItemJSON(db, "history.recentlyOpenedPathsList", wrap)
}

func scrubGlassSourceKeys(db *sql.DB, plan *Plan) error {
	for _, sid := range plan.SourceWSIDs {
		if sid == "" || sid == plan.TargetWSID {
			continue
		}
		prefixes := glassKeyPrefixes(sid)
		for _, prefix := range prefixes {
			keys, err := listItemKeysExactOrLike(db, prefix)
			if err != nil {
				return err
			}
			for _, k := range keys {
				_ = vscdb.DeleteItem(db, k)
			}
		}
	}
	// Drop localRepoBranchRecency for --from if present.
	_ = vscdb.DeleteItem(db, "glass.localRepoBranchRecency."+filepath.Clean(plan.From))
	_ = vscdb.DeleteItem(db, "glass.localRepoBranchRecency."+filepath.ToSlash(filepath.Clean(plan.From)))
	_ = scrubGlassSidebarSettings(db, plan)
	return nil
}

// scrubGlassSidebarSettings removes --from workspace ids from the Agents
// sidebar ordering so Cursor cannot keep an "ai" section bound to the old path.
func scrubGlassSidebarSettings(db *sql.DB, plan *Plan) error {
	raw, ok, err := vscdb.GetItemRaw(db, "cursor/glassSidebarSettings")
	if err != nil || !ok {
		return err
	}
	sourceSet := toSet(plan.SourceWSIDs)
	if len(sourceSet) == 0 || plan.TargetWSID == "" {
		return nil
	}
	replace := map[string]string{}
	for sid := range sourceSet {
		if sid == "" || sid == plan.TargetWSID {
			continue
		}
		replace["workspace:"+sid] = "workspace:"+plan.TargetWSID
		replace[sid] = plan.TargetWSID
	}
	before := string(raw)
	after := before
	for old, neu := range replace {
		after = strings.ReplaceAll(after, `"`+old+`"`, `"`+neu+`"`)
	}
	if after == before {
		return nil
	}
	return vscdb.SetItemRaw(db, "cursor/glassSidebarSettings", []byte(after))
}

func retireProjectsSlug(projectsDir string, plan *Plan) error {
	if projectsDir == "" || plan.ProjectFrom == "" {
		return nil
	}
	src := filepath.Join(projectsDir, plan.ProjectFrom)
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	dst := filepath.Join(projectsDir, plan.ProjectTo)
	// If target already has transcripts, only remove empty-ish source leftovers
	// so Cursor stops advertising the old Agents project slug.
	if _, err := os.Stat(dst); err == nil {
		if projectSlugIsStub(src) {
			return os.RemoveAll(src)
		}
		return nil
	}
	return os.Rename(src, dst)
}

func projectSlugIsStub(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	for _, e := range entries {
		name := e.Name()
		if name == "agent-transcripts" || name == "agent-tools" || name == "terminals" {
			return false
		}
	}
	return true
}
