package rebind

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Interittus13/cursor-rebind/internal/backup"
	"github.com/Interittus13/cursor-rebind/internal/discover"
	"github.com/Interittus13/cursor-rebind/internal/guard"
	"github.com/Interittus13/cursor-rebind/internal/paths"
	"github.com/Interittus13/cursor-rebind/internal/vscdb"
)

// Mode selects how paths are matched.
type Mode string

const (
	ModeExact  Mode = "exact"
	ModePrefix Mode = "prefix"
)

// Plan describes a rebind operation.
type Plan struct {
	Mode               Mode          `json:"mode"`
	From               string        `json:"from"`
	To                 string        `json:"to"`
	Strategy           Strategy      `json:"strategy,omitempty"`
	TargetWSID         string        `json:"targetWorkspaceId,omitempty"`
	SourceWSIDs        []string      `json:"sourceWorkspaceIds,omitempty"`
	SourceInventory    SideInventory `json:"sourceInventory"`
	TargetInventory    SideInventory `json:"targetInventory"`
	HeadersByPath      int           `json:"headersByPath"`
	HeadersByWorkspace int           `json:"headersByWorkspaceId"`
	HeadersMatched     int           `json:"headersMatched"`
	ComposersFromWS    int           `json:"composersFromWorkspaceDb"`
	ProjectFrom        string        `json:"projectFrom,omitempty"`
	ProjectTo          string        `json:"projectTo,omitempty"`
	ProjectExists      bool          `json:"projectFromExists"`
	TargetExists       bool          `json:"targetPathExists"`
	Warnings           []string      `json:"warnings,omitempty"`
	Ops                []Op          `json:"ops"`
}

// Op is one planned mutation.
type Op struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Count  int    `json:"count,omitempty"`
}

// Result is the outcome of Apply.
type Result struct {
	BackupID             string   `json:"backupId"`
	HeadersUpdated       int      `json:"headersUpdated"`
	HeadersAdded         int      `json:"headersAdded"`
	ComposersRewritten   int      `json:"composersRewritten"`
	GlassProjectsUpdated int      `json:"glassProjectsUpdated"`
	GlassKeysMoved       int      `json:"glassKeysMoved"`
	ProjectMoved         bool     `json:"projectMoved"`
	TranscriptsWritten   int      `json:"transcriptsWritten,omitempty"`
	SourceStoragePurged  int      `json:"sourceStoragePurged,omitempty"`
	TargetWSID           string   `json:"targetWorkspaceId"`
	HealthOK             bool     `json:"healthOk"`
	HealthIssues         []string `json:"healthIssues,omitempty"`
}

// BuildPlan constructs a rebind plan from inventory + from/to.
func BuildPlan(inv *discover.Inventory, from, to string, mode Mode) (*Plan, error) {
	return BuildPlanWithTarget(inv, from, to, mode, "")
}

// BuildPlanWithTarget is BuildPlan with an optional forced target workspace id.
func BuildPlanWithTarget(inv *discover.Inventory, from, to string, mode Mode, targetID string) (*Plan, error) {
	from = filepath.Clean(from)
	to = filepath.Clean(to)
	if from == "" || to == "" {
		return nil, fmt.Errorf("from and to paths are required")
	}
	if from == to {
		return nil, fmt.Errorf("from and to are the same path")
	}

	p := &Plan{Mode: mode, From: from, To: to}
	p.ProjectFrom = paths.SanitizeProjectPath(from)
	p.ProjectTo = paths.SanitizeProjectPath(to)
	if st, err := os.Stat(to); err == nil && st.IsDir() {
		p.TargetExists = true
	} else {
		p.Warnings = append(p.Warnings, "target path does not exist on disk — open it in Cursor after migrate")
	}

	if targetID != "" {
		p.TargetWSID = targetID
	} else {
		// Prefer the empty shell Cursor minted for the target path (not the data-holding leftover).
		p.TargetWSID = pickTargetWorkspaceID(inv, to)
	}
	if p.TargetWSID == "" && mode == ModeExact {
		p.Warnings = append(p.Warnings, "no workspaceStorage entry for target yet — a new id will be created on apply if needed")
	}
	if mode == ModeExact {
		if live := liveWorkspaceIDs(inv, to); len(live) > 1 {
			p.Warnings = append(p.Warnings, fmt.Sprintf(
				"SPLIT-BRAIN risk: %d live workspace ids for --to (%s); migrate attaches chats to %s and orphans the rest",
				len(live), strings.Join(live, ", "), p.TargetWSID,
			))
		}
	}

	p.SourceWSIDs = findSourceWorkspaceIDs(inv, from, to, p.TargetWSID, mode)
	if len(p.SourceWSIDs) == 0 && mode == ModeExact {
		p.Warnings = append(p.Warnings, "no source workspaceStorage id found for --from (path may already have been rewritten)")
	}

	gdb := openGlobalRO(inv)
	if gdb != nil {
		defer gdb.Close()
	}
	p.SourceInventory = inventoryPath(inv, gdb, from, p.SourceWSIDs)
	targetIDs := []string{}
	if p.TargetWSID != "" {
		targetIDs = []string{p.TargetWSID}
	}
	p.TargetInventory = inventoryPath(inv, gdb, to, targetIDs)
	p.Strategy = chooseStrategy(p.TargetWSID, p.TargetInventory)
	p.Warnings = append(p.Warnings, "strategy: "+strategyDetail(p.Strategy, p.SourceInventory, p.TargetInventory))

	sourceSet := toSet(p.SourceWSIDs)
	byPath, byWS := 0, 0
	seen := map[string]bool{}
	for _, e := range inv.Headers.Entries {
		hit := false
		if e.WorkspacePath != "" {
			fp := filepath.Clean(e.WorkspacePath)
			if matches(fp, from, mode) {
				byPath++
				hit = true
			}
			// Already on new path but wrong workspace id (botched earlier migrate).
			if mode == ModeExact && fp == to && e.WorkspaceID != "" && e.WorkspaceID != p.TargetWSID {
				byWS++
				hit = true
				if e.WorkspaceID != "" {
					// Ensure we treat that id as a source for rewrite.
					found := false
					for _, id := range p.SourceWSIDs {
						if id == e.WorkspaceID {
							found = true
							break
						}
					}
					if !found {
						p.SourceWSIDs = append(p.SourceWSIDs, e.WorkspaceID)
						sourceSet[e.WorkspaceID] = true
					}
				}
			}
		}
		if e.WorkspaceID != "" && sourceSet[e.WorkspaceID] {
			byWS++
			hit = true
		}
		if hit && e.ComposerID != "" {
			seen[e.ComposerID] = true
		}
	}
	p.HeadersByPath = byPath
	p.HeadersByWorkspace = byWS
	if len(seen) > 0 {
		p.HeadersMatched = len(seen)
	} else {
		// Path/id hits without composer ids still count for planning.
		p.HeadersMatched = byPath
		if byWS > p.HeadersMatched {
			p.HeadersMatched = byWS
		}
	}
	if byPath == 0 && byWS == 0 {
		// Headers may already sit on the target identity from a partial earlier migrate.
		already := 0
		for _, e := range inv.Headers.Entries {
			if e.WorkspacePath != "" && filepath.Clean(e.WorkspacePath) == to {
				already++
			}
		}
		if already > 0 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%d header(s) already point at --to — will refresh tabs/editor/glass even if path retag is a no-op", already))
			if p.HeadersMatched == 0 {
				p.HeadersMatched = already
			}
		} else {
			p.Warnings = append(p.Warnings, "no global chat headers match --from by path or workspace id")
		}
	} else if byPath == 0 && byWS > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("path match is 0, but %d header(s) match source workspace id(s) — will retag by id", byWS))
	}

	// Composers living only in workspace DBs (selected tabs / panes) + agent transcripts.
	composerIDs := collectWorkspaceComposerIDs(inv.Roots.WorkspaceStorage, p.SourceWSIDs)
	for _, cid := range transcriptComposerIDs(p) {
		composerIDs[cid] = true
	}
	missing := 0
	for id := range composerIDs {
		if !seen[id] {
			missing++
		}
	}
	p.ComposersFromWS = len(composerIDs)
	if missing > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d composer(s) found in source workspace DB/transcripts but missing from global headers — will register them", missing))
	}

	totalTouch := p.HeadersMatched + missing
	p.Ops = append(p.Ops, Op{
		Kind:   "strategy",
		Detail: strategyDetail(p.Strategy, p.SourceInventory, p.TargetInventory),
	})
	if p.Strategy == StrategyCreate {
		p.Ops = append(p.Ops, Op{
			Kind:   "create-workspace",
			Detail: "create workspaceStorage entry for --to before moving chats",
		})
	}
	p.Ops = append(p.Ops, Op{
		Kind:   "rewrite-headers",
		Detail: fmt.Sprintf("IDE: retag/register composer.composerHeaders for %s → %s", from, to),
		Count:  totalTouch,
	})

	if mode == ModeExact {
		fromDir := filepath.Join(inv.Roots.ProjectsDir, p.ProjectFrom)
		if st, err := os.Stat(fromDir); err == nil && st.IsDir() {
			p.ProjectExists = true
			toDir := filepath.Join(inv.Roots.ProjectsDir, p.ProjectTo)
			if _, err := os.Stat(toDir); err == nil {
				p.Ops = append(p.Ops, Op{
					Kind:   "merge-projects",
					Detail: fmt.Sprintf("merge %s into existing %s", p.ProjectFrom, p.ProjectTo),
				})
			} else {
				p.Ops = append(p.Ops, Op{
					Kind:   "rename-project",
					Detail: fmt.Sprintf("rename ~/.cursor/projects/%s → %s", p.ProjectFrom, p.ProjectTo),
				})
			}
		}
		p.Ops = append(p.Ops, Op{
			Kind:   "rewrite-composer-data",
			Detail: "Agents: set composerData.workspaceIdentifier + trackedGitRepos paths/repoUrl on --to",
		})
		switch p.Strategy {
		case StrategyMerge:
			p.Ops = append(p.Ops, Op{
				Kind:   "transfer-workspace-tabs",
				Detail: "IDE: merge — keep target contentful chats, attach source chats, bind richest primary",
			})
		case StrategyReplaceEmpty:
			p.Ops = append(p.Ops, Op{
				Kind:   "transfer-workspace-tabs",
				Detail: "IDE: replace empty target stubs with source contentful primary chat",
			})
		default:
			p.Ops = append(p.Ops, Op{
				Kind:   "transfer-workspace-tabs",
				Detail: "IDE: bind primary contentful composer + editor restore state onto the new workspace",
			})
		}
		p.Ops = append(p.Ops, Op{
			Kind:   "rewrite-glass-projects",
			Detail: "Agents Window: retag glass projects + move glass.tabs / cache keys (same pass as IDE)",
		})
		p.Ops = append(p.Ops, Op{
			Kind:   "detach-orphan-workspaces",
			Detail: "point leftover workspaceStorage folders away from the target path so Cursor opens one identity",
		})
		p.Ops = append(p.Ops, Op{
			Kind:   "retire-source-identity",
			Detail: "retire leftover --from workspaces, workspaceMetadata, recently-opened, and stub agent project dirs",
		})
		// Do NOT rewrite source workspace.json to the target path (creates duplicate identities).
	} else {
		p.Ops = append(p.Ops, Op{
			Kind:   "rewrite-project-dirs",
			Detail: "rename ~/.cursor/projects dirs whose names map under the path prefix",
		})
		p.Ops = append(p.Ops, Op{
			Kind:   "update-workspace-json",
			Detail: "rewrite workspace.json folder URIs under the path prefix",
		})
	}

	p.Ops = append(p.Ops, Op{
		Kind:   "backup",
		Detail: "timestamped backup under ~/.cursor-rebind/backups before writes",
	})

	return p, nil
}

