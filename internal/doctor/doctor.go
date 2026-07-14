package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Interittus13/cursor-rebind/internal/discover"
	"github.com/Interittus13/cursor-rebind/internal/paths"
)

// Report explains IDE vs Agent visibility for a project path.
type Report struct {
	QueryPath       string                  `json:"queryPath"`
	AbsPath         string                  `json:"absPath"`
	PathExists      bool                    `json:"pathExists"`
	SanitizedName   string                  `json:"sanitizedProjectName"`
	Workspaces      []discover.Workspace    `json:"workspaces"`
	AgentProjects   []discover.AgentProject `json:"agentProjects"`
	ExactHeaders    []discover.HeaderEntry  `json:"exactHeaders"`
	LooseHeaders    []discover.HeaderEntry  `json:"looseHeaders"`
	OrphanOldPaths  map[string]int          `json:"orphanOldPaths"`
	IDEVisibleEstimate int                  `json:"ideVisibleEstimate"`
	AgentVisibleEstimate int                `json:"agentVisibleEstimate"`
	Diagnosis       []string                `json:"diagnosis"`
	NextSteps       []string                `json:"nextSteps"`
}

// Analyze builds a doctor report for a project path.
func Analyze(inv *discover.Inventory, query string) (*Report, error) {
	abs := query
	if !filepath.IsAbs(query) {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		abs = filepath.Join(wd, query)
	}
	abs = filepath.Clean(abs)

	r := &Report{
		QueryPath:      query,
		AbsPath:        abs,
		SanitizedName:  paths.SanitizeProjectPath(abs),
		OrphanOldPaths: map[string]int{},
	}
	if st, err := os.Stat(abs); err == nil && st.IsDir() {
		r.PathExists = true
	}

	r.Workspaces = inv.FindWorkspacesByPath(abs)
	r.AgentProjects = inv.FindProjectsMatching(abs)
	r.ExactHeaders = inv.HeadersForPath(abs)
	r.LooseHeaders = inv.HeadersForPathLoose(abs)

	// Count agent transcripts.
	for _, p := range r.AgentProjects {
		r.AgentVisibleEstimate += p.TranscriptCount
	}

	// IDE sidebar in Cursor 3.0 filters global headers by workspaceIdentifier matching open workspace.
	currentIDs := map[string]bool{}
	for _, w := range r.Workspaces {
		if w.FolderPath == abs || filepath.Clean(w.FolderPath) == abs {
			currentIDs[w.ID] = true
			r.IDEVisibleEstimate += w.HeaderChats
		}
	}
	// If no workspace opened yet at this path, estimate from exact path headers.
	if r.IDEVisibleEstimate == 0 {
		r.IDEVisibleEstimate = len(r.ExactHeaders)
	}

	base := filepath.Base(abs)
	for _, e := range r.LooseHeaders {
		if e.WorkspacePath == "" {
			continue
		}
		if filepath.Clean(e.WorkspacePath) == abs {
			continue
		}
		if filepath.Base(e.WorkspacePath) != base {
			continue
		}
		r.OrphanOldPaths[e.WorkspacePath]++
	}

	r.Diagnosis, r.NextSteps = diagnose(r)
	return r, nil
}

