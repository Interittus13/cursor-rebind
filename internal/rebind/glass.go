package rebind

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Interittus13/cursor-rebind/internal/vscdb"
)

// rewriteGlassAgentIdentity moves Agents Window identity with IDE chats:
// localAgentProjects, glass.tabs.v2 workspace keys, slash-menu cache, additionalProjects.
// primaryComposerID, when set, is written to cursor/glass.selectedAgent.
func rewriteGlassAgentIdentity(globalDB string, plan *Plan, primaryComposerID string) (projectsUpdated, keysMoved int, err error) {
	db, err := vscdb.OpenReadWrite(globalDB)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		_ = vscdb.CheckpointWAL(db)
		_ = db.Close()
	}()

	projectsUpdated, err = rewriteGlassLocalProjects(db, plan)
	if err != nil {
		return projectsUpdated, 0, err
	}
	keysMoved, err = transferGlassWorkspaceKeys(db, plan)
	if err != nil {
		return projectsUpdated, keysMoved, err
	}
	if n, err := rewriteGlassAdditionalProjects(db, plan); err != nil {
		return projectsUpdated, keysMoved, err
	} else {
		keysMoved += n
	}
	if primaryComposerID != "" {
		// Cursor stores this as a bare UUID string (not JSON-quoted).
		if err := vscdb.SetItemRaw(db, "cursor/glass.selectedAgent", []byte(primaryComposerID)); err != nil {
			return projectsUpdated, keysMoved, err
		}
	}
	return projectsUpdated, keysMoved, nil
}

func rewriteGlassLocalProjects(db *sql.DB, plan *Plan) (int, error) {
	raw, ok, err := vscdb.GetItemRaw(db, "glass.localAgentProjects.v1")
	if err != nil || !ok {
		return 0, err
	}
	var projects []map[string]any
	if json.Unmarshal(raw, &projects) != nil {
		return 0, nil
	}

	sourceSet := toSet(plan.SourceWSIDs)
	targetURI := workspaceURIMap(plan.To)
	toClean := filepath.Clean(plan.To)
	changed := 0
	for _, p := range projects {
		ws, _ := p["workspace"].(map[string]any)
		if ws == nil {
			continue
		}
		// Retag only projects whose workspace already belongs to this migrate.
		// Never retag a shared glass project solely because a member composer is
		// in the migrate set — that steals foreign chats (e.g. cursor-rebind)
		// when migrating a different folder (e.g. ai).
		hit := false
		if id, _ := ws["id"].(string); id != "" && (sourceSet[id] || id == plan.TargetWSID) {
			hit = true
		}
		if uri, _ := ws["uri"].(map[string]any); uri != nil {
			if fp := mapFsPath(uri); fp != "" {
				clean := filepath.Clean(fp)
				if matches(clean, plan.From, plan.Mode) || (plan.Mode == ModeExact && clean == toClean) {
					hit = true
				}
				if glassProjectMatchesFrom(clean, plan.From, plan.Mode) {
					hit = true
				}
				if strings.Contains(clean, ".__rebind_orphan_") &&
					glassProjectMatchesFrom(strings.Split(clean, ".__rebind_orphan_")[0], plan.From, plan.Mode) {
					hit = true
				}
			}
		}
		if !hit {
			continue
		}
		ws["id"] = plan.TargetWSID
		ws["uri"] = targetURI
		p["workspace"] = ws
		changed++
	}

	// Re-home migrate members that still sit in a foreign glass project.
	n, err := ensureGlassMembershipForPlan(db, plan, &projects)
	if err != nil {
		return changed, err
	}
	changed += n

	// Drop empty --from stubs from membership so they cannot pull the shared
	// project back onto the old machine path in Agents Window.
	if n, err := purgeGlassEmptySourceMembers(db, plan); err != nil {
		return changed, err
	} else {
		changed += n
	}

	if changed == 0 {
		return 0, nil
	}
	if err := vscdb.SetItemJSON(db, "glass.localAgentProjects.v1", projects); err != nil {
		return changed, err
	}
	return changed, nil
}

