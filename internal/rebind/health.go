package rebind

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Interittus13/cursor-rebind/internal/discover"
	"github.com/Interittus13/cursor-rebind/internal/paths"
)

// HealthReport describes whether a project path has a single Cursor workspace
// identity with contentful chats on that same id (no dual-workspace split-brain).
type HealthReport struct {
	Path               string         `json:"path"`
	KeepID             string         `json:"keepId,omitempty"`
	LiveWorkspaceIDs   []string       `json:"liveWorkspaceIds"`
	NamedByWorkspaceID map[string]int `json:"namedByWorkspaceId"`
	NamedTotal         int            `json:"namedTotal"`
	OffTargetNamed     int            `json:"offTargetNamed"`
	SplitBrain         bool           `json:"splitBrain"`
	OK                 bool           `json:"ok"`
	Issues             []string       `json:"issues,omitempty"`
	RepairHint         string         `json:"repairHint,omitempty"`
}

// UnhealthyError is returned when migrate/repair finishes writing but the
// path still has dual live workspace ids or named chats off the keep id.
type UnhealthyError struct {
	BackupID string
	Report   *HealthReport
}

func (e *UnhealthyError) Error() string {
	if e == nil || e.Report == nil {
		return "workspace identity unhealthy after rebind"
	}
	var b strings.Builder
	b.WriteString("workspace identity unhealthy after rebind (split-brain)\n")
	for _, issue := range e.Report.Issues {
		b.WriteString("  • ")
		b.WriteString(issue)
		b.WriteByte('\n')
	}
	if e.Report.RepairHint != "" {
		b.WriteString("  → ")
		b.WriteString(e.Report.RepairHint)
		b.WriteByte('\n')
	}
	if e.BackupID != "" {
		b.WriteString("  Backup: ")
		b.WriteString(e.BackupID)
		b.WriteString(" (cursor-rebind restore ")
		b.WriteString(e.BackupID)
		b.WriteString(")\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// AssessPathHealth inspects live workspaceStorage folders and global headers
// for folderPath. keepID, when set, is the identity chats must use (post-migrate).
func AssessPathHealth(inv *discover.Inventory, folderPath, keepID string) *HealthReport {
	folderPath = filepath.Clean(folderPath)
	r := &HealthReport{
		Path:               folderPath,
		KeepID:             keepID,
		NamedByWorkspaceID: map[string]int{},
	}
	if inv == nil {
		r.Issues = append(r.Issues, "no inventory")
		r.RepairHint = repairHint(folderPath, keepID)
		return r
	}

	live := liveWorkspaceIDs(inv, folderPath)
	r.LiveWorkspaceIDs = live

	for _, e := range inv.Headers.Entries {
		if e.WorkspacePath == "" || filepath.Clean(e.WorkspacePath) != folderPath {
			continue
		}
		if !headerLooksNamed(e.Name) {
			continue
		}
		r.NamedTotal++
		wid := e.WorkspaceID
		if wid == "" {
			wid = "(missing-id)"
		}
		r.NamedByWorkspaceID[wid]++
		if keepID != "" && wid != keepID {
			r.OffTargetNamed++
		}
	}

	if keepID == "" && len(live) > 0 {
		r.KeepID = pickKeepID(inv, folderPath, live)
		keepID = r.KeepID
		// Recompute off-target once keep is known.
		r.OffTargetNamed = 0
		for wid, n := range r.NamedByWorkspaceID {
			if wid != keepID {
				r.OffTargetNamed += n
			}
		}
	}

	if len(live) > 1 {
		r.SplitBrain = true
		r.Issues = append(r.Issues, fmt.Sprintf(
			"%d live workspaceStorage ids still point at this folder: %s",
			len(live), strings.Join(live, ", "),
		))
	}
	if keepID != "" && len(live) == 1 && live[0] != keepID {
		r.SplitBrain = true
		r.Issues = append(r.Issues, fmt.Sprintf(
			"live workspace id is %s but chats should use keep id %s",
			live[0], keepID,
		))
	}
	if keepID != "" && r.OffTargetNamed > 0 {
		r.SplitBrain = true
		r.Issues = append(r.Issues, fmt.Sprintf(
			"%d named chat(s) still keyed to a sibling workspace id (not %s)",
			r.OffTargetNamed, keepID,
		))
	}
	if keepID == "" && len(r.NamedByWorkspaceID) > 1 {
		r.SplitBrain = true
		ids := make([]string, 0, len(r.NamedByWorkspaceID))
		for wid := range r.NamedByWorkspaceID {
			ids = append(ids, wid)
		}
		sort.Strings(ids)
		r.Issues = append(r.Issues, fmt.Sprintf(
			"named chats are split across workspace ids: %s",
			strings.Join(ids, ", "),
		))
	}

	r.OK = !r.SplitBrain && len(r.Issues) == 0
	if !r.OK {
		r.RepairHint = repairHint(folderPath, keepID)
	}
	return r
}

// RescanPathHealth reloads workspaceStorage + global headers from disk, then assesses.
func RescanPathHealth(roots paths.Roots, folderPath, keepID string) (*HealthReport, error) {
	inv, err := discover.Scan(roots)
	if err != nil {
		return nil, err
	}
	return AssessPathHealth(inv, folderPath, keepID), nil
}

func liveWorkspaceIDs(inv *discover.Inventory, folderPath string) []string {
	folderPath = filepath.Clean(folderPath)
	var live []string
	for _, w := range inv.Workspaces {
		if w.FolderPath == "" {
			continue
		}
		fp := filepath.Clean(w.FolderPath)
		if isOrphanFolderPath(fp) {
			continue
		}
		if fp == folderPath {
			live = append(live, w.ID)
		}
	}
	sort.Strings(live)
	return live
}

func isOrphanFolderPath(fp string) bool {
	return strings.Contains(fp, ".__rebind_orphan_")
}

func headerLooksNamed(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "new agent", "new chat", "restored chat", "untitled", "composer":
		return false
	default:
		return true
	}
}

func namedHeaderCount(inv *discover.Inventory, wsid string) int {
	if inv == nil || wsid == "" {
		return 0
	}
	n := 0
	for _, e := range inv.Headers.Entries {
		if e.WorkspaceID == wsid && headerLooksNamed(e.Name) {
			n++
		}
	}
	return n
}

func pickKeepID(inv *discover.Inventory, folderPath string, live []string) string {
	if len(live) == 0 {
		return ""
	}
	if len(live) == 1 {
		return live[0]
	}
	return pickTargetWorkspaceID(inv, folderPath)
}

func repairHint(folderPath, keepID string) string {
	if keepID != "" {
		return fmt.Sprintf(
			"Quit Cursor, then: cursor-rebind repair --to %s --target-id %s --yes",
			folderPath, keepID,
		)
	}
	return fmt.Sprintf(
		"Quit Cursor, then: cursor-rebind repair --to %s --yes",
		folderPath,
	)
}

// enforceExactHealth rescans disk after an exact migrate/repair and fails if
// dual live workspace ids or off-target named chats remain.
func enforceExactHealth(roots paths.Roots, folderPath, keepID, backupID string) error {
	if folderPath == "" {
		return nil
	}
	// Ensure target workspace.json still exists before assessing.
	if keepID != "" {
		wj := filepath.Join(roots.WorkspaceStorage, keepID, "workspace.json")
		if _, err := os.Stat(wj); err != nil {
			return &UnhealthyError{
				BackupID: backupID,
				Report: &HealthReport{
					Path:   folderPath,
					KeepID: keepID,
					Issues: []string{fmt.Sprintf("keep workspaceStorage missing: %s", keepID)},
					RepairHint: repairHint(folderPath, keepID),
				},
			}
		}
	}
	report, err := RescanPathHealth(roots, folderPath, keepID)
	if err != nil {
		return fmt.Errorf("post-rebind health rescan: %w", err)
	}
	if report.OK {
		return nil
	}
	return &UnhealthyError{BackupID: backupID, Report: report}
}

// FormatHealthHuman renders a short verify/doctor health block.
func FormatHealthHuman(r *HealthReport) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	status := "healthy"
	if !r.OK {
		status = "SPLIT-BRAIN"
	}
	fmt.Fprintf(&b, "Workspace health:  %s\n", status)
	if len(r.LiveWorkspaceIDs) > 0 {
		fmt.Fprintf(&b, "Live workspace ids: %s\n", strings.Join(r.LiveWorkspaceIDs, ", "))
	} else {
		fmt.Fprintf(&b, "Live workspace ids: (none)\n")
	}
	if r.KeepID != "" {
		fmt.Fprintf(&b, "Recommended keep:  %s\n", r.KeepID)
	}
	fmt.Fprintf(&b, "Named chats:       %d", r.NamedTotal)
	if r.OffTargetNamed > 0 {
		fmt.Fprintf(&b, " (%d off keep id)", r.OffTargetNamed)
	}
	b.WriteByte('\n')
	if len(r.NamedByWorkspaceID) > 0 {
		ids := make([]string, 0, len(r.NamedByWorkspaceID))
		for wid := range r.NamedByWorkspaceID {
			ids = append(ids, wid)
		}
		sort.Strings(ids)
		for _, wid := range ids {
			fmt.Fprintf(&b, "  - %s: %d named\n", wid, r.NamedByWorkspaceID[wid])
		}
	}
	for _, issue := range r.Issues {
		fmt.Fprintf(&b, "  • %s\n", issue)
	}
	if r.RepairHint != "" && !r.OK {
		fmt.Fprintf(&b, "  → %s\n", r.RepairHint)
	}
	return b.String()
}
