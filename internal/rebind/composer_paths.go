package rebind

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Interittus13/cursor-rebind/internal/paths"
	"github.com/Interittus13/cursor-rebind/internal/vscdb"
)

// rewriteComposerDiskPaths rewrites --from → --to inside composerData blobs for
// chats bound to this migrate. Agents Window groups chats using:
//   - composerData.workspaceIdentifier (drives "On <name>" / environment)
//   - trackedGitRepos.repoUrl (drives github owner/repo Repositories buckets)
// Headers alone are not enough — missing workspaceIdentifier leaves the chat
// under the old agent root even when IDE already shows both chats.
func rewriteComposerDiskPaths(globalDB string, plan *Plan) (int, error) {
	if plan == nil || plan.Mode != ModeExact {
		return 0, nil
	}
	db, err := vscdb.OpenReadWrite(globalDB)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = vscdb.CheckpointWAL(db)
		_ = db.Close()
	}()

	repoURL := lookupTargetRepoURL(db, plan)
	ids := composerIDsNeedingPathRewrite(db, plan)
	updated := 0
	for _, id := range ids {
		ok, err := rewriteOneComposerData(db, id, plan, repoURL)
		if err != nil {
			return updated, err
		}
		if ok {
			updated++
		}
	}
	return updated, nil
}

func composerIDsNeedingPathRewrite(db *sql.DB, plan *Plan) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, id := range headerComposerIDsForPlan(db, plan) {
		add(id)
	}
	for _, id := range transcriptComposerIDs(plan) {
		add(id)
	}
	for _, id := range plan.SourceInventory.AgentComposerIDs {
		add(id)
	}
	for _, id := range plan.SourceInventory.IDEComposerIDs {
		add(id)
	}
	for _, id := range plan.TargetInventory.AgentComposerIDs {
		add(id)
	}
	for _, id := range plan.TargetInventory.IDEComposerIDs {
		add(id)
	}
	return out
}

