package rebind

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	Mode           Mode   `json:"mode"`
	From           string `json:"from"`
	To             string `json:"to"`
	TargetWSID     string `json:"targetWorkspaceId,omitempty"`
	HeadersMatched int    `json:"headersMatched"`
	ProjectFrom    string `json:"projectFrom,omitempty"`
	ProjectTo      string `json:"projectTo,omitempty"`
	ProjectExists  bool   `json:"projectFromExists"`
	TargetExists   bool   `json:"targetPathExists"`
	Warnings       []string `json:"warnings,omitempty"`
	Ops            []Op   `json:"ops"`
}

// Op is one planned mutation.
type Op struct {
	Kind    string `json:"kind"`
	Detail  string `json:"detail"`
	Count   int    `json:"count,omitempty"`
}

// Result is the outcome of Apply.
type Result struct {
	BackupID       string `json:"backupId"`
	HeadersUpdated int    `json:"headersUpdated"`
	ProjectMoved   bool   `json:"projectMoved"`
	TargetWSID     string `json:"targetWorkspaceId"`
}

// BuildPlan constructs a rebind plan from inventory + from/to.
func BuildPlan(inv *discover.Inventory, from, to string, mode Mode) (*Plan, error) {
	from = filepath.Clean(from)
	to = filepath.Clean(to)
	if from == "" || to == "" {
		return nil, fmt.Errorf("from and to paths are required")
	}
	if from == to {
		return nil, fmt.Errorf("from and to are the same path")
	}

	p := &Plan{
		Mode: mode,
		From: from,
		To:   to,
	}
	if st, err := os.Stat(to); err == nil && st.IsDir() {
		p.TargetExists = true
	} else {
		p.Warnings = append(p.Warnings, "target path does not exist on disk — open it in Cursor after migrate")
	}

	// Resolve destination workspace id (prefer an existing workspaceStorage for To).
	for _, w := range inv.Workspaces {
		if w.FolderPath != "" && filepath.Clean(w.FolderPath) == to {
			p.TargetWSID = w.ID
			break
		}
	}
	if p.TargetWSID == "" && mode == ModeExact {
		p.Warnings = append(p.Warnings, "no workspaceStorage entry for target yet — a new id will be created on apply if needed")
	}

	matched := 0
	for _, e := range inv.Headers.Entries {
		if e.WorkspacePath == "" {
			continue
		}
		fp := filepath.Clean(e.WorkspacePath)
		if matches(fp, from, mode) {
			matched++
		}
	}
	p.HeadersMatched = matched
	if matched == 0 {
		p.Warnings = append(p.Warnings, "no global chat headers match the from path")
	}

	p.Ops = append(p.Ops, Op{
		Kind:   "rewrite-headers",
		Detail: fmt.Sprintf("retag composer.composerHeaders paths %s → %s", from, to),
		Count:  matched,
	})

	if mode == ModeExact {
		p.ProjectFrom = paths.SanitizeProjectPath(from)
		p.ProjectTo = paths.SanitizeProjectPath(to)
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
			Kind:   "update-workspace-json",
			Detail: "point matching workspaceStorage entries at the new folder URI",
		})
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
	fmt.Fprintf(&b, "Headers:  %d matched\n", p.HeadersMatched)
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

// Apply executes the plan. dryRun skips mutations (still prints intent).
func Apply(inv *discover.Inventory, plan *Plan, yes, dryRun bool) (*Result, error) {
	if plan.HeadersMatched == 0 && !plan.ProjectExists && plan.Mode == ModeExact {
		return nil, fmt.Errorf("nothing to rebind — no matching headers or agent project dirs")
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

	// Ensure target workspace id.
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

	// Agent projects that may change.
	if plan.Mode == ModeExact && plan.ProjectExists {
		src := filepath.Join(inv.Roots.ProjectsDir, plan.ProjectFrom)
		_ = backup.CopyTree(bdir, man, "projects/"+plan.ProjectFrom, src)
	}

	if err := backup.WriteManifest(bdir, man); err != nil {
		return nil, err
	}

	n, err := rewriteHeaders(inv.Roots.GlobalDB, plan)
	if err != nil {
		return nil, fmt.Errorf("rewrite headers: %w", err)
	}
	res.HeadersUpdated = n

	if err := rewriteWorkspaceJSON(inv.Roots.WorkspaceStorage, plan); err != nil {
		return nil, fmt.Errorf("workspace.json: %w", err)
	}

	moved, err := rewriteProjects(inv.Roots.ProjectsDir, plan)
	if err != nil {
		return nil, fmt.Errorf("projects: %w", err)
	}
	res.ProjectMoved = moved

	return res, nil
}

func rewriteHeaders(globalDB string, plan *Plan) (int, error) {
	db, err := vscdb.OpenReadWrite(globalDB)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var headers vscdb.ComposerHeaders
	ok, err := vscdb.GetItemJSON(db, "composer.composerHeaders", &headers)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("composer.composerHeaders not found (is this Cursor 3.0+?)")
	}

	updated := 0
	for i := range headers.AllComposers {
		c := &headers.AllComposers[i]
		if c.WorkspaceIdentifier == nil || c.WorkspaceIdentifier.URI == nil {
			continue
		}
		fp := c.WorkspaceIdentifier.URI.FsPath
		if fp == "" {
			fp = c.WorkspaceIdentifier.URI.Path
		}
		if fp == "" || !matches(fp, plan.From, plan.Mode) {
			continue
		}
		newPath := rewritePath(fp, plan.From, plan.To, plan.Mode)
		uri := buildURI(newPath)
		c.WorkspaceIdentifier.URI = uri
		if plan.Mode == ModeExact && plan.TargetWSID != "" {
			c.WorkspaceIdentifier.ID = plan.TargetWSID
		} else if plan.Mode == ModePrefix {
			// Keep workspace id; Cursor resolves via uri. Optionally remap id when we can match.
			// Path-only retag is enough for many cases; leave id unless exact mode.
		}
		updated++
	}

	if updated == 0 {
		return 0, nil
	}
	if err := vscdb.SetItemJSON(db, "composer.composerHeaders", headers); err != nil {
		return 0, err
	}
	return updated, nil
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
			// Merge: move children that don't exist in dest.
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

	// Prefix mode: rewrite project dir names by reconstructing path mapping.
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return false, err
	}
	moved := false
	fromSan := paths.SanitizeProjectPath(plan.From)
	toSan := paths.SanitizeProjectPath(plan.To)
	// Prefix sanitize is imperfect; also try string replace on sanitized from→to if from is a prefix.
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
			return nil // keep destination file
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
	// Re-scan for race
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