func pickTargetWorkspaceID(inv *discover.Inventory, to string) string {
	to = filepath.Clean(to)
	var candidates []discover.Workspace
	for _, w := range inv.Workspaces {
		if w.FolderPath != "" && filepath.Clean(w.FolderPath) == to {
			candidates = append(candidates, w)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	if len(candidates) == 1 {
		return candidates[0].ID
	}

	// Multiple workspaceStorage entries for the same folder (common after rename).
	// Attach chats onto the shell Cursor opens for the new path: fewest *named*
	// global headers (data leftover vs empty reminted shell), then fewest local
	// contentful tabs, then newest mtime. Never prefer the data-holding leftover —
	// deleting the empty shell makes Cursor remint it and leaves IDE/Agents blank.
	type scored struct {
		id         string
		named      int
		contentful int
		modTime    time.Time
	}
	var gdb *sql.DB
	if inv.Roots.GlobalDB != "" {
		if db, err := vscdb.OpenReadOnly(inv.Roots.GlobalDB); err == nil {
			gdb = db
			defer db.Close()
		}
	}
	list := make([]scored, 0, len(candidates))
	for _, w := range candidates {
		named := namedHeaderCount(inv, w.ID)
		ids := readWorkspaceComposerIDs(filepath.Join(inv.Roots.WorkspaceStorage, w.ID, "state.vscdb"))
		contentful := 0
		if gdb != nil {
			for _, cid := range ids {
				if !composerIsEmpty(gdb, cid, "") {
					contentful++
				}
			}
		}
		list = append(list, scored{id: w.ID, named: named, contentful: contentful, modTime: w.ModTime})
	}

	best := list[0]
	for _, s := range list[1:] {
		switch {
		case s.named < best.named:
			best = s
		case s.named > best.named:
			continue
		case s.contentful < best.contentful:
			best = s
		case s.contentful > best.contentful:
			continue
		case s.modTime.After(best.modTime):
			best = s
		}
	}
	return best.id
}

func findSourceWorkspaceIDs(inv *discover.Inventory, from, to, targetID string, mode Mode) []string {
	from = filepath.Clean(from)
	to = filepath.Clean(to)
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id == "" || id == targetID || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}

	for _, w := range inv.Workspaces {
		if w.FolderPath != "" && matches(filepath.Clean(w.FolderPath), from, mode) {
			add(w.ID)
		}
	}

	if mode == ModeExact {
		// Any other workspaceStorage row already pointing at `to` is a rename leftover.
		for _, w := range inv.Workspaces {
			if w.FolderPath != "" && filepath.Clean(w.FolderPath) == to {
				add(w.ID)
			}
		}
		// Orphans from a previous cursor-rebind run still hold editor state.
		orphanMarker := ".__rebind_orphan_"
		toBase := filepath.Base(to)
		fromBase := filepath.Base(from)
		for _, w := range inv.Workspaces {
			fp := filepath.Clean(w.FolderPath)
			if fp == "" || !strings.Contains(fp, orphanMarker) {
				continue
			}
			// file:///.../cursor-rebind.__rebind_orphan_<id>
			stem := fp
			if i := strings.Index(stem, orphanMarker); i >= 0 {
				stem = stem[:i]
			}
			if filepath.Base(stem) == toBase || filepath.Base(stem) == fromBase ||
				stem == to || stem == from {
				add(w.ID)
			}
		}
		// Basename match for odd leftovers (skip short names like "ai").
		base := filepath.Base(from)
		if basenameLongEnough(base) {
			for _, w := range inv.Workspaces {
				if w.FolderPath != "" && filepath.Base(filepath.Clean(w.FolderPath)) == base {
					add(w.ID)
				}
			}
		}
		for _, e := range inv.Headers.Entries {
			if e.WorkspacePath != "" && filepath.Clean(e.WorkspacePath) == from && e.WorkspaceID != "" {
				add(e.WorkspaceID)
			}
			if e.WorkspacePath != "" && filepath.Clean(e.WorkspacePath) == to && e.WorkspaceID != "" && e.WorkspaceID != targetID {
				add(e.WorkspaceID)
			}
		}
		// Prefer ordering sources that actually hold contentful composers first.
		var gdb *sql.DB
		if inv.Roots.GlobalDB != "" {
			if db, err := vscdb.OpenReadOnly(inv.Roots.GlobalDB); err == nil {
				gdb = db
				defer db.Close()
			}
		}
		sort.SliceStable(out, func(i, j int) bool {
			idsI := readWorkspaceComposerIDs(filepath.Join(inv.Roots.WorkspaceStorage, out[i], "state.vscdb"))
			idsJ := readWorkspaceComposerIDs(filepath.Join(inv.Roots.WorkspaceStorage, out[j], "state.vscdb"))
			if gdb == nil {
				return len(idsI) > len(idsJ)
			}
			ci, cj := 0, 0
			for _, id := range idsI {
				if !composerIsEmpty(gdb, id, "") {
					ci++
				}
			}
			for _, id := range idsJ {
				if !composerIsEmpty(gdb, id, "") {
					cj++
				}
			}
			return ci > cj
		})
	}
	return out
}

func collectWorkspaceComposerIDs(wsRoot string, sourceIDs []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range sourceIDs {
		dbPath := filepath.Join(wsRoot, id, "state.vscdb")
		ids := readWorkspaceComposerIDs(dbPath)
		for _, c := range ids {
			out[c] = true
		}
	}
	return out
}

func readWorkspaceComposerIDs(dbPath string) []string {
	db, err := vscdb.OpenReadOnly(dbPath)
	if err != nil {
		return nil
	}
	defer db.Close()

	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}

	var data vscdb.ComposerData
	if ok, err := vscdb.GetItemJSON(db, "composer.composerData", &data); err == nil && ok {
		for _, c := range data.AllComposers {
			add(c.ComposerID)
		}
		for _, id := range data.SelectedComposerIDs {
			add(id)
		}
		for _, id := range data.LastFocusedComposerIDs {
			add(id)
		}
	}

	// Editor restore often still references the real chat after selectedComposerIds
	// has been overwritten with an empty stub (classic Ctrl+Alt+J failure mode).
	if raw, ok, _ := vscdb.GetItemRaw(db, "workbench.parts.embeddedAuxBarEditor.state"); ok {
		extractComposerIDsFromJSON(raw, add)
		extractComposerIDsFromEditorValue(raw, add)
	}

	// Pane / view state often retains composer ids not yet in global headers.
	rows, err := db.Query(`SELECT key, value FROM ItemTable WHERE key LIKE 'workbench.panel.composerChatViewPane%'`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var key string
			var raw []byte
			if rows.Scan(&key, &raw) != nil {
				continue
			}
			extractComposerIDsFromJSON(raw, add)
		}
	}
	return out
}

