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

	memberHit := collectGlassMemberHits(db, plan)
	sourceSet := toSet(plan.SourceWSIDs)
	targetURI := workspaceURIMap(plan.To)
	changed := 0
	for _, p := range projects {
		ws, _ := p["workspace"].(map[string]any)
		if ws == nil {
			continue
		}
		hit := false
		if id, _ := p["id"].(string); id != "" && memberHit[id] {
			hit = true
		}
		if id, _ := ws["id"].(string); id != "" && (sourceSet[id] || id == plan.TargetWSID) {
			hit = true
		}
		if uri, _ := ws["uri"].(map[string]any); uri != nil {
			if fp := mapFsPath(uri); fp != "" {
				clean := filepath.Clean(fp)
				if matches(clean, plan.From, plan.Mode) || (plan.Mode == ModeExact && clean == filepath.Clean(plan.To)) {
					hit = true
				}
				if glassProjectMatchesFrom(clean, plan.From, plan.Mode) {
					hit = true
				}
				if strings.Contains(clean, ".__rebind_orphan_") {
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
	if changed == 0 {
		return 0, nil
	}
	if err := vscdb.SetItemJSON(db, "glass.localAgentProjects.v1", projects); err != nil {
		return changed, err
	}
	return changed, nil
}

func collectGlassMemberHits(db *sql.DB, plan *Plan) map[string]bool {
	memberHit := map[string]bool{}
	mraw, mok, _ := vscdb.GetItemRaw(db, "glass.localAgentProjectMembership.v1")
	if !mok {
		return memberHit
	}
	var mem map[string]string
	if json.Unmarshal(mraw, &mem) != nil {
		return memberHit
	}
	mark := func(cid string) {
		if pid := mem[cid]; pid != "" {
			memberHit[pid] = true
		}
	}
	for _, cid := range transcriptComposerIDs(plan) {
		mark(cid)
	}
	for _, cid := range plan.SourceInventory.AgentComposerIDs {
		mark(cid)
	}
	for _, cid := range plan.SourceInventory.IDEComposerIDs {
		mark(cid)
	}
	var headers vscdb.ComposerHeaders
	if ok, _ := vscdb.GetItemJSON(db, "composer.composerHeaders", &headers); ok {
		sourceSet := toSet(plan.SourceWSIDs)
		for _, c := range headers.AllComposers {
			if !headerHitsPlan(c, plan, sourceSet) {
				continue
			}
			mark(c.ComposerID)
		}
	}
	if sel, ok, _ := vscdb.GetItemRaw(db, "cursor/glass.selectedAgent"); ok {
		cid := strings.Trim(strings.TrimSpace(string(sel)), `"`)
		mark(cid)
	}
	return memberHit
}

func headerHitsPlan(c vscdb.ComposerMeta, plan *Plan, sourceSet map[string]bool) bool {
	if c.WorkspaceIdentifier == nil {
		return false
	}
	if sourceSet[c.WorkspaceIdentifier.ID] || c.WorkspaceIdentifier.ID == plan.TargetWSID {
		return true
	}
	fp := uriFsPath(c.WorkspaceIdentifier.URI)
	if fp == "" {
		return false
	}
	return matches(fp, plan.From, plan.Mode) ||
		filepath.Clean(fp) == filepath.Clean(plan.To) ||
		glassProjectMatchesFrom(fp, plan.From, plan.Mode)
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
	// Agents Window often keeps the old folder basename even after partial migrates.
	return filepath.Base(clean) == filepath.Base(from) &&
		!strings.Contains(clean, ".__rebind_orphan_")
}