func diagnose(r *Report) (diag, next []string) {
	if !r.PathExists {
		diag = append(diag, "Project path does not exist on disk.")
		next = append(next, "Create or clone the project, open it once in Cursor, then re-run doctor.")
		return
	}

	hasCurrentWS := false
	for _, w := range r.Workspaces {
		if filepath.Clean(w.FolderPath) == r.AbsPath && w.PathExists {
			hasCurrentWS = true
			break
		}
	}

	orphanHeaderCount := 0
	for _, n := range r.OrphanOldPaths {
		orphanHeaderCount += n
	}

	oldAgent := 0
	newAgent := 0
	for _, p := range r.AgentProjects {
		if p.Name == r.SanitizedName {
			newAgent += p.TranscriptCount
		} else {
			oldAgent += p.TranscriptCount
		}
	}

	switch {
	case orphanHeaderCount > 0 && len(r.ExactHeaders) == 0:
		diag = append(diag, fmt.Sprintf(
			"IDE chats look missing: %d global chat header(s) still point at old path(s) for project %q.",
			orphanHeaderCount, filepath.Base(r.AbsPath),
		))
		for old, n := range r.OrphanOldPaths {
			diag = append(diag, fmt.Sprintf("  - %d chat(s) keyed to %s", n, old))
		}
	case orphanHeaderCount > 0 && len(r.ExactHeaders) > 0:
		diag = append(diag, fmt.Sprintf(
			"Split identity: %d header(s) on current path, %d still on old path(s).",
			len(r.ExactHeaders), orphanHeaderCount,
		))
	case len(r.ExactHeaders) > 0:
		diag = append(diag, fmt.Sprintf("Global index has %d chat(s) for this exact path.", len(r.ExactHeaders)))
	default:
		diag = append(diag, "No global composer headers found for this project path.")
	}

	if oldAgent > 0 && newAgent == 0 {
		diag = append(diag, fmt.Sprintf(
			"Agent view may still show history via old ~/.cursor/projects dir (%d transcript(s)); current sanitized name %q has none.",
			oldAgent, r.SanitizedName,
		))
	} else if oldAgent > 0 && newAgent > 0 {
		diag = append(diag, fmt.Sprintf(
			"Agent projects exist for both old and new path spellings (%d old / %d new transcripts).",
			oldAgent, newAgent,
		))
	} else if newAgent > 0 {
		diag = append(diag, fmt.Sprintf("Agent project dir %q has %d transcript(s).", r.SanitizedName, newAgent))
	}

	if !hasCurrentWS {
		diag = append(diag, "No workspaceStorage entry mapped to this exact path yet (open the folder in Cursor once).")
	}

	if orphanHeaderCount > 0 || (oldAgent > 0 && len(r.ExactHeaders) == 0) {
		next = append(next,
			"Your chats are still on disk — they are linked to the old project path.",
			"Close Cursor completely before rebinding.",
			fmt.Sprintf("Rebind from the old path to %s.", r.AbsPath),
		)
		if len(r.OrphanOldPaths) == 1 {
			for old := range r.OrphanOldPaths {
				next = append(next, fmt.Sprintf("Suggested map: %s  →  %s", old, r.AbsPath))
			}
		}
	} else if len(r.ExactHeaders) > 0 {
		next = append(next, "Identity looks healthy. If the sidebar is still empty, restart Cursor and check again.")
	} else {
		next = append(next, "No orphaned chats found for this project. Try cursor-rebind scan for a full inventory.")
	}

	return
}

// FormatHuman renders a plain-text doctor report.
func FormatHuman(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cursor-rebind doctor\n")
	fmt.Fprintf(&b, "====================\n")
	fmt.Fprintf(&b, "Path:      %s\n", r.AbsPath)
	fmt.Fprintf(&b, "Exists:    %v\n", r.PathExists)
	fmt.Fprintf(&b, "Projects:  %s\n\n", r.SanitizedName)

	fmt.Fprintf(&b, "IDE-visible estimate (headers for current path): %d\n", r.IDEVisibleEstimate)
	fmt.Fprintf(&b, "Agent-visible estimate (transcripts matched):   %d\n\n", r.AgentVisibleEstimate)

	if len(r.Workspaces) == 0 {
		fmt.Fprintf(&b, "Workspaces: (none matched)\n")
	} else {
		fmt.Fprintf(&b, "Workspaces (%d):\n", len(r.Workspaces))
		for _, w := range r.Workspaces {
			fmt.Fprintf(&b, "  - id=%s schema=%s headers=%d local=%s tabs=%d exists=%v\n",
				w.ID, w.Schema, w.HeaderChats, formatLocal(w), w.SelectedTabs, w.PathExists)
			fmt.Fprintf(&b, "    folder: %s\n", w.FolderPath)
		}
	}
	fmt.Fprintln(&b)

	if len(r.AgentProjects) == 0 {
		fmt.Fprintf(&b, "Agent projects: (none matched)\n")
	} else {
		fmt.Fprintf(&b, "Agent projects (%d):\n", len(r.AgentProjects))
		for _, p := range r.AgentProjects {
			fmt.Fprintf(&b, "  - %s  transcripts=%d\n", p.Name, p.TranscriptCount)
			if p.InferredPath != "" {
				fmt.Fprintf(&b, "    inferred: %s\n", p.InferredPath)
			}
		}
	}
	fmt.Fprintln(&b)

	if len(r.OrphanOldPaths) > 0 {
		fmt.Fprintf(&b, "Orphan header paths (same project name, different URI):\n")
		for old, n := range r.OrphanOldPaths {
			fmt.Fprintf(&b, "  - %d chat(s) @ %s\n", n, old)
		}
		fmt.Fprintln(&b)
	}

	fmt.Fprintf(&b, "Diagnosis:\n")
	for _, d := range r.Diagnosis {
		fmt.Fprintf(&b, "  • %s\n", d)
	}
	fmt.Fprintf(&b, "\nNext steps:\n")
	for _, d := range r.NextSteps {
		fmt.Fprintf(&b, "  → %s\n", d)
	}
	return b.String()
}

func formatLocal(w discover.Workspace) string {
	if w.LocalChats > 0 {
		return fmt.Sprintf("%d", w.LocalChats)
	}
	return "0(migrated)"
}