// extractComposerIDsFromEditorValue digs into nested JSON-string editor payloads.
func extractComposerIDsFromEditorValue(raw []byte, add func(string)) {
	var state map[string]any
	if json.Unmarshal(raw, &state) != nil {
		return
	}
	grid, _ := state["serializedGrid"].(map[string]any)
	if grid == nil {
		return
	}
	root, _ := grid["root"].(map[string]any)
	if root == nil {
		return
	}
	var walkLeaves func(any)
	walkLeaves = func(n any) {
		m, ok := n.(map[string]any)
		if !ok {
			return
		}
		if typ, _ := m["type"].(string); typ == "leaf" {
			inner, _ := m["data"].(map[string]any)
			editors, _ := inner["editors"].([]any)
			for _, ed := range editors {
				em, _ := ed.(map[string]any)
				val, _ := em["value"].(string)
				if val == "" {
					continue
				}
				var payload map[string]any
				if json.Unmarshal([]byte(val), &payload) != nil {
					continue
				}
				if id, _ := payload["composerId"].(string); looksLikeUUID(id) {
					add(id)
				}
			}
			return
		}
		if data, ok := m["data"].([]any); ok {
			for _, child := range data {
				walkLeaves(child)
			}
		}
	}
	walkLeaves(root)
}

func extractComposerIDsFromJSON(raw []byte, add func(string)) {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return
	}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			for k, val := range t {
				lk := strings.ToLower(k)
				if strings.Contains(lk, "composer") {
					if s, ok := val.(string); ok && looksLikeUUID(s) {
						add(s)
					}
				}
				walk(val)
			}
		case []any:
			for _, item := range t {
				if s, ok := item.(string); ok && looksLikeUUID(s) {
					add(s)
				}
				walk(item)
			}
		}
	}
	walk(v)
}

func looksLikeUUID(s string) bool {
	if len(s) < 32 || len(s) > 40 {
		return false
	}
	hyphen := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' {
			hyphen++
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return hyphen >= 4
}

