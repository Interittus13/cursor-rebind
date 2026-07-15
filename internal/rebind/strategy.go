package rebind

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/Interittus13/cursor-rebind/internal/discover"
	"github.com/Interittus13/cursor-rebind/internal/vscdb"
)

// Strategy labels how source chats relate to the destination. Exact-mode Apply
// always runs the same pipeline; strategy only affects plan copy and which
// contentful chat becomes the primary IDE/Agents tab.
type Strategy string

const (
	// StrategyCreate: no workspaceStorage for --to yet.
	StrategyCreate Strategy = "create"
	// StrategyReplaceEmpty: target exists but only empty stubs.
	StrategyReplaceEmpty Strategy = "replace-empty"
	// StrategyMerge: target already has contentful chats (threads stay separate).
	StrategyMerge Strategy = "merge"
)

// SideInventory summarizes chats for one path / set of workspace ids.
type SideInventory struct {
	WorkspaceIDs      []string `json:"workspaceIds,omitempty"`
	IDEContentful     int      `json:"ideContentful"`
	IDEEmpty          int      `json:"ideEmpty"`
	IDEComposerIDs    []string `json:"ideComposerIds,omitempty"`
	AgentContentful   int      `json:"agentContentful"`
	AgentComposerIDs  []string `json:"agentComposerIds,omitempty"`
	AgentProjectCount int      `json:"agentProjects"`
}

func (s SideInventory) HasContentful() bool {
	return s.IDEContentful > 0 || s.AgentContentful > 0
}

func chooseStrategy(targetWSID string, target SideInventory) Strategy {
	if targetWSID == "" {
		return StrategyCreate
	}
	if target.HasContentful() {
		return StrategyMerge
	}
	return StrategyReplaceEmpty
}

func inventoryPath(
	inv *discover.Inventory,
	gdb *sql.DB,
	folder string,
	wsIDs []string,
) SideInventory {
	folder = filepath.Clean(folder)
	out := SideInventory{WorkspaceIDs: append([]string{}, wsIDs...)}
	seenIDE := map[string]bool{}
	seenAgent := map[string]bool{}

	for _, e := range inv.Headers.Entries {
		if e.ComposerID == "" {
			continue
		}
		hit := false
		if e.WorkspacePath != "" && filepath.Clean(e.WorkspacePath) == folder {
			hit = true
		}
		for _, id := range wsIDs {
			if e.WorkspaceID == id {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		if gdb != nil && composerIsEmpty(gdb, e.ComposerID, e.Name) {
			out.IDEEmpty++
			continue
		}
		if !seenIDE[e.ComposerID] {
			seenIDE[e.ComposerID] = true
			out.IDEContentful++
			out.IDEComposerIDs = append(out.IDEComposerIDs, e.ComposerID)
		}
	}

	// Workspace DB selected / editor composers (may not be in headers yet).
	for _, wid := range wsIDs {
		for _, cid := range readWorkspaceComposerIDs(filepath.Join(inv.Roots.WorkspaceStorage, wid, "state.vscdb")) {
			if cid == "" || seenIDE[cid] {
				continue
			}
			if gdb != nil && composerIsEmpty(gdb, cid, "") {
				out.IDEEmpty++
				continue
			}
			seenIDE[cid] = true
			out.IDEContentful++
			out.IDEComposerIDs = append(out.IDEComposerIDs, cid)
		}
	}

	if gdb == nil {
		return out
	}

	raw, ok, err := vscdb.GetItemRaw(gdb, "glass.localAgentProjects.v1")
	if err != nil || !ok {
		return out
	}
	var projects []map[string]any
	if json.Unmarshal(raw, &projects) != nil {
		return out
	}
	mem := map[string]string{}
	if mraw, mok, _ := vscdb.GetItemRaw(gdb, "glass.localAgentProjectMembership.v1"); mok {
		_ = json.Unmarshal(mraw, &mem)
	}
	// reverse: projectId → composer ids
	byProj := map[string][]string{}
	for cid, pid := range mem {
		byProj[pid] = append(byProj[pid], cid)
	}

	wsSet := toSet(wsIDs)
	for _, p := range projects {
		pid, _ := p["id"].(string)
		ws, _ := p["workspace"].(map[string]any)
		if ws == nil {
			continue
		}
		hit := false
		if id, _ := ws["id"].(string); wsSet[id] {
			hit = true
		}
		uri, _ := ws["uri"].(map[string]any)
		if uri != nil {
			fp := mapFsPath(uri)
			if fp != "" && filepath.Clean(fp) == folder {
				hit = true
			}
			// Basename match for leftover Agents Window labels ("mover").
			if fp != "" && filepath.Base(filepath.Clean(fp)) == filepath.Base(folder) {
				hit = true
			}
		}
		if !hit {
			continue
		}
		out.AgentProjectCount++
		contentfulHere := 0
		for _, cid := range byProj[pid] {
			if composerIsEmpty(gdb, cid, "") {
				continue
			}
			if !seenAgent[cid] {
				seenAgent[cid] = true
				out.AgentComposerIDs = append(out.AgentComposerIDs, cid)
			}
			contentfulHere++
		}
		if contentfulHere > 0 {
			out.AgentContentful += contentfulHere
		}
	}
	return out
}

func openGlobalRO(inv *discover.Inventory) *sql.DB {
	if inv == nil || inv.Roots.GlobalDB == "" {
		return nil
	}
	db, err := vscdb.OpenReadOnly(inv.Roots.GlobalDB)
	if err != nil {
		return nil
	}
	return db
}

func strategyDetail(s Strategy, src, dst SideInventory) string {
	switch s {
	case StrategyCreate:
		return fmt.Sprintf("create workspaceStorage for --to, then move %d IDE + %d Agent chat(s)",
			src.IDEContentful, len(src.AgentComposerIDs))
	case StrategyReplaceEmpty:
		return fmt.Sprintf("target has no contentful chats (empty stubs=%d) — move %d IDE + %d Agent chat(s) onto it",
			dst.IDEEmpty, src.IDEContentful, len(src.AgentComposerIDs))
	case StrategyMerge:
		return fmt.Sprintf("merge source (%d IDE / %d Agent) into target (%d IDE / %d Agent)",
			src.IDEContentful, len(src.AgentComposerIDs), dst.IDEContentful, len(dst.AgentComposerIDs))
	default:
		return string(s)
	}
}