func rewriteOneComposerData(db *sql.DB, composerID string, plan *Plan, repoURL string) (bool, error) {
	raw, ok, err := vscdb.GetDiskKVRaw(db, "composerData:"+composerID)
	if err != nil || !ok {
		return false, err
	}
	before := string(raw)
	var blob map[string]any
	if err := json.Unmarshal(raw, &blob); err != nil {
		return false, nil
	}
	rewritten := rewriteValuePaths(blob, plan.From, plan.To, plan.Mode)
	m, ok := rewritten.(map[string]any)
	if !ok {
		return false, nil
	}
	blob = m
	if repos, exists := blob["trackedGitRepos"]; exists {
		blob["trackedGitRepos"] = remapTrackedGitRepos(repos, plan.From, plan.To, plan.Mode)
	} else {
		blob["trackedGitRepos"] = []any{}
	}
	blob["trackedGitRepos"] = enrichTrackedGitRepos(blob["trackedGitRepos"], plan.To, repoURL)
	// Agents Window binds "On <name>" / Repositories buckets via composerData.workspaceIdentifier
	// (not only composer.composerHeaders). Missing this leaves the chat on the old agent root.
	if plan.TargetWSID != "" {
		blob["workspaceIdentifier"] = map[string]any{
			"id":  plan.TargetWSID,
			"uri": workspaceURIMap(plan.To),
		}
	}
	_ = clearStuckFlagsInBlob(blob)
	out, err := json.Marshal(blob)
	if err != nil {
		return false, err
	}
	if string(out) == before {
		return false, nil
	}
	_, err = db.Exec(`INSERT INTO cursorDiskKV(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "composerData:"+composerID, string(out))
	if err != nil {
		return false, err
	}
	return true, nil
}

func enrichTrackedGitRepos(v any, to, repoURL string) []any {
	toClean := filepath.Clean(to)
	arr, _ := v.([]any)
	if len(arr) == 0 {
		entry := map[string]any{"repoPath": toClean}
		if repoURL != "" {
			entry["repoUrl"] = repoURL
		}
		return []any{entry}
	}
	out := make([]any, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		nm := cloneMap(m)
		if rp, _ := nm["repoPath"].(string); filepath.Clean(rp) == toClean || rp == "" {
			nm["repoPath"] = toClean
			if repoURL != "" {
				if cur, _ := nm["repoUrl"].(string); cur == "" {
					nm["repoUrl"] = repoURL
				}
			}
		}
		out = append(out, nm)
	}
	return out
}

func lookupTargetRepoURL(db *sql.DB, plan *Plan) string {
	if plan == nil {
		return ""
	}
	toClean := filepath.Clean(plan.To)
	toURI := paths.FileURI(plan.To)

	if raw, ok, _ := vscdb.GetItemRaw(db, "workspaceMetadata.entries"); ok {
		var wrap map[string]any
		if json.Unmarshal(raw, &wrap) == nil {
			entries, _ := wrap["entries"].([]any)
			for _, item := range entries {
				e, _ := item.(map[string]any)
				if e == nil {
					continue
				}
				wid, _ := e["workspaceId"].(string)
				folder, _ := e["folderUri"].(string)
				fp := filepath.Clean(paths.PathFromFileURI(folder))
				if wid != plan.TargetWSID && folder != toURI && fp != toClean {
					continue
				}
				repos, _ := e["trackedGitRepos"].([]any)
				for _, r := range repos {
					m, _ := r.(map[string]any)
					if m == nil {
						continue
					}
					if u, _ := m["repoUrl"].(string); u != "" {
						return u
					}
				}
			}
		}
	}
	if raw, ok, _ := vscdb.GetItemRaw(db, "cursor/glass.additionalProjects"); ok {
		var projects []map[string]any
		if json.Unmarshal(raw, &projects) == nil {
			for _, p := range projects {
				wi, _ := p["workspaceIdentifier"].(map[string]any)
				id, _ := wi["id"].(string)
				if id != plan.TargetWSID {
					continue
				}
				if urls, ok := p["repoUrls"].([]any); ok {
					for _, u := range urls {
						if s, _ := u.(string); s != "" {
							return s
						}
					}
				}
			}
		}
	}
	if raw, ok, _ := vscdb.GetItemRaw(db, "repositoryTracker.paths"); ok {
		var track map[string]map[string]any
		if json.Unmarshal(raw, &track) == nil {
			for key, entry := range track {
				lp, _ := entry["localPath"].(string)
				if lp == toURI || filepath.Clean(paths.PathFromFileURI(lp)) == toClean {
					return key
				}
			}
		}
	}
	return ""
}

func remapTrackedGitRepos(v any, from, to string, mode Mode) []any {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		if ok {
			return arr
		}
		return nil
	}
	fromClean := filepath.Clean(from)
	toClean := filepath.Clean(to)
	byPath := map[string]map[string]any{}
	var order []string
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		nm := cloneMap(m)
		rp, _ := nm["repoPath"].(string)
		if rp != "" {
			clean := filepath.Clean(rp)
			if matches(clean, from, mode) || clean == fromClean {
				nm["repoPath"] = toClean
			} else if mode == ModeExact && clean == toClean {
				nm["repoPath"] = toClean
			} else {
				nm["repoPath"] = rewritePath(clean, from, to, mode)
			}
		}
		key, _ := nm["repoPath"].(string)
		key = filepath.Clean(key)
		if key == "" {
			continue
		}
		// Drop any entry that still points at --from after remap.
		if matches(key, from, mode) && key != toClean {
			continue
		}
		if existing, ok := byPath[key]; ok {
			mergeTrackedGitEntry(existing, nm)
			continue
		}
		byPath[key] = nm
		order = append(order, key)
	}
	out := make([]any, 0, len(order))
	for _, k := range order {
		out = append(out, byPath[k])
	}
	return out
}

func mergeTrackedGitEntry(dst, src map[string]any) {
	for _, field := range []string{"remoteUrl", "repoUrl", "branchName", "repoName", "commitHash"} {
		if _, ok := dst[field]; !ok {
			if v, ok := src[field]; ok {
				dst[field] = v
			}
		}
	}
	for _, field := range []string{"branches", "worktrees"} {
		da, dok := dst[field].([]any)
		sa, sok := src[field].([]any)
		if !sok {
			continue
		}
		if !dok {
			dst[field] = sa
			continue
		}
		seen := map[string]bool{}
		for _, x := range da {
			seen[stableJSON(x)] = true
		}
		for _, x := range sa {
			k := stableJSON(x)
			if !seen[k] {
				da = append(da, x)
				seen[k] = true
			}
		}
		dst[field] = da
	}
}

func stableJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func rewriteValuePaths(v any, from, to string, mode Mode) any {
	switch x := v.(type) {
	case string:
		return rewriteEmbeddedPath(x, from, to, mode)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			nk := rewriteEmbeddedPath(k, from, to, mode)
			out[nk] = rewriteValuePaths(val, from, to, mode)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = rewriteValuePaths(val, from, to, mode)
		}
		return out
	default:
		return v
	}
}

func rewriteEmbeddedPath(s, from, to string, mode Mode) string {
	fromClean := filepath.Clean(from)
	toClean := filepath.Clean(to)
	fromSlash := filepath.ToSlash(fromClean)
	toSlash := filepath.ToSlash(toClean)
	if mode == ModeExact {
		if !strings.Contains(s, fromSlash) && !strings.Contains(s, fromClean) {
			return s
		}
		s = strings.ReplaceAll(s, fromSlash, toSlash)
		if fromClean != fromSlash {
			s = strings.ReplaceAll(s, fromClean, toClean)
		}
		return s
	}
	// Prefix: only remapped via rewritePath for whole-path strings.
	if matches(s, from, mode) {
		return rewritePath(s, from, to, mode)
	}
	if matches(filepath.Clean(s), from, mode) {
		return rewritePath(s, from, to, mode)
	}
	if strings.HasPrefix(s, fromSlash+"/") || s == fromSlash {
		return toSlash + strings.TrimPrefix(s, fromSlash)
	}
	return s
}