func toSet(ids []string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func matches(path, from string, mode Mode) bool {
	path = filepath.Clean(path)
	from = filepath.Clean(from)
	switch mode {
	case ModePrefix:
		return path == from || strings.HasPrefix(path, from+string(filepath.Separator))
	default:
		return path == from
	}
}

func rewritePath(path, from, to string, mode Mode) string {
	path = filepath.Clean(path)
	from = filepath.Clean(from)
	to = filepath.Clean(to)
	switch mode {
	case ModePrefix:
		if path == from {
			return to
		}
		if strings.HasPrefix(path, from+string(filepath.Separator)) {
			return to + path[len(from):]
		}
		return path
	default:
		if path == from {
			return to
		}
		return path
	}
}

// FormatPlan renders a human-readable plan.
func FormatPlan(p *Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cursor-rebind plan\n")
	fmt.Fprintf(&b, "==================\n")
	fmt.Fprintf(&b, "Mode:     %s\n", p.Mode)
	fmt.Fprintf(&b, "From:     %s\n", p.From)
	fmt.Fprintf(&b, "To:       %s\n", p.To)
	if p.Strategy != "" {
		fmt.Fprintf(&b, "Strategy: %s\n", p.Strategy)
	}
	if p.TargetWSID != "" {
		fmt.Fprintf(&b, "Target ID:%s\n", p.TargetWSID)
	}
	if len(p.SourceWSIDs) > 0 {
		fmt.Fprintf(&b, "Source IDs:%s\n", strings.Join(p.SourceWSIDs, ", "))
	}
	fmt.Fprintf(&b, "Source chats: IDE=%d contentful (%d empty), Agent=%d\n",
		p.SourceInventory.IDEContentful, p.SourceInventory.IDEEmpty, len(p.SourceInventory.AgentComposerIDs))
	fmt.Fprintf(&b, "Target chats: IDE=%d contentful (%d empty), Agent=%d\n",
		p.TargetInventory.IDEContentful, p.TargetInventory.IDEEmpty, len(p.TargetInventory.AgentComposerIDs))
	fmt.Fprintf(&b, "Headers:  %d matched (path=%d, workspaceId=%d)\n", p.HeadersMatched, p.HeadersByPath, p.HeadersByWorkspace)
	fmt.Fprintf(&b, "WS composers: %d\n", p.ComposersFromWS)
	fmt.Fprintf(&b, "Target exists on disk: %v\n\n", p.TargetExists)

	fmt.Fprintf(&b, "Operations:\n")
	for _, op := range p.Ops {
		if op.Count > 0 {
			fmt.Fprintf(&b, "  • [%s] %s (%d)\n", op.Kind, op.Detail, op.Count)
		} else {
			fmt.Fprintf(&b, "  • [%s] %s\n", op.Kind, op.Detail)
		}
	}
	if len(p.Warnings) > 0 {
		fmt.Fprintf(&b, "\nWarnings:\n")
		for _, w := range p.Warnings {
			fmt.Fprintf(&b, "  ! %s\n", w)
		}
	}
	return b.String()
}

// Apply executes the plan. dryRun skips mutations.
// cleanup, when true, removes orphaned source workspaceStorage dirs after a
// successful exact-mode identity pass (refused with --prefix).
func Apply(inv *discover.Inventory, plan *Plan, yes, dryRun, cleanup bool) (*Result, error) {
	touchable := plan.HeadersMatched > 0 || plan.ComposersFromWS > 0 || plan.ProjectExists ||
		len(plan.SourceWSIDs) > 0 || plan.SourceInventory.HasContentful() ||
		plan.Strategy == StrategyCreate
	if !touchable {
		return nil, fmt.Errorf("nothing to rebind — no matching headers, workspace ids, or agent project dirs")
	}
	if !yes && !dryRun {
		return nil, fmt.Errorf("refusing to write without --yes (use --dry-run to preview)")
	}
	if cleanup && plan.Mode == ModePrefix {
		return nil, fmt.Errorf("--cleanup requires exact mode (omit --prefix)")
	}
	if !dryRun {
		if err := guard.EnsureCursorClosed(); err != nil {
			return nil, err
		}
	}

	res := &Result{TargetWSID: plan.TargetWSID}
	if dryRun {
		return res, nil
	}

	if plan.TargetWSID == "" && plan.Mode == ModeExact {
		id, err := ensureWorkspace(inv.Roots, plan.To)
		if err != nil {
			return nil, err
		}
		plan.TargetWSID = id
		res.TargetWSID = id
		if plan.Strategy == "" {
			plan.Strategy = StrategyCreate
		}
	}

	id, bdir, man, err := backup.Create(fmt.Sprintf("rebind %s → %s (%s)", plan.From, plan.To, plan.Mode))
	if err != nil {
		return nil, err
	}
	res.BackupID = id

	global := inv.Roots.GlobalDB
	for _, side := range []struct {
		logical, src string
	}{
		{"global/state.vscdb", global},
		{"global/state.vscdb-wal", global + "-wal"},
		{"global/state.vscdb-shm", global + "-shm"},
	} {
		_ = backup.CopyFile(bdir, man, side.logical, side.src)
	}
	for _, sid := range plan.SourceWSIDs {
		_ = backup.CopyFile(bdir, man, "ws/"+sid+"/state.vscdb", filepath.Join(inv.Roots.WorkspaceStorage, sid, "state.vscdb"))
	}
	if plan.TargetWSID != "" {
		_ = backup.CopyFile(bdir, man, "ws/"+plan.TargetWSID+"/state.vscdb", filepath.Join(inv.Roots.WorkspaceStorage, plan.TargetWSID, "state.vscdb"))
	}
	if plan.Mode == ModeExact && plan.ProjectExists {
		src := filepath.Join(inv.Roots.ProjectsDir, plan.ProjectFrom)
		_ = backup.CopyTree(bdir, man, "projects/"+plan.ProjectFrom, src)
	}
	if err := backup.WriteManifest(bdir, man); err != nil {
		return nil, err
	}

	updated, added, err := rewriteHeaders(inv.Roots.GlobalDB, inv.Roots.WorkspaceStorage, plan)
	if err != nil {
		return nil, fmt.Errorf("rewrite headers: %w", err)
	}
	res.HeadersUpdated = updated
	res.HeadersAdded = added

	if plan.Mode == ModePrefix {
		if err := rewriteWorkspaceJSON(inv.Roots.WorkspaceStorage, plan); err != nil {
			return nil, fmt.Errorf("workspace.json: %w", err)
		}
		moved, err := rewriteProjects(inv.Roots.ProjectsDir, plan)
		if err != nil {
			return nil, fmt.Errorf("projects: %w", err)
		}
		res.ProjectMoved = moved
		if err := normalizeStorageText(inv.Roots.GlobalDB, inv.Roots.WorkspaceStorage, plan); err != nil {
			return nil, fmt.Errorf("normalize storage text: %w", err)
		}
		res.HealthOK = true
	} else {
		idRes, err := applyExactIdentity(inv, plan, exactIdentityOpts{
			rewriteProjects:      true,
			cleanupSourceStorage: cleanup,
		})
		if err != nil {
			return nil, err
		}
		res.ComposersRewritten = idRes.ComposersRewritten
		res.GlassProjectsUpdated = idRes.GlassProjectsUpdated
		res.GlassKeysMoved = idRes.GlassKeysMoved
		res.ProjectMoved = idRes.ProjectMoved
		res.TranscriptsWritten = idRes.TranscriptsWritten
		res.SourceStoragePurged = idRes.SourceStoragePurged

		if err := enforceExactHealth(inv.Roots, plan.To, plan.TargetWSID, res.BackupID); err != nil {
			if ue, ok := err.(*UnhealthyError); ok && ue.Report != nil {
				res.HealthOK = false
				res.HealthIssues = append([]string{}, ue.Report.Issues...)
			}
			return res, err
		}
		res.HealthOK = true
	}

	return res, nil
}

type exactIdentityOpts struct {
	// rewriteProjects moves/merges ~/.cursor/projects (migrate only; repair skips).
	rewriteProjects bool
	// cleanupSourceStorage removes orphaned source workspaceStorage after success.
	cleanupSourceStorage bool
}

type exactIdentityResult struct {
	PrimaryComposerID    string
	ComposersRewritten   int
	GlassProjectsUpdated int
	GlassKeysMoved       int
	ProjectMoved         bool
	TranscriptsWritten   int
	SourceStoragePurged  int
}

// applyExactIdentity runs the exact-mode identity pass shared by Apply and RepairTabs.
func applyExactIdentity(inv *discover.Inventory, plan *Plan, opts exactIdentityOpts) (*exactIdentityResult, error) {
	out := &exactIdentityResult{}
	n, err := rewriteComposerDiskPaths(inv.Roots.GlobalDB, plan)
	if err != nil {
		return nil, fmt.Errorf("composer disk paths: %w", err)
	}
	out.ComposersRewritten = n
	primary, err := transferWorkspaceTabsPrimary(inv.Roots.WorkspaceStorage, inv.Roots.GlobalDB, plan)
	if err != nil {
		return nil, fmt.Errorf("transfer tabs: %w", err)
	}
	out.PrimaryComposerID = primary
	gp, gk, err := rewriteGlassAgentIdentity(inv.Roots.GlobalDB, plan, primary)
	if err != nil {
		return nil, fmt.Errorf("glass agent identity: %w", err)
	}
	out.GlassProjectsUpdated = gp
	out.GlassKeysMoved = gk
	if err := detachOrphanWorkspaces(inv.Roots.WorkspaceStorage, plan); err != nil {
		return nil, fmt.Errorf("detach orphans: %w", err)
	}
	if opts.rewriteProjects {
		// Move/merge ~/.cursor/projects before retireSourceIdentity — retirement
		// may delete the --from project slug as a stub and would break rewriteProjects.
		moved, err := rewriteProjects(inv.Roots.ProjectsDir, plan)
		if err != nil {
			return nil, fmt.Errorf("projects: %w", err)
		}
		out.ProjectMoved = moved
	}
	if err := retireSourceIdentity(inv.Roots.GlobalDB, inv.Roots.WorkspaceStorage, inv.Roots.ProjectsDir, plan); err != nil {
		return nil, fmt.Errorf("retire source identity: %w", err)
	}
	tn, err := ensureAgentTranscripts(inv.Roots.GlobalDB, plan)
	if err != nil {
		return nil, fmt.Errorf("agent transcripts: %w", err)
	}
	out.TranscriptsWritten = tn
	if err := normalizeStorageText(inv.Roots.GlobalDB, inv.Roots.WorkspaceStorage, plan); err != nil {
		return nil, fmt.Errorf("normalize storage text: %w", err)
	}
	if opts.cleanupSourceStorage {
		n, _, err := purgeSourceStorage(inv.Roots.WorkspaceStorage, inv.Roots.ProjectsDir, plan)
		if err != nil {
			return nil, fmt.Errorf("purge source storage: %w", err)
		}
		out.SourceStoragePurged = n
	}
	return out, nil
}

func rewriteHeaders(globalDB, wsRoot string, plan *Plan) (updated, added int, err error) {
	db, err := vscdb.OpenReadWrite(globalDB)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		_ = vscdb.CheckpointWAL(db)
		_ = db.Close()
	}()

	var headers vscdb.ComposerHeaders
	ok, err := vscdb.GetItemJSON(db, "composer.composerHeaders", &headers)
	if err != nil {
		return 0, 0, err
	}
	if !ok {
		return 0, 0, fmt.Errorf("composer.composerHeaders not found (is this Cursor 3.0+?)")
	}

	sourceSet := toSet(plan.SourceWSIDs)
	targetURI := buildURI(plan.To)
	byID := map[string]int{}
	for i := range headers.AllComposers {
		byID[headers.AllComposers[i].ComposerID] = i
	}

	for i := range headers.AllComposers {
		c := &headers.AllComposers[i]
		hit := false
		if c.WorkspaceIdentifier != nil {
			if c.WorkspaceIdentifier.ID != "" && sourceSet[c.WorkspaceIdentifier.ID] {
				hit = true
			}
			if c.WorkspaceIdentifier.URI != nil {
				fp := uriFsPath(c.WorkspaceIdentifier.URI)
				if fp != "" && matches(fp, plan.From, plan.Mode) {
					hit = true
				}
				if plan.Mode == ModeExact && fp != "" && filepath.Clean(fp) == filepath.Clean(plan.To) &&
					c.WorkspaceIdentifier.ID != plan.TargetWSID {
					hit = true
				}
			}
		}
		if !hit {
			continue
		}
		// Do not promote empty "New Agent" stubs onto the target identity.
		// Leave them for the pass below — dropping source-side stubs clears the
		// leftover --from path from Agents Window / sidebar.
		if composerIsEmpty(db, c.ComposerID, c.Name) {
			continue
		}
		if c.WorkspaceIdentifier == nil {
			c.WorkspaceIdentifier = &vscdb.WorkspaceIdentifier{}
		}
		if plan.Mode == ModeExact {
			c.WorkspaceIdentifier.ID = plan.TargetWSID
			c.WorkspaceIdentifier.URI = targetURI
			// Agents Window hydrates history for agent-mode chats only.
			if c.UnifiedMode == "" || c.UnifiedMode == "chat" {
				c.UnifiedMode = "agent"
			}
		} else {
			fp := uriFsPath(c.WorkspaceIdentifier.URI)
			newPath := rewritePath(fp, plan.From, plan.To, plan.Mode)
			if newPath == "" {
				newPath = plan.To
			}
			c.WorkspaceIdentifier.URI = buildURI(newPath)
		}
		updated++
	}

	// Register real composers from source workspace DBs + agent transcripts.
	need := collectWorkspaceComposerIDs(wsRoot, plan.SourceWSIDs)
	for _, cid := range transcriptComposerIDs(plan) {
		need[cid] = true
	}
	for composerID := range need {
		if composerIsEmpty(db, composerID, "") {
			continue
		}
		if _, exists := byID[composerID]; exists {
			if plan.Mode == ModeExact {
				idx := byID[composerID]
				c := &headers.AllComposers[idx]
				if c.WorkspaceIdentifier == nil {
					c.WorkspaceIdentifier = &vscdb.WorkspaceIdentifier{}
				}
				if c.WorkspaceIdentifier.ID != plan.TargetWSID {
					c.WorkspaceIdentifier.ID = plan.TargetWSID
					c.WorkspaceIdentifier.URI = targetURI
					if c.UnifiedMode == "" || c.UnifiedMode == "chat" {
						c.UnifiedMode = "agent"
					}
					updated++
				} else if c.WorkspaceIdentifier.URI == nil ||
					filepath.Clean(uriFsPath(c.WorkspaceIdentifier.URI)) != filepath.Clean(plan.To) {
					c.WorkspaceIdentifier.URI = targetURI
					if c.UnifiedMode == "" || c.UnifiedMode == "chat" {
						c.UnifiedMode = "agent"
					}
					updated++
				} else if c.UnifiedMode == "" || c.UnifiedMode == "chat" {
					c.UnifiedMode = "agent"
					updated++
				}
			}
			continue
		}
		meta := loadComposerMeta(db, composerID)
		meta.ComposerID = composerID
		if meta.Type == "" {
			meta.Type = "head"
		}
		meta.WorkspaceIdentifier = &vscdb.WorkspaceIdentifier{
			ID:  plan.TargetWSID,
			URI: targetURI,
		}
		headers.AllComposers = append(headers.AllComposers, meta)
		byID[composerID] = len(headers.AllComposers) - 1
		added++
	}

	// Drop empty stub headers on --to (cleanup) and on --from (otherwise Agents
	// keeps an "old path" bucket forever — rewrite skips promoting empty stubs).
	removed := 0
	if plan.Mode == ModeExact {
		filtered := make([]vscdb.ComposerMeta, 0, len(headers.AllComposers))
		for _, c := range headers.AllComposers {
			if !composerIsEmpty(db, c.ComposerID, c.Name) {
				filtered = append(filtered, c)
				continue
			}
			onTarget := c.WorkspaceIdentifier != nil && c.WorkspaceIdentifier.ID == plan.TargetWSID
			onSource := false
			if c.WorkspaceIdentifier != nil {
				if c.WorkspaceIdentifier.ID != "" && sourceSet[c.WorkspaceIdentifier.ID] {
					onSource = true
				}
				if fp := uriFsPath(c.WorkspaceIdentifier.URI); fp != "" {
					if matches(fp, plan.From, plan.Mode) || filepath.Clean(fp) == filepath.Clean(plan.From) {
						onSource = true
					}
				}
			}
			if onTarget || onSource {
				removed++
				continue
			}
			filtered = append(filtered, c)
		}
		headers.AllComposers = filtered
	}
	if updated == 0 && added == 0 && removed == 0 {
		// Still sync the Agents Window table — it can lag behind ItemTable.
		if err := syncComposerHeadersTable(db, plan, headers.AllComposers, sourceSet); err != nil {
			return 0, 0, err
		}
		return 0, 0, nil
	}
	if err := vscdb.SetItemJSON(db, "composer.composerHeaders", headers); err != nil {
		return updated, added, err
	}
	// Cursor 3 Agents Window reads the dedicated composerHeaders SQL table, not
	// only ItemTable["composer.composerHeaders"]. Leaving that table on --from
	// is why IDE chats load while Agents still shows /home/ulap177/... paths.
	if err := syncComposerHeadersTable(db, plan, headers.AllComposers, sourceSet); err != nil {
		return updated, added, err
	}
	return updated, added, nil
}