// purgeGlassEmptySourceMembers removes membership rows for empty stubs whose
// headers still (or recently) advertised --from / source workspace ids.
func purgeGlassEmptySourceMembers(db *sql.DB, plan *Plan) (int, error) {
	mraw, mok, err := vscdb.GetItemRaw(db, "glass.localAgentProjectMembership.v1")
	if err != nil || !mok {
		return 0, err
	}
	var mem map[string]string
	if json.Unmarshal(mraw, &mem) != nil {
		return 0, nil
	}
	headers := loadHeaderMap(db)
	sourceSet := toSet(plan.SourceWSIDs)
	changed := 0
	for cid := range mem {
		h, ok := headers[cid]
		empty := composerIsEmpty(db, cid, h.Name)
		if !empty {
			continue
		}
		drop := false
		if !ok {
			// Orphan membership with no header — leave alone unless composerData
			// is clearly on --from.
			if composerDataMentionsFrom(db, cid, plan) {
				drop = true
			}
		} else if h.WorkspaceIdentifier != nil {
			if sourceSet[h.WorkspaceIdentifier.ID] {
				drop = true
			}
			if fp := uriFsPath(h.WorkspaceIdentifier.URI); fp != "" &&
				(matches(fp, plan.From, plan.Mode) || filepath.Clean(fp) == filepath.Clean(plan.From)) {
				drop = true
			}
			if h.WorkspaceIdentifier.ID == plan.TargetWSID {
				drop = true // empty stub on --to
			}
		}
		if drop {
			delete(mem, cid)
			changed++
		}
	}
	if changed == 0 {
		return 0, nil
	}
	if err := vscdb.SetItemJSON(db, "glass.localAgentProjectMembership.v1", mem); err != nil {
		return changed, err
	}
	return changed, nil
}

// ensureGlassMembershipForPlan moves composers that belong to this migrate onto
// a glass local project whose workspace is --to, creating one if needed.
func ensureGlassMembershipForPlan(db *sql.DB, plan *Plan, projects *[]map[string]any) (int, error) {
	mraw, mok, err := vscdb.GetItemRaw(db, "glass.localAgentProjectMembership.v1")
	if err != nil || !mok {
		return 0, err
	}
	var mem map[string]string
	if json.Unmarshal(mraw, &mem) != nil {
		return 0, nil
	}
	byID := map[string]map[string]any{}
	for _, p := range *projects {
		if id, _ := p["id"].(string); id != "" {
			byID[id] = p
		}
	}
	headers := loadHeaderMap(db)
	targetURI := workspaceURIMap(plan.To)
	changed := 0

	needProject := func() string {
		pid := dedicatedGlassProjectID(plan)
		for i, p := range *projects {
			if id, _ := p["id"].(string); id == pid {
				ws, _ := p["workspace"].(map[string]any)
				if ws == nil {
					ws = map[string]any{}
				}
				ws["id"] = plan.TargetWSID
				ws["uri"] = targetURI
				(*projects)[i]["workspace"] = ws
				(*projects)[i]["name"] = filepath.Base(plan.To)
				return pid
			}
		}
		*projects = append(*projects, map[string]any{
			"id":   pid,
			"name": filepath.Base(plan.To),
			"workspace": map[string]any{
				"id":  plan.TargetWSID,
				"uri": targetURI,
			},
		})
		return pid
	}

	for _, cid := range composersNeedingGlassTo(plan, headers) {
		want := dedicatedGlassProjectID(plan)
		if mem[cid] == want {
			continue
		}
		pid := needProject()
		if mem[cid] != pid {
			mem[cid] = pid
			changed++
		}
		byID = map[string]map[string]any{}
		for _, p := range *projects {
			if id, _ := p["id"].(string); id != "" {
				byID[id] = p
			}
		}
	}
	// Kick foreign chats off --to glass projects (stuck after a chat-named
	// project was reused for workspace retags).
	if n := scrubForeignGlassMembers(plan, mem, byID, headers, projects); n > 0 {
		changed += n
	}
	if changed == 0 {
		return 0, nil
	}
	if err := vscdb.SetItemJSON(db, "glass.localAgentProjectMembership.v1", mem); err != nil {
		return changed, err
	}
	return changed, nil
}

