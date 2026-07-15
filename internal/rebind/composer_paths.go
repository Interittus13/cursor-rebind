package rebind

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Interittus13/cursor-rebind/internal/paths"
	"github.com/Interittus13/cursor-rebind/internal/vscdb"
)

// rewriteComposerDiskPaths rewrites --from → --to inside composerData for chats that
// actually belong to this migrate. Agents Window uses composerData.workspaceIdentifier
// (not only headers); that field is taken from the (already rewritten) header so we
// never stamp unrelated shared-glass chats onto --to.
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

	headersByID := loadHeaderMap(db)
	repoURL := lookupTargetRepoURL(db, plan)
	ids := composerIDsNeedingPathRewrite(db, plan, headersByID)
	updated := 0
	for _, id := range ids {
		h := headersByID[id]
		// Empty stubs on --from are deleted from headers; don't rewrite their blobs
		// onto --to (would create ghost Agents rows).
		if composerIsEmpty(db, id, h.Name) {
			continue
		}
		ok, err := rewriteOneComposerData(db, id, plan, repoURL, h)
		if err != nil {
			return updated, err
		}
		if ok {
			updated++
		}
		if n, err := rewriteComposerSatellitePaths(db, id, plan); err != nil {
			return updated, err
		} else if n > 0 && !ok {
			updated++
		}
		_ = ensureGlassAgentTabState(db, plan, id)
	}
	return updated, nil
}

func loadHeaderMap(db *sql.DB) map[string]vscdb.ComposerMeta {
	out := map[string]vscdb.ComposerMeta{}
	var headers vscdb.ComposerHeaders
	if ok, _ := vscdb.GetItemJSON(db, "composer.composerHeaders", &headers); !ok {
		return out
	}
	for _, c := range headers.AllComposers {
		if c.ComposerID != "" {
			out[c.ComposerID] = c
		}
	}
	return out
}

func composerIDsNeedingPathRewrite(db *sql.DB, plan *Plan, headersByID map[string]vscdb.ComposerMeta) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		// Never rewrite a chat whose header clearly belongs to another folder.
		if h, ok := headersByID[id]; ok && !composerHeaderBelongsToPlan(h, plan) {
			if !composerDataMentionsFrom(db, id, plan) {
				return
			}
			// Header elsewhere but blob still has --from paths: rewrite paths only
			// via belongs check inside rewriteOneComposerData (WI follows header).
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
	// Only source-side inventory — target "Agent=N" often includes unrelated chats
	// that share a glass project and must not be stamped onto --to.
	for _, id := range plan.SourceInventory.AgentComposerIDs {
		add(id)
	}
	for _, id := range plan.SourceInventory.IDEComposerIDs {
		add(id)
	}
	return out
}

func composerHeaderBelongsToPlan(c vscdb.ComposerMeta, plan *Plan) bool {
	if c.WorkspaceIdentifier == nil {
		return false
	}
	sourceSet := toSet(plan.SourceWSIDs)
	if sourceSet[c.WorkspaceIdentifier.ID] || c.WorkspaceIdentifier.ID == plan.TargetWSID {
		return true
	}
	fp := uriFsPath(c.WorkspaceIdentifier.URI)
	if fp == "" {
		return false
	}
	clean := filepath.Clean(fp)
	return matches(clean, plan.From, plan.Mode) || clean == filepath.Clean(plan.To)
}

func composerDataMentionsFrom(db *sql.DB, composerID string, plan *Plan) bool {
	raw, ok, err := vscdb.GetDiskKVRaw(db, "composerData:"+composerID)
	if err != nil || !ok {
		return false
	}
	text := string(raw)
	fromSlash := filepath.ToSlash(filepath.Clean(plan.From))
	return strings.Contains(text, fromSlash) || strings.Contains(text, filepath.Clean(plan.From))
}