// syncComposerHeadersTable updates/deletes rows in the composerHeaders table so
// Agents Window matches ItemTable. Safe no-op when the table is absent.
func syncComposerHeadersTable(db *sql.DB, plan *Plan, keep []vscdb.ComposerMeta, sourceSet map[string]bool) error {
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='composerHeaders'`).Scan(&name)
	if err == sql.ErrNoRows || name == "" {
		return nil
	}
	if err != nil {
		return err
	}

	keepIDs := map[string]vscdb.ComposerMeta{}
	for _, c := range keep {
		if c.ComposerID != "" {
			keepIDs[c.ComposerID] = c
		}
	}

	rows, err := db.Query(`SELECT composerId, workspaceId, value FROM composerHeaders`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type row struct {
		id, wid, val string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.wid, &r.val); err != nil {
			return err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	targetURI := buildURI(plan.To)
	for _, r := range all {
		meta, ok := keepIDs[r.id]
		if !ok {
			// Drop empty/source stubs removed from ItemTable, or orphans still on --from.
			drop := sourceSet[r.wid] || r.wid == ""
			if !drop && r.val != "" {
				var blob map[string]any
				if json.Unmarshal([]byte(r.val), &blob) == nil {
					if wi, _ := blob["workspaceIdentifier"].(map[string]any); wi != nil {
						if id, _ := wi["id"].(string); sourceSet[id] {
							drop = true
						}
						if uri, _ := wi["uri"].(map[string]any); uri != nil {
							fp := mapFsPath(uri)
							if fp != "" && (matches(fp, plan.From, plan.Mode) || filepath.Clean(fp) == filepath.Clean(plan.From)) {
								drop = true
							}
						}
					}
				}
			}
			if drop && composerIsEmpty(db, r.id, "") {
				if _, err := db.Exec(`DELETE FROM composerHeaders WHERE composerId = ?`, r.id); err != nil {
					return err
				}
			}
			continue
		}

		// Ensure kept chats bind to --to in the Agents table without clobbering
		// richer fields stored only in this table (unread, checkpoint, etc.).
		//
		// Cold-start bug: Agents Window paints from this SQL table first. If the
		// row still has --from (e.g. mover) while ItemTable/composerData already
		// say --to (cursor-rebind), the chat flashes under the old folder then
		// jumps after composerData loads. Always reconcile SQL → --to when the
		// ItemTable header belongs to this migrate.
		wid := r.wid
		val := r.val
		needWrite := false
		if plan.Mode == ModeExact && plan.TargetWSID != "" {
			fromSlash := filepath.ToSlash(filepath.Clean(plan.From))
			fromClean := filepath.Clean(plan.From)
			toClean := filepath.Clean(plan.To)
			onSource := sourceSet[wid]
			if !onSource && r.val != "" {
				if strings.Contains(r.val, fromSlash) || strings.Contains(r.val, fromClean) {
					onSource = true
				}
			}
			metaOnTarget := false
			if meta.WorkspaceIdentifier != nil {
				if meta.WorkspaceIdentifier.ID == plan.TargetWSID {
					metaOnTarget = true
				}
				if fp := uriFsPath(meta.WorkspaceIdentifier.URI); fp != "" && filepath.Clean(fp) == toClean {
					metaOnTarget = true
				}
			}
			sqlMismatch := metaOnTarget && (wid != plan.TargetWSID || onSource)
			if onSource || sqlMismatch || (metaOnTarget && wid != plan.TargetWSID) {
				wid = plan.TargetWSID
				needWrite = true
			}
			if onSource || sqlMismatch || wid == plan.TargetWSID || metaOnTarget {
				patched, changed, err := patchComposerHeaderValueWorkspace(r.val, plan.TargetWSID, plan.To)
				if err != nil {
					return err
				}
				if changed {
					val = patched
					needWrite = true
				} else if wid != r.wid {
					// Column alone was wrong; keep value but rewrite workspaceId.
					needWrite = true
				}
			}
		}
		if !needWrite {
			continue
		}
		if _, err := db.Exec(`UPDATE composerHeaders SET workspaceId = ?, value = ? WHERE composerId = ?`, wid, val, r.id); err != nil {
			return err
		}
		_ = ensureGlassAgentTabState(db, plan, r.id)
	}

	// Upsert any ItemTable composers missing from the Agents table.
	for id, meta := range keepIDs {
		var exists int
		_ = db.QueryRow(`SELECT COUNT(1) FROM composerHeaders WHERE composerId = ?`, id).Scan(&exists)
		if exists > 0 {
			continue
		}
		if meta.WorkspaceIdentifier == nil && plan.Mode == ModeExact {
			meta.WorkspaceIdentifier = &vscdb.WorkspaceIdentifier{ID: plan.TargetWSID, URI: targetURI}
		}
		raw, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		wid := plan.TargetWSID
		if meta.WorkspaceIdentifier != nil && meta.WorkspaceIdentifier.ID != "" {
			wid = meta.WorkspaceIdentifier.ID
		}
		_, err = db.Exec(`INSERT INTO composerHeaders(composerId, workspaceId, createdAt, lastUpdatedAt, isArchived, isSubagent, recency, checkpointAt, value)
			VALUES(?, ?, ?, ?, 0, 0, ?, ?, ?)`,
			id, wid, meta.CreatedAt, meta.LastUpdatedAt, meta.LastUpdatedAt, meta.LastUpdatedAt, string(raw))
		if err != nil {
			return err
		}
	}
	return nil
}

func patchComposerHeaderValueWorkspace(raw, targetID, toPath string) (string, bool, error) {
	if raw == "" {
		return raw, false, nil
	}
	var blob map[string]any
	if err := json.Unmarshal([]byte(raw), &blob); err != nil {
		return raw, false, nil
	}
	uri := workspaceURIMap(toPath)
	wi, _ := blob["workspaceIdentifier"].(map[string]any)
	changed := false
	if wi == nil {
		blob["workspaceIdentifier"] = map[string]any{"id": targetID, "uri": uri}
		changed = true
	} else {
		if id, _ := wi["id"].(string); id != targetID {
			wi["id"] = targetID
			changed = true
		}
		if prev, _ := wi["uri"].(map[string]any); prev == nil || mapFsPath(prev) != filepath.Clean(toPath) {
			wi["uri"] = uri
			changed = true
		}
		blob["workspaceIdentifier"] = wi
	}
	// Promote chat → agent so Agents Window hydrates the conversation.
	if um, _ := blob["unifiedMode"].(string); um != "agent" {
		blob["unifiedMode"] = "agent"
		changed = true
	}
	if fm, _ := blob["forceMode"].(string); fm == "" || fm == "chat" {
		blob["forceMode"] = "edit"
		changed = true
	}
	// Agents repo grouping uses trackedGitRepos on the header row.
	// Must include branches:[] — glass does entry.branches.map without null checks.
	repos := normalizeTrackedGitRepos(blob["trackedGitRepos"])
	if len(repos) == 0 {
		repos = []any{map[string]any{"repoPath": filepath.Clean(toPath), "branches": []any{}}}
		changed = true
	} else {
		before, _ := json.Marshal(blob["trackedGitRepos"])
		after, _ := json.Marshal(repos)
		if string(before) != string(after) {
			changed = true
		}
	}
	blob["trackedGitRepos"] = repos
	if !changed {
		return raw, false, nil
	}
	out, err := json.Marshal(blob)
	if err != nil {
		return raw, false, err
	}
	return string(out), true, nil
}

// ensureGlassAgentTabState creates the per-agent glass tabs key Agents expects
// when opening a migrated chat (missing on chats that only lived in IDE).
func ensureGlassAgentTabState(db *sql.DB, plan *Plan, composerID string) error {
	if plan == nil || plan.TargetWSID == "" || composerID == "" {
		return nil
	}
	key := "cursor/glass.tabs.v2/" + plan.TargetWSID + "/" + composerID + "/state.json"
	if _, ok, _ := vscdb.GetItemRaw(db, key); ok {
		return nil
	}
	body := map[string]any{
		"version":             1,
		"agentId":             composerID,
		"browserTabs":         []any{},
		"tabOrder":            []any{},
		"rememberedAppTabIds": map[string]any{},
	}
	return vscdb.SetItemJSON(db, key, body)
}

func transcriptComposerIDs(plan *Plan) []string {
	var out []string
	seen := map[string]bool{}
	addDir := func(name string) {
		if name == "" {
			return
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		dir := filepath.Join(home, ".cursor", "projects", name, "agent-transcripts")
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			id := e.Name()
			if e.IsDir() {
				if looksLikeUUID(id) && !seen[id] {
					seen[id] = true
					out = append(out, id)
				}
				continue
			}
			base := strings.TrimSuffix(id, filepath.Ext(id))
			if looksLikeUUID(base) && !seen[base] {
				seen[base] = true
				out = append(out, base)
			}
		}
	}
	addDir(plan.ProjectFrom)
	addDir(plan.ProjectTo)
	return out
}

func composerBubbleCount(db *sql.DB, composerID string) int {
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM cursorDiskKV WHERE key LIKE ?`, "bubbleId:"+composerID+":%").Scan(&n)
	return n
}