func dedicatedGlassProjectID(plan *Plan) string {
	if plan == nil || plan.TargetWSID == "" {
		return ""
	}
	pid := "rebind-" + plan.TargetWSID
	if len(pid) > 48 {
		pid = pid[:48]
	}
	return pid
}

// scrubForeignGlassMembers moves chats that do not belong to this migrate off
// any glass project retagged onto --to (keeps cursor-rebind chats etc. intact).
func scrubForeignGlassMembers(
	plan *Plan,
	mem map[string]string,
	byID map[string]map[string]any,
	headers map[string]vscdb.ComposerMeta,
	projects *[]map[string]any,
) int {
	if plan == nil || mem == nil {
		return 0
	}
	toClean := filepath.Clean(plan.To)
	changed := 0
	for cid, mid := range mem {
		p := byID[mid]
		if p == nil {
			continue
		}
		ws, _ := p["workspace"].(map[string]any)
		if ws == nil {
			continue
		}
		onTarget := false
		if id, _ := ws["id"].(string); id == plan.TargetWSID {
			onTarget = true
		}
		if uri, _ := ws["uri"].(map[string]any); uri != nil && filepath.Clean(mapFsPath(uri)) == toClean {
			onTarget = true
		}
		if !onTarget {
			continue
		}
		h, ok := headers[cid]
		if ok && composerHeaderBelongsToPlan(h, plan) {
			continue
		}
		home := glassHomeForForeignHeader(h, ok, projects, byID)
		if home == mid {
			continue
		}
		if home == "" {
			delete(mem, cid)
		} else {
			mem[cid] = home
		}
		changed++
	}
	return changed
}

func glassHomeForForeignHeader(
	h vscdb.ComposerMeta,
	ok bool,
	projects *[]map[string]any,
	byID map[string]map[string]any,
) string {
	if !ok || h.WorkspaceIdentifier == nil {
		return ""
	}
	wsID := h.WorkspaceIdentifier.ID
	fp := uriFsPath(h.WorkspaceIdentifier.URI)
	for _, p := range *projects {
		ws, _ := p["workspace"].(map[string]any)
		if ws == nil {
			continue
		}
		if id, _ := ws["id"].(string); id != "" && id == wsID {
			if pid, _ := p["id"].(string); pid != "" {
				return pid
			}
		}
		if uri, _ := ws["uri"].(map[string]any); uri != nil && fp != "" &&
			filepath.Clean(mapFsPath(uri)) == filepath.Clean(fp) {
			if pid, _ := p["id"].(string); pid != "" {
				return pid
			}
		}
	}
	if wsID == "" || wsID == "empty-window" {
		return ""
	}
	pid := "rebind-" + wsID
	if len(pid) > 48 {
		pid = pid[:48]
	}
	if _, exists := byID[pid]; exists {
		return pid
	}
	name := filepath.Base(fp)
	if name == "" || name == "." {
		name = wsID[:8]
	}
	entry := map[string]any{
		"id":   pid,
		"name": name,
		"workspace": map[string]any{
			"id": wsID,
		},
	}
	if fp != "" {
		entry["workspace"].(map[string]any)["uri"] = workspaceURIMap(fp)
	}
	*projects = append(*projects, entry)
	byID[pid] = entry
	return pid
}