func rewriteOneComposerData(db *sql.DB, composerID string, plan *Plan, repoURL string, header vscdb.ComposerMeta) (bool, error) {
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

	// Workspace identity follows the header (source of truth after rewriteHeaders).
	// Falling back to --to only when this chat is clearly part of the migrate and
	// has no usable header URI.
	bindPath := ""
	bindID := ""
	if header.ComposerID != "" && header.WorkspaceIdentifier != nil {
		bindID = header.WorkspaceIdentifier.ID
		bindPath = uriFsPath(header.WorkspaceIdentifier.URI)
	}
	belongs := composerHeaderBelongsToPlan(header, plan)
	if !belongs {
		// Path-only rewrite for --from remnants; do not change workspace binding.
		if repos, exists := blob["trackedGitRepos"]; exists {
			blob["trackedGitRepos"] = remapTrackedGitRepos(repos, plan.From, plan.To, plan.Mode)
		}
		_ = clearStuckFlagsInBlob(blob)
	} else {
		if repos, exists := blob["trackedGitRepos"]; exists {
			blob["trackedGitRepos"] = remapTrackedGitRepos(repos, plan.From, plan.To, plan.Mode)
		} else {
			blob["trackedGitRepos"] = []any{}
		}
		// Always bind migrate members to --to. Header is only used to detect
		// "already on another project" (keep) vs "on --from / --to" (force --to).
		// Previously we copied bindPath even when it was still --from, which left
		// Agents showing the old machine path and an empty transcript chrome.
		targetPath := plan.To
		targetID := plan.TargetWSID
		targetRepoURL := repoURL
		if bindPath != "" &&
			filepath.Clean(bindPath) != filepath.Clean(plan.To) &&
			!matches(bindPath, plan.From, plan.Mode) {
			// Header already on another project — keep that identity.
			targetPath = bindPath
			targetID = bindID
			targetRepoURL = ""
		}
		blob["trackedGitRepos"] = enrichTrackedGitRepos(blob["trackedGitRepos"], targetPath, targetRepoURL)
		if targetID != "" {
			blob["workspaceIdentifier"] = map[string]any{
				"id":  targetID,
				"uri": workspaceURIMap(targetPath),
			}
		}
		// Agents Window only hydrates transcript chrome for agent-mode composers.
		// Chats created as IDE "chat" (unifiedMode=chat, isAgentic=false) list in
		// the sidebar but open empty until promoted.
		promoteBlobForAgentsWindow(blob)
		_ = clearStuckFlagsInBlob(blob)
	}

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

func promoteBlobForAgentsWindow(blob map[string]any) {
	if blob == nil {
		return
	}
	blob["isAgentic"] = true
	blob["agentBackend"] = "cursor-agent"
	blob["unifiedMode"] = "agent"
	if fm, _ := blob["forceMode"].(string); fm == "" || fm == "chat" {
		blob["forceMode"] = "edit"
	}
	if _, ok := blob["hasLoaded"]; !ok {
		blob["hasLoaded"] = true
	}
}

func enrichTrackedGitRepos(v any, to, repoURL string) []any {
	toClean := filepath.Clean(to)
	arr, _ := v.([]any)
	if len(arr) == 0 {
		entry := map[string]any{"repoPath": toClean, "branches": []any{}}
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
	return normalizeTrackedGitRepos(out)
}

// normalizeTrackedGitRepos ensures every entry has branches (possibly empty).
// Agents Window glass code does entry.branches.map(...) with no null check and
// crashes with "Cannot read properties of undefined (reading 'map')" when we
// write [{repoPath}] without branches — chat lists but opens empty.
func normalizeTrackedGitRepos(v any) []any {
	arr, _ := v.([]any)
	if len(arr) == 0 {
		return arr
	}
	out := make([]any, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		nm := cloneMap(m)
		if _, ok := nm["branches"]; !ok {
			nm["branches"] = []any{}
		} else if nm["branches"] == nil {
			nm["branches"] = []any{}
		}
		out = append(out, nm)
	}
	return out
}

// rewriteComposerSatellitePaths rewrites --from → --to inside bubble / ofsContent
// rows for one composer. composerData alone is not enough for Agents tooling.
func rewriteComposerSatellitePaths(db *sql.DB, composerID string, plan *Plan) (int, error) {
	if plan == nil || plan.Mode != ModeExact || composerID == "" {
		return 0, nil
	}
	updated := 0
	prefixes := []string{
		"bubbleId:" + composerID + ":",
		"ofsContent:" + composerID + ":",
		"checkpointId:" + composerID + ":",
	}
	for _, prefix := range prefixes {
		rows, err := db.Query(`SELECT key, value FROM cursorDiskKV WHERE key LIKE ?`, prefix+"%")
		if err != nil {
			return updated, err
		}
		type kv struct {
			key, val string
		}
		var batch []kv
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				_ = rows.Close()
				return updated, err
			}
			batch = append(batch, kv{k, v})
		}
		_ = rows.Close()
		for _, item := range batch {
			newKey := rewriteEmbeddedPath(item.key, plan.From, plan.To, plan.Mode)
			newVal := rewriteEmbeddedPath(item.val, plan.From, plan.To, plan.Mode)
			if newKey == item.key && newVal == item.val {
				continue
			}
			if _, err := db.Exec(`INSERT INTO cursorDiskKV(key, value) VALUES(?, ?)
				ON CONFLICT(key) DO UPDATE SET value=excluded.value`, newKey, newVal); err != nil {
				return updated, err
			}
			if newKey != item.key {
				_, _ = db.Exec(`DELETE FROM cursorDiskKV WHERE key = ?`, item.key)
			}
			updated++
		}
	}
	return updated, nil
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
	return normalizeTrackedGitRepos(out)
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