func composerIsEmpty(db *sql.DB, composerID, headerName string) bool {
	if composerID == "" {
		return true
	}
	bubbles := composerBubbleCount(db, composerID)
	if bubbles > 0 {
		return false
	}
	meta := loadComposerMeta(db, composerID)
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = strings.TrimSpace(headerName)
	}
	switch strings.ToLower(name) {
	case "", "new agent", "new chat", "restored chat", "untitled", "composer":
		return true
	}
	// Named but no bubbles yet — treat as stub.
	return true
}

func loadComposerMeta(db *sql.DB, composerID string) vscdb.ComposerMeta {
	meta := vscdb.ComposerMeta{ComposerID: composerID, Name: "Restored chat"}
	raw, ok, err := vscdb.GetDiskKVRaw(db, "composerData:"+composerID)
	if err != nil || !ok {
		raw, ok, err = vscdb.GetItemRaw(db, "composerData:"+composerID)
		if err != nil || !ok {
			return meta
		}
	}
	var blob map[string]any
	if json.Unmarshal(raw, &blob) != nil {
		return meta
	}
	if name, ok := blob["name"].(string); ok && name != "" {
		meta.Name = name
	}
	if mode, ok := blob["unifiedMode"].(string); ok {
		meta.UnifiedMode = mode
	}
	if sub, ok := blob["subtitle"].(string); ok {
		meta.Subtitle = sub
	}
	if v, ok := blob["createdAt"].(float64); ok {
		meta.CreatedAt = int64(v)
	}
	if v, ok := blob["lastUpdatedAt"].(float64); ok {
		meta.LastUpdatedAt = int64(v)
	}
	if meta.LastUpdatedAt == 0 {
		meta.LastUpdatedAt = time.Now().UnixMilli()
	}
	return meta
}

// transferWorkspaceTabsPrimary binds one contentful composer into the target workspace
// and returns the primary composer id (empty if nothing contentful was found).
func transferWorkspaceTabsPrimary(wsRoot, globalDBPath string, plan *Plan) (string, error) {
	if plan.TargetWSID == "" {
		return "", nil
	}
	targetDB := filepath.Join(wsRoot, plan.TargetWSID, "state.vscdb")

	globalDB, gerr := vscdb.OpenReadOnly(globalDBPath)
	if gerr != nil {
		return "", fmt.Errorf("open global db for emptiness checks: %w", gerr)
	}

	var contentful []string
	seen := map[string]bool{}
	consider := func(id string) {
		if id == "" || seen[id] {
			return
		}
		if composerIsEmpty(globalDB, id, "") {
			return
		}
		seen[id] = true
		contentful = append(contentful, id)
	}

	// Prefer composers that were actually open on a source workspace (stable UX),
	// then transcripts, then headers already pointing at the target.
	var sourceSelected []string
	for _, sid := range plan.SourceWSIDs {
		ids := readWorkspaceComposerIDs(filepath.Join(wsRoot, sid, "state.vscdb"))
		sourceSelected = append(sourceSelected, ids...)
		for _, id := range ids {
			consider(id)
		}
	}
	for _, id := range transcriptComposerIDs(plan) {
		consider(id)
	}
	// Editor restore on the target often still points at the real chat after
	// selectedComposerIds was replaced with an empty stub.
	for _, id := range readWorkspaceComposerIDs(targetDB) {
		consider(id)
	}
	for _, id := range headerComposerIDsForPlan(globalDB, plan) {
		consider(id)
	}

	if len(contentful) > 1 {
		sort.SliceStable(contentful, func(i, j int) bool {
			bi, bj := composerBubbleCount(globalDB, contentful[i]), composerBubbleCount(globalDB, contentful[j])
			if bi != bj {
				return bi > bj
			}
			mi, mj := loadComposerMeta(globalDB, contentful[i]), loadComposerMeta(globalDB, contentful[j])
			return mi.LastUpdatedAt > mj.LastUpdatedAt
		})
	}

	// Bind at most one primary conversation into the open editor. History/sidebar
	// still lists everything via composer.composerHeaders.
	if len(contentful) > 1 {
		primary := contentful[0] // richest after sort
		switch plan.Strategy {
		case StrategyReplaceEmpty, StrategyCreate:
			// Prefer a composer that was open on the source workspace.
			for _, id := range sourceSelected {
				if !composerIsEmpty(globalDB, id, "") {
					primary = id
					break
				}
			}
		case StrategyMerge:
			// Prefer richest overall; if target already focused a contentful chat, keep it.
			for _, id := range readWorkspaceComposerIDs(targetDB) {
				if !composerIsEmpty(globalDB, id, "") {
					// Keep target focus only when it is among the richest (top bubble count).
					if composerBubbleCount(globalDB, id) >= composerBubbleCount(globalDB, primary) {
						primary = id
					}
					break
				}
			}
		}
		contentful = []string{primary}
	}
	_ = globalDB.Close()

	// Never wipe a working tab state with an empty selection (re-migrate after orphan).
	if len(contentful) == 0 {
		return "", nil
	}

	if _, err := os.Stat(targetDB); err != nil {
		if err := os.MkdirAll(filepath.Dir(targetDB), 0o755); err != nil {
			return "", err
		}
		f, cerr := os.Create(targetDB)
		if cerr != nil {
			return "", cerr
		}
		_ = f.Close()
		db, err := vscdb.OpenReadWrite(targetDB)
		if err != nil {
			return "", err
		}
		_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS ItemTable (key TEXT UNIQUE, value BLOB)`)
		_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS cursorDiskKV (key TEXT UNIQUE, value BLOB)`)
		_ = vscdb.CheckpointWAL(db)
		_ = db.Close()
	}

	db, err := vscdb.OpenReadWrite(targetDB)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = vscdb.CheckpointWAL(db)
		_ = db.Close()
	}()

	var data vscdb.ComposerData
	_, _ = vscdb.GetItemJSON(db, "composer.composerData", &data)
	data.SelectedComposerIDs = append([]string{}, contentful...)
	data.LastFocusedComposerIDs = []string{contentful[0]}
	data.HasMigratedComposerData = true
	data.HasMigratedMultipleComposers = true
	// Drop leftover empty stubs from allComposers so Cursor doesn't reopen them.
	var kept []vscdb.ComposerMeta
	for _, c := range data.AllComposers {
		if c.ComposerID == contentful[0] {
			kept = append(kept, c)
		}
	}
	data.AllComposers = kept
	if err := vscdb.SetItemJSON(db, "composer.composerData", data); err != nil {
		return "", err
	}

	// Prefer copying Cursor's own editor serialization from a source (or orphan).
	editorIDs := append([]string{}, plan.SourceWSIDs...)
	editorIDs = append(editorIDs, plan.TargetWSID)
	if raw := findSourceEditorState(wsRoot, editorIDs); len(raw) > 0 {
		patched := rewriteEditorStateComposerIDs(raw, contentful[0])
		if err := vscdb.SetItemRaw(db, "workbench.parts.embeddedAuxBarEditor.state", patched); err != nil {
			return "", err
		}
	} else if err := vscdb.SetItemJSON(db, "workbench.parts.embeddedAuxBarEditor.state", buildComposerEditorState(contentful)); err != nil {
		return "", err
	}
	if err := vscdb.SetItemRaw(db, "workbench.parts.embeddedAuxBarEditor.lastActivePart", []byte("embedded")); err != nil {
		return "", err
	}
	_ = vscdb.SetItemRaw(db, "cursor/needsComposerInitialOpening", []byte("false"))

	if bg := readBestBackgroundComposer(wsRoot, plan); bg != nil {
		if remote, ok := bg["cachedSelectedRemote"].(map[string]any); ok {
			remote["rootUri"] = workspaceURIMap(plan.To)
			bg["cachedSelectedRemote"] = remote
		}
		_ = vscdb.SetItemJSON(db, "workbench.backgroundComposer.workspacePersistentData", bg)
	}

	_ = clearComposerStuckFlags(globalDBPath, contentful[0])
	return contentful[0], nil
}