func composersNeedingGlassTo(plan *Plan, headers map[string]vscdb.ComposerMeta) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		if h, ok := headers[id]; ok && !composerHeaderBelongsToPlan(h, plan) {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, id := range plan.SourceInventory.AgentComposerIDs {
		add(id)
	}
	for _, id := range plan.SourceInventory.IDEComposerIDs {
		add(id)
	}
	for _, id := range transcriptComposerIDs(plan) {
		add(id)
	}
	for id, h := range headers {
		if composerHeaderBelongsToPlan(h, plan) {
			add(id)
		}
	}
	return out
}

func transferGlassWorkspaceKeys(db *sql.DB, plan *Plan) (int, error) {
	if plan.TargetWSID == "" || plan.Mode != ModeExact {
		return 0, nil
	}
	moved := 0
	for _, sid := range plan.SourceWSIDs {
		if sid == "" || sid == plan.TargetWSID {
			continue
		}
		for _, prefix := range glassTransferPrefixes(sid) {
			keys, err := listItemKeysExactOrLike(db, prefix)
			if err != nil {
				return moved, err
			}
			for _, oldKey := range keys {
				raw, ok, err := vscdb.GetItemRaw(db, oldKey)
				if err != nil || !ok {
					continue
				}
				newKey := strings.Replace(oldKey, sid, plan.TargetWSID, 1)
				if newKey == oldKey {
					continue
				}
				body := strings.ReplaceAll(string(raw), sid, plan.TargetWSID)
				body = strings.ReplaceAll(body, filepath.ToSlash(plan.From), filepath.ToSlash(plan.To))
				if err := vscdb.SetItemRaw(db, newKey, []byte(body)); err != nil {
					return moved, err
				}
				_ = vscdb.DeleteItem(db, oldKey)
				moved++
			}
		}
	}
	return moved, nil
}

func rewriteGlassAdditionalProjects(db *sql.DB, plan *Plan) (int, error) {
	raw, ok, err := vscdb.GetItemRaw(db, "cursor/glass.additionalProjects")
	if err != nil || !ok {
		return 0, err
	}
	var projects []map[string]any
	if json.Unmarshal(raw, &projects) != nil {
		return 0, nil
	}
	sourceSet := toSet(plan.SourceWSIDs)
	changed := 0
	for _, p := range projects {
		wi, _ := p["workspaceIdentifier"].(map[string]any)
		if wi == nil {
			continue
		}
		hit := false
		if id, _ := wi["id"].(string); sourceSet[id] {
			hit = true
		}
		if uri, _ := wi["uri"].(map[string]any); uri != nil {
			if fp := mapFsPath(uri); fp != "" && matches(filepath.Clean(fp), plan.From, plan.Mode) {
				hit = true
			}
		}
		if id, _ := p["id"].(string); strings.HasPrefix(id, "workspace:") {
			for sid := range sourceSet {
				if id == "workspace:"+sid {
					hit = true
				}
			}
		}
		if !hit {
			continue
		}
		wi["id"] = plan.TargetWSID
		wi["uri"] = workspaceURIMap(plan.To)
		p["workspaceIdentifier"] = wi
		p["id"] = "workspace:" + plan.TargetWSID
		p["name"] = filepath.Base(plan.To)
		p["displayPath"] = displayPathFor(plan.To)
		changed++
	}
	if changed == 0 {
		return 0, nil
	}
	if err := vscdb.SetItemJSON(db, "cursor/glass.additionalProjects", projects); err != nil {
		return changed, err
	}
	return changed, nil
}

func glassProjectMatchesFrom(fp, from string, mode Mode) bool {
	if fp == "" {
		return false
	}
	clean := filepath.Clean(fp)
	if matches(clean, from, mode) {
		return true
	}
	if strings.Contains(clean, ".__rebind_orphan_") {
		return false
	}
	base := filepath.Base(from)
	// Basename-only match helps leftover Agents labels ("mover") but is unsafe for
	// short names like "ai" (matches too easily / confuses unrelated folders).
	if !basenameLongEnough(base) {
		return false
	}
	return filepath.Base(clean) == base
}
