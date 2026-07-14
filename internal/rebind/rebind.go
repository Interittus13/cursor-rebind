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
	Mode               Mode     `json:"mode"`
	From               string   `json:"from"`
	To                 string   `json:"to"`
	TargetWSID         string   `json:"targetWorkspaceId,omitempty"`
	SourceWSIDs        []string `json:"sourceWorkspaceIds,omitempty"`
	HeadersByPath      int      `json:"headersByPath"`
	HeadersByWorkspace int      `json:"headersByWorkspaceId"`
	HeadersMatched     int      `json:"headersMatched"`
	ComposersFromWS    int      `json:"composersFromWorkspaceDb"`
	ProjectFrom        string   `json:"projectFrom,omitempty"`
	ProjectTo          string   `json:"projectTo,omitempty"`
	ProjectExists      bool     `json:"projectFromExists"`
	TargetExists       bool     `json:"targetPathExists"`
	Warnings           []string `json:"warnings,omitempty"`
	Ops                []Op     `json:"ops"`
}

// Op is one planned mutation.
type Op struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Count  int    `json:"count,omitempty"`
}

// Result is the outcome of Apply.
type Result struct {
	BackupID         string `json:"backupId"`
	HeadersUpdated   int    `json:"headersUpdated"`
	HeadersAdded     int    `json:"headersAdded"`
	ProjectMoved     bool   `json:"projectMoved"`
	TargetWSID       string `json:"targetWorkspaceId"`
	WorkspaceJSONSkip bool  `json:"workspaceJsonSkipped"`
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

	p.SourceWSIDs = findSourceWorkspaceIDs(inv, from, to, p.TargetWSID, mode)
	if len(p.SourceWSIDs) == 0 && mode == ModeExact {
		p.Warnings = append(p.Warnings, "no source workspaceStorage id found for --from (path may already have been rewritten)")
	}

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
		Kind:   "rewrite-headers",
		Detail: fmt.Sprintf("retag/register composer.composerHeaders for %s → %s", from, to),
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
			Kind:   "transfer-workspace-tabs",
			Detail: "bind one primary contentful composer + copy Cursor editor restore state onto the target",
		})
		p.Ops = append(p.Ops, Op{
			Kind:   "rewrite-glass-projects",
			Detail: "retag Agents Window glass.localAgentProjects from --from → --to",
		})
		p.Ops = append(p.Ops, Op{
			Kind:   "detach-orphan-workspaces",
			Detail: "point leftover workspaceStorage folders away from the target path so Cursor opens one identity",
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
	// Attach chats onto the shell Cursor opens for the new path: fewest *contentful*
	// composers, then newest mtime. Do NOT prefer fewest raw tab IDs — empty
	// "New Agent" stubs inflate counts and previously inverted source/target.
	type scored struct {
		id         string
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
		ids := readWorkspaceComposerIDs(filepath.Join(inv.Roots.WorkspaceStorage, w.ID, "state.vscdb"))
		contentful := 0
		if gdb != nil {
			for _, cid := range ids {
				if !composerIsEmpty(gdb, cid, "") {
					contentful++
				}
			}
		}
		list = append(list, scored{id: w.ID, contentful: contentful, modTime: w.ModTime})
	}

	best := list[0]
	for _, s := range list[1:] {
		if s.contentful < best.contentful ||
			(s.contentful == best.contentful && s.modTime.After(best.modTime)) {
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
		// Basename match for odd leftovers.
		base := filepath.Base(from)
		for _, w := range inv.Workspaces {
			if w.FolderPath != "" && filepath.Base(filepath.Clean(w.FolderPath)) == base {
				add(w.ID)
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
	if p.TargetWSID != "" {
		fmt.Fprintf(&b, "Target ID:%s\n", p.TargetWSID)
	}
	if len(p.SourceWSIDs) > 0 {
		fmt.Fprintf(&b, "Source IDs:%s\n", strings.Join(p.SourceWSIDs, ", "))
	}
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
func Apply(inv *discover.Inventory, plan *Plan, yes, dryRun bool) (*Result, error) {
	touchable := plan.HeadersMatched > 0 || plan.ComposersFromWS > 0 || plan.ProjectExists || len(plan.SourceWSIDs) > 0
	if !touchable {
		return nil, fmt.Errorf("nothing to rebind — no matching headers, workspace ids, or agent project dirs")
	}
	if !yes && !dryRun {
		return nil, fmt.Errorf("refusing to write without --yes (use --dry-run to preview)")
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
	}

	id, bdir, man, err := backup.Create(fmt.Sprintf("rebind %s → %s (%s)", plan.From, plan.To, plan.Mode))
	if err != nil {
		return nil, err
	}
	res.BackupID = id

	global := inv.Roots.GlobalDB
	for _, side := range []string{global, global + "-wal", global + "-shm"} {
		_ = backup.CopyFile(bdir, man, "", side)
	}
	for _, sid := range plan.SourceWSIDs {
		_ = backup.CopyFile(bdir, man, "", filepath.Join(inv.Roots.WorkspaceStorage, sid, "state.vscdb"))
	}
	if plan.TargetWSID != "" {
		_ = backup.CopyFile(bdir, man, "", filepath.Join(inv.Roots.WorkspaceStorage, plan.TargetWSID, "state.vscdb"))
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
	} else {
		res.WorkspaceJSONSkip = true
		if err := transferWorkspaceTabs(inv.Roots.WorkspaceStorage, inv.Roots.GlobalDB, plan); err != nil {
			return nil, fmt.Errorf("transfer tabs: %w", err)
		}
		if err := rewriteGlassAgentProjects(inv.Roots.GlobalDB, plan); err != nil {
			return nil, fmt.Errorf("glass agent projects: %w", err)
		}
		if err := detachOrphanWorkspaces(inv.Roots.WorkspaceStorage, plan); err != nil {
			return nil, fmt.Errorf("detach orphans: %w", err)
		}
	}

	moved, err := rewriteProjects(inv.Roots.ProjectsDir, plan)
	if err != nil {
		return nil, fmt.Errorf("projects: %w", err)
	}
	res.ProjectMoved = moved

	return res, nil
}

func rewriteHeaders(globalDB, wsRoot string, plan *Plan) (updated, added int, err error) {
	db, err := vscdb.OpenReadWrite(globalDB)
	if err != nil {
		return 0, 0, err
	}
	defer db.Close()

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
				fp := c.WorkspaceIdentifier.URI.FsPath
				if fp == "" {
					fp = c.WorkspaceIdentifier.URI.Path
				}
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
		if composerIsEmpty(db, c.ComposerID, c.Name) {
			continue
		}
		if c.WorkspaceIdentifier == nil {
			c.WorkspaceIdentifier = &vscdb.WorkspaceIdentifier{}
		}
		if plan.Mode == ModeExact {
			c.WorkspaceIdentifier.ID = plan.TargetWSID
			c.WorkspaceIdentifier.URI = targetURI
		} else {
			fp := ""
			if c.WorkspaceIdentifier.URI != nil {
				fp = c.WorkspaceIdentifier.URI.FsPath
				if fp == "" {
					fp = c.WorkspaceIdentifier.URI.Path
				}
			}
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
					updated++
				} else if c.WorkspaceIdentifier.URI == nil ||
					filepath.Clean(c.WorkspaceIdentifier.URI.FsPath) != filepath.Clean(plan.To) {
					c.WorkspaceIdentifier.URI = targetURI
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

	// Drop empty stub headers that already point at the target (or leftover from bad migrates).
	removed := 0
	if plan.Mode == ModeExact && plan.TargetWSID != "" {
		filtered := make([]vscdb.ComposerMeta, 0, len(headers.AllComposers))
		for _, c := range headers.AllComposers {
			if c.WorkspaceIdentifier != nil && c.WorkspaceIdentifier.ID == plan.TargetWSID &&
				composerIsEmpty(db, c.ComposerID, c.Name) {
				removed++
				continue
			}
			filtered = append(filtered, c)
		}
		headers.AllComposers = filtered
	}
	if updated == 0 && added == 0 && removed == 0 {
		return 0, 0, nil
	}
	if err := vscdb.SetItemJSON(db, "composer.composerHeaders", headers); err != nil {
		return updated, added, err
	}
	return updated, added, nil
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

func transferWorkspaceTabs(wsRoot, globalDBPath string, plan *Plan) error {
	if plan.TargetWSID == "" {
		return nil
	}
	targetDB := filepath.Join(wsRoot, plan.TargetWSID, "state.vscdb")

	globalDB, gerr := vscdb.OpenReadOnly(globalDBPath)
	if gerr != nil {
		return fmt.Errorf("open global db for emptiness checks: %w", gerr)
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
		primary := contentful[0]
		for _, id := range sourceSelected {
			if !composerIsEmpty(globalDB, id, "") {
				primary = id
				break
			}
		}
		contentful = []string{primary}
	}
	_ = globalDB.Close()

	// Never wipe a working tab state with an empty selection (re-migrate after orphan).
	if len(contentful) == 0 {
		return nil
	}

	if _, err := os.Stat(targetDB); err != nil {
		if err := os.MkdirAll(filepath.Dir(targetDB), 0o755); err != nil {
			return err
		}
		f, cerr := os.Create(targetDB)
		if cerr != nil {
			return cerr
		}
		_ = f.Close()
		db, err := vscdb.OpenReadWrite(targetDB)
		if err != nil {
			return err
		}
		_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS ItemTable (key TEXT UNIQUE, value BLOB)`)
		_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS cursorDiskKV (key TEXT UNIQUE, value BLOB)`)
		_ = db.Close()
	}

	db, err := vscdb.OpenReadWrite(targetDB)
	if err != nil {
		return err
	}
	defer db.Close()

	var data vscdb.ComposerData
	_, _ = vscdb.GetItemJSON(db, "composer.composerData", &data)
	data.SelectedComposerIDs = append([]string{}, contentful...)
	data.LastFocusedComposerIDs = []string{contentful[0]}
	data.HasMigratedComposerData = true
	data.HasMigratedMultipleComposers = true
	if err := vscdb.SetItemJSON(db, "composer.composerData", data); err != nil {
		return err
	}

	// Prefer copying Cursor's own editor serialization from a source (or orphan).
	editorIDs := append([]string{}, plan.SourceWSIDs...)
	editorIDs = append(editorIDs, plan.TargetWSID)
	if raw := findSourceEditorState(wsRoot, editorIDs); len(raw) > 0 {
		patched := rewriteEditorStateComposerIDs(raw, contentful[0])
		if err := vscdb.SetItemRaw(db, "workbench.parts.embeddedAuxBarEditor.state", patched); err != nil {
			return err
		}
	} else if err := vscdb.SetItemJSON(db, "workbench.parts.embeddedAuxBarEditor.state", buildComposerEditorState(contentful)); err != nil {
		return err
	}
	if err := vscdb.SetItemRaw(db, "workbench.parts.embeddedAuxBarEditor.lastActivePart", []byte("embedded")); err != nil {
		return err
	}
	_ = vscdb.SetItemRaw(db, "cursor/needsComposerInitialOpening", []byte("false"))

	if bg := readBestBackgroundComposer(wsRoot, plan); bg != nil {
		if remote, ok := bg["cachedSelectedRemote"].(map[string]any); ok {
			remote["rootUri"] = map[string]any{
				"$mid":     1,
				"fsPath":   filepath.Clean(plan.To),
				"external": paths.FileURI(plan.To),
				"path":     filepath.ToSlash(filepath.Clean(plan.To)),
				"scheme":   "file",
			}
			bg["cachedSelectedRemote"] = remote
		}
		_ = vscdb.SetItemJSON(db, "workbench.backgroundComposer.workspacePersistentData", bg)
	}

	_ = clearComposerStuckFlags(globalDBPath, contentful[0])
	return nil
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
			fp := c.WorkspaceIdentifier.URI.FsPath
			if fp == "" {
				fp = c.WorkspaceIdentifier.URI.Path
			}
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
	defer db.Close()
	raw, ok, err := vscdb.GetDiskKVRaw(db, "composerData:"+composerID)
	if err != nil || !ok {
		return err
	}
	var blob map[string]any
	if json.Unmarshal(raw, &blob) != nil {
		return nil
	}
	changed := false
	if st, _ := blob["status"].(string); st == "aborted" || st == "generating" || st == "error" {
		blob["status"] = "none"
		changed = true
	}
	if _, ok := blob["generatingBubbleIds"]; ok {
		blob["generatingBubbleIds"] = []any{}
		changed = true
	}
	if !changed {
		return nil
	}
	out, err := json.Marshal(blob)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO cursorDiskKV(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "composerData:"+composerID, out)
	return err
}

func buildComposerEditorState(composerIDs []string) map[string]any {
	editors := make([]any, 0, len(composerIDs))
	mru := make([]any, 0, len(composerIDs))
	for i, id := range composerIDs {
		val, _ := json.Marshal(map[string]any{
			"composerId":                 id,
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
	to := filepath.Clean(plan.To)
	entries, err := os.ReadDir(wsRoot)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == plan.TargetWSID {
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
		folder, _ := meta["folder"].(string)
		if filepath.Clean(paths.PathFromFileURI(folder)) != to {
			continue
		}
		// Point the duplicate at a non-existent path so Cursor won't open it for `to`.
		orphan := to + ".__rebind_orphan_" + e.Name()[:8]
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

// rewriteGlassAgentProjects retags the Agents Window index (separate from IDE headers).
func rewriteGlassAgentProjects(globalDB string, plan *Plan) error {
	db, err := vscdb.OpenReadWrite(globalDB)
	if err != nil {
		return err
	}
	defer db.Close()

	raw, ok, err := vscdb.GetItemRaw(db, "glass.localAgentProjects.v1")
	if err != nil || !ok {
		return err
	}
	var projects []map[string]any
	if json.Unmarshal(raw, &projects) != nil {
		return nil
	}

	memberHit := map[string]bool{}
	if mraw, mok, _ := vscdb.GetItemRaw(db, "glass.localAgentProjectMembership.v1"); mok {
		var mem map[string]string
		if json.Unmarshal(mraw, &mem) == nil {
			for _, cid := range transcriptComposerIDs(plan) {
				if pid := mem[cid]; pid != "" {
					memberHit[pid] = true
				}
			}
			var headers vscdb.ComposerHeaders
			if ok, _ := vscdb.GetItemJSON(db, "composer.composerHeaders", &headers); ok {
				sourceSet := toSet(plan.SourceWSIDs)
				for _, c := range headers.AllComposers {
					hit := false
					if c.WorkspaceIdentifier != nil {
						if sourceSet[c.WorkspaceIdentifier.ID] || c.WorkspaceIdentifier.ID == plan.TargetWSID {
							hit = true
						}
						if c.WorkspaceIdentifier.URI != nil {
							fp := c.WorkspaceIdentifier.URI.FsPath
							if fp == "" {
								fp = c.WorkspaceIdentifier.URI.Path
							}
							if fp != "" && (matches(fp, plan.From, plan.Mode) || filepath.Clean(fp) == filepath.Clean(plan.To)) {
								hit = true
							}
						}
					}
					if hit {
						if pid := mem[c.ComposerID]; pid != "" {
							memberHit[pid] = true
						}
					}
				}
			}
		}
	}

	sourceSet := toSet(plan.SourceWSIDs)
	targetURI := map[string]any{
		"$mid":     1,
		"fsPath":   filepath.Clean(plan.To),
		"external": paths.FileURI(plan.To),
		"path":     filepath.ToSlash(filepath.Clean(plan.To)),
		"scheme":   "file",
	}
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
		uri, _ := ws["uri"].(map[string]any)
		if uri != nil {
			fp, _ := uri["fsPath"].(string)
			if fp == "" {
				fp, _ = uri["path"].(string)
			}
			if fp != "" {
				clean := filepath.Clean(fp)
				if matches(clean, plan.From, plan.Mode) || (plan.Mode == ModeExact && clean == filepath.Clean(plan.To)) {
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
		return nil
	}
	return vscdb.SetItemJSON(db, "glass.localAgentProjects.v1", projects)
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

// RepairTabs rebinds the IDE open-tab / Agents Window identity for an already-migrated
// target without rewriting headers again. Cursor must be fully quit.
func RepairTabs(inv *discover.Inventory, plan *Plan, yes bool) error {
	if plan == nil {
		return fmt.Errorf("nil plan")
	}
	if plan.Mode != ModeExact {
		return fmt.Errorf("repair only supports exact mode")
	}
	if plan.TargetWSID == "" {
		return fmt.Errorf("no target workspace id — pass --target-id or open the project once in Cursor")
	}
	if !yes {
		return fmt.Errorf("refusing to write without --yes")
	}
	if err := guard.EnsureCursorClosed(); err != nil {
		return err
	}
	if err := transferWorkspaceTabs(inv.Roots.WorkspaceStorage, inv.Roots.GlobalDB, plan); err != nil {
		return fmt.Errorf("repair tabs: %w", err)
	}
	if err := rewriteGlassAgentProjects(inv.Roots.GlobalDB, plan); err != nil {
		return fmt.Errorf("repair glass: %w", err)
	}
	return nil
}

// Verify reports how many headers currently point at path.
func Verify(inv *discover.Inventory, path string) (exact, loose int, agent int) {
	path = filepath.Clean(path)
	base := filepath.Base(path)
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
	return exact, loose, agent
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