func headerComposerIDsForPlan(db *sql.DB, plan *Plan) []string {
	var headers vscdb.ComposerHeaders
	ok, err := vscdb.GetItemJSON(db, "composer.composerHeaders", &headers)
	if err != nil || !ok {
		return nil
	}
	sourceSet := toSet(plan.SourceWSIDs)
	var out []string
	for _, c := range headers.AllComposers {
		if c.ComposerID == "" || c.WorkspaceIdentifier == nil {
			continue
		}
		hit := sourceSet[c.WorkspaceIdentifier.ID] || c.WorkspaceIdentifier.ID == plan.TargetWSID
		if c.WorkspaceIdentifier.URI != nil {
			fp := uriFsPath(c.WorkspaceIdentifier.URI)
			if fp != "" {
				clean := filepath.Clean(fp)
				if matches(clean, plan.From, plan.Mode) || clean == filepath.Clean(plan.To) {
					hit = true
				}
			}
		}
		if hit {
			out = append(out, c.ComposerID)
		}
	}
	return out
}

func findSourceEditorState(wsRoot string, sourceIDs []string) []byte {
	for _, id := range sourceIDs {
		db, err := vscdb.OpenReadOnly(filepath.Join(wsRoot, id, "state.vscdb"))
		if err != nil {
			continue
		}
		raw, ok, _ := vscdb.GetItemRaw(db, "workbench.parts.embeddedAuxBarEditor.state")
		_ = db.Close()
		if ok && len(raw) > 0 {
			return raw
		}
	}
	return nil
}

func rewriteEditorStateComposerIDs(raw []byte, primaryID string) []byte {
	var state map[string]any
	if json.Unmarshal(raw, &state) != nil {
		return raw
	}
	val, _ := json.Marshal(map[string]any{
		"composerId":                  primaryID,
		"restoreInRegularEditorGroup": true,
	})
	editor := map[string]any{
		"id":    "workbench.editor.composer.input",
		"value": string(val),
	}
	grid, _ := state["serializedGrid"].(map[string]any)
	if grid == nil {
		return mustJSON(buildComposerEditorState([]string{primaryID}))
	}
	root, _ := grid["root"].(map[string]any)
	if root == nil {
		return mustJSON(buildComposerEditorState([]string{primaryID}))
	}
	data, _ := root["data"].([]any)
	if len(data) == 0 {
		return mustJSON(buildComposerEditorState([]string{primaryID}))
	}
	leaf, _ := data[0].(map[string]any)
	if leaf == nil {
		return mustJSON(buildComposerEditorState([]string{primaryID}))
	}
	inner, _ := leaf["data"].(map[string]any)
	if inner == nil {
		inner = map[string]any{}
		leaf["data"] = inner
	}
	inner["editors"] = []any{editor}
	inner["mru"] = []any{0}
	if _, ok := inner["id"]; !ok {
		inner["id"] = 1
	}
	out, err := json.Marshal(state)
	if err != nil {
		return raw
	}
	return out
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}

func clearComposerStuckFlags(globalDBPath, composerID string) error {
	db, err := vscdb.OpenReadWrite(globalDBPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = vscdb.CheckpointWAL(db)
		_ = db.Close()
	}()
	raw, ok, err := vscdb.GetDiskKVRaw(db, "composerData:"+composerID)
	if err != nil || !ok {
		return err
	}
	var blob map[string]any
	if json.Unmarshal(raw, &blob) != nil {
		return nil
	}
	if !clearStuckFlagsInBlob(blob) {
		return nil
	}
	out, err := json.Marshal(blob)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO cursorDiskKV(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "composerData:"+composerID, string(out))
	return err
}

func buildComposerEditorState(composerIDs []string) map[string]any {
	editors := make([]any, 0, len(composerIDs))
	mru := make([]any, 0, len(composerIDs))
	for i, id := range composerIDs {
		val, _ := json.Marshal(map[string]any{
			"composerId":                  id,
			"restoreInRegularEditorGroup": true,
		})
		editors = append(editors, map[string]any{
			"id":    "workbench.editor.composer.input",
			"value": string(val),
		})
		mru = append(mru, i)
	}
	return map[string]any{
		"serializedGrid": map[string]any{
			"root": map[string]any{
				"type": "branch",
				"data": []any{
					map[string]any{
						"type": "leaf",
						"data": map[string]any{
							"id":      1,
							"editors": editors,
							"mru":     mru,
						},
						"size": 680,
					},
				},
				"size": 440,
			},
			"orientation": 0,
			"width":       440,
			"height":      680,
		},
		"activeGroup":            1,
		"mostRecentActiveGroups": []any{1},
	}
}

func readBestBackgroundComposer(wsRoot string, plan *Plan) map[string]any {
	try := append([]string{}, plan.SourceWSIDs...)
	try = append(try, plan.TargetWSID)
	for _, id := range try {
		if id == "" {
			continue
		}
		db, err := vscdb.OpenReadOnly(filepath.Join(wsRoot, id, "state.vscdb"))
		if err != nil {
			continue
		}
		var bg map[string]any
		ok, _ := vscdb.GetItemJSON(db, "workbench.backgroundComposer.workspacePersistentData", &bg)
		_ = db.Close()
		if ok && bg != nil {
			return bg
		}
	}
	return nil
}

// detachOrphanWorkspaces keeps only the target workspaceStorage entry on the
// destination folder URI. Leftover rename duplicates otherwise confuse Cursor.
func detachOrphanWorkspaces(wsRoot string, plan *Plan) error {
	if plan.Mode != ModeExact || plan.TargetWSID == "" {
		return nil
	}
	return orphanWorkspaceFolders(wsRoot, plan.To, plan.TargetWSID)
}

func rewriteWorkspaceJSON(wsRoot string, plan *Plan) error {
	entries, err := os.ReadDir(wsRoot)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
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
		changed := false
		if folder, ok := meta["folder"].(string); ok {
			fp := paths.PathFromFileURI(folder)
			if matches(fp, plan.From, plan.Mode) {
				newPath := rewritePath(fp, plan.From, plan.To, plan.Mode)
				meta["folder"] = paths.FileURI(newPath)
				changed = true
			}
		}
		if ws, ok := meta["workspace"].(string); ok {
			fp := paths.PathFromFileURI(ws)
			if matches(fp, plan.From, plan.Mode) {
				newPath := rewritePath(fp, plan.From, plan.To, plan.Mode)
				meta["workspace"] = paths.FileURI(newPath)
				changed = true
			}
		}
		if !changed {
			continue
		}
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

func rewriteProjects(projectsDir string, plan *Plan) (bool, error) {
	if plan.Mode == ModeExact {
		if !plan.ProjectExists {
			return false, nil
		}
		src := filepath.Join(projectsDir, plan.ProjectFrom)
		dst := filepath.Join(projectsDir, plan.ProjectTo)
		if _, err := os.Stat(src); err != nil {
			// Already retired/merged earlier, or never present at apply time.
			return false, nil
		}
		if _, err := os.Stat(dst); err == nil {
			if err := mergeDir(src, dst); err != nil {
				return false, err
			}
			_ = os.RemoveAll(src)
			return true, nil
		}
		if err := os.Rename(src, dst); err != nil {
			return false, err
		}
		return true, nil
	}

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return false, err
	}
	moved := false
	fromSan := paths.SanitizeProjectPath(plan.From)
	toSan := paths.SanitizeProjectPath(plan.To)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		var newName string
		if fromSan != "" && (name == fromSan || strings.HasPrefix(name, fromSan+"-")) {
			newName = toSan + name[len(fromSan):]
		}
		if newName == "" || newName == name {
			continue
		}
		src := filepath.Join(projectsDir, name)
		dst := filepath.Join(projectsDir, newName)
		if _, err := os.Stat(dst); err == nil {
			if err := mergeDir(src, dst); err != nil {
				return moved, err
			}
			_ = os.RemoveAll(src)
		} else {
			if err := os.Rename(src, dst); err != nil {
				return moved, err
			}
		}
		moved = true
	}
	return moved, nil
}

func mergeDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if _, err := os.Stat(target); err == nil {
			return nil
		}
		in, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, in, info.Mode())
	})
}

func ensureWorkspace(roots paths.Roots, folderPath string) (string, error) {
	entries, _ := os.ReadDir(roots.WorkspaceStorage)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(roots.WorkspaceStorage, e.Name(), "workspace.json")
		raw, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var meta struct {
			Folder string `json:"folder"`
		}
		if json.Unmarshal(raw, &meta) != nil {
			continue
		}
		if filepath.Clean(paths.PathFromFileURI(meta.Folder)) == filepath.Clean(folderPath) {
			return e.Name(), nil
		}
	}

	id, err := randomID()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(roots.WorkspaceStorage, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	meta := map[string]string{"folder": paths.FileURI(folderPath)}
	raw, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "workspace.json"), raw, 0o644); err != nil {
		return "", err
	}
	return id, nil
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// RepairResult is the outcome of RepairTabs.
type RepairResult struct {
	BackupID             string
	PrimaryComposerID    string
	ComposersRewritten   int
	GlassProjectsUpdated int
	GlassKeysMoved       int
	TranscriptsWritten   int
	SourceStoragePurged  int
	HealthOK             bool
	HealthIssues         []string
}

// RepairTabs rebinds IDE open-tab + Agents Window identity after a partial migrate.
// Unlike Apply, it does not require contentful chats still sitting on --from — it
// still rewrites headers (to drop empty --from stubs), composerData, glass, and
// retires leftover --from Agents roots that keep showing the old machine path.
// cleanup removes orphaned source workspaceStorage after success (opt-in).
func RepairTabs(inv *discover.Inventory, plan *Plan, yes, cleanup bool) (*RepairResult, error) {
	if plan == nil {
		return nil, fmt.Errorf("nil plan")
	}
	if plan.Mode != ModeExact {
		return nil, fmt.Errorf("repair only supports exact mode")
	}
	if plan.TargetWSID == "" {
		return nil, fmt.Errorf("no target workspace id — pass --target-id or open the project once in Cursor")
	}
	if !yes {
		return nil, fmt.Errorf("refusing to write without --yes")
	}
	if err := guard.EnsureCursorClosed(); err != nil {
		return nil, err
	}

	id, bdir, man, err := backup.Create(fmt.Sprintf("repair %s → %s", plan.From, plan.To))
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	_ = backup.CopyFile(bdir, man, "global/state.vscdb", inv.Roots.GlobalDB)
	for _, sid := range plan.SourceWSIDs {
		_ = backup.CopyFile(bdir, man, "ws/"+sid+"/state.vscdb", filepath.Join(inv.Roots.WorkspaceStorage, sid, "state.vscdb"))
	}
	if plan.TargetWSID != "" {
		_ = backup.CopyFile(bdir, man, "ws/"+plan.TargetWSID+"/state.vscdb", filepath.Join(inv.Roots.WorkspaceStorage, plan.TargetWSID, "state.vscdb"))
	}
	if err := backup.WriteManifest(bdir, man); err != nil {
		return nil, fmt.Errorf("backup manifest: %w", err)
	}

	// Drop empty --from stubs + retag any leftover headers. Skipping this left
	// Agents Window permanently bound to /home/ulap177/.../ai via a ghost header
	// even when the real chat already pointed at --to (IDE worked, Agents empty).
	if _, _, err := rewriteHeaders(inv.Roots.GlobalDB, inv.Roots.WorkspaceStorage, plan); err != nil {
		return nil, fmt.Errorf("repair headers: %w", err)
	}
	idRes, err := applyExactIdentity(inv, plan, exactIdentityOpts{
		rewriteProjects:      false,
		cleanupSourceStorage: cleanup,
	})
	if err != nil {
		return nil, fmt.Errorf("repair: %w", err)
	}
	out := &RepairResult{
		BackupID:             id,
		PrimaryComposerID:    idRes.PrimaryComposerID,
		ComposersRewritten:   idRes.ComposersRewritten,
		GlassProjectsUpdated: idRes.GlassProjectsUpdated,
		GlassKeysMoved:       idRes.GlassKeysMoved,
		TranscriptsWritten:   idRes.TranscriptsWritten,
		SourceStoragePurged:  idRes.SourceStoragePurged,
	}
	if err := enforceExactHealth(inv.Roots, plan.To, plan.TargetWSID, id); err != nil {
		if ue, ok := err.(*UnhealthyError); ok && ue.Report != nil {
			out.HealthOK = false
			out.HealthIssues = append([]string{}, ue.Report.Issues...)
		}
		return out, err
	}
	out.HealthOK = true
	return out, nil
}

func normalizeStorageText(globalDB, wsRoot string, plan *Plan) error {
	paths := []string{globalDB}
	if plan != nil && plan.TargetWSID != "" {
		paths = append(paths, filepath.Join(wsRoot, plan.TargetWSID, "state.vscdb"))
	}
	if plan != nil {
		for _, sid := range plan.SourceWSIDs {
			if sid == "" || (plan.TargetWSID != "" && sid == plan.TargetWSID) {
				continue
			}
			paths = append(paths, filepath.Join(wsRoot, sid, "state.vscdb"))
		}
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		db, err := vscdb.OpenReadWrite(p)
		if err != nil {
			return err
		}
		if _, err := vscdb.NormalizeItemTableText(db); err != nil {
			_ = db.Close()
			return err
		}
		_ = vscdb.CheckpointWAL(db)
		_ = db.Close()
	}
	return nil
}

// VerifyReport is the outcome of VerifyPath.
type VerifyReport struct {
	Exact   int
	Loose   int
	Agent   int
	Health  *HealthReport
}

// Verify reports how many headers currently point at path.
func Verify(inv *discover.Inventory, path string) (exact, loose int, agent int) {
	rep := VerifyPath(inv, path)
	return rep.Exact, rep.Loose, rep.Agent
}

// VerifyPath counts headers/transcripts and assesses dual-workspace health.
func VerifyPath(inv *discover.Inventory, path string) VerifyReport {
	path = filepath.Clean(path)
	base := filepath.Base(path)
	var exact, loose, agent int
	for _, e := range inv.Headers.Entries {
		if e.WorkspacePath == "" {
			continue
		}
		fp := filepath.Clean(e.WorkspacePath)
		if fp == path {
			exact++
		} else if filepath.Base(fp) == base {
			loose++
		}
	}
	san := paths.SanitizeProjectPath(path)
	for _, p := range inv.Projects {
		if p.Name == san || strings.HasSuffix(p.Name, "-"+strings.ReplaceAll(base, "_", "")) {
			agent += p.TranscriptCount
		}
	}
	return VerifyReport{
		Exact:  exact,
		Loose:  loose,
		Agent:  agent,
		Health: AssessPathHealth(inv, path, ""),
	}
}

// Restore applies a backup by id.
func Restore(backupID string) error {
	if err := guard.EnsureCursorClosed(); err != nil {
		return err
	}
	root, err := backup.Dir()
	if err != nil {
		return err
	}
	bdir := filepath.Join(root, backupID)
	man, err := backup.LoadManifest(bdir)
	if err != nil {
		return err
	}
	for original := range man.Files {
		if err := backup.RestoreFile(bdir, man, original); err != nil {
			return fmt.Errorf("restore %s: %w", original, err)
		}
	}
	return nil
}
