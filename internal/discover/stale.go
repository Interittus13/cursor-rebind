package discover

import (
	"path/filepath"
	"strings"
	"unicode"
)

// StaleKind explains why a scan row is considered noise for default display
// and/or a safe prune candidate.
type StaleKind string

const (
	StaleNone              StaleKind = ""
	StaleOrphanWorkspace   StaleKind = "orphan-workspace"
	StaleEmptyWindow       StaleKind = "empty-window"
	StaleEphemeralProject  StaleKind = "ephemeral-project"
	StaleEmptyAgentProject StaleKind = "empty-agent-project"
)

// IsOrphanFolderPath reports cursor-rebind's retired-workspace marker.
func IsOrphanFolderPath(fp string) bool {
	return strings.Contains(fp, ".__rebind_orphan_")
}

// IsEphemeralProjectName matches Cursor session / empty-window project slugs.
func IsEphemeralProjectName(name string) bool {
	if name == "" {
		return false
	}
	if name == "empty-window" || strings.HasPrefix(name, "tmp-") {
		return true
	}
	return isNumericSessionSlug(name)
}

func isNumericSessionSlug(name string) bool {
	if name == "" || strings.Contains(name, "-") {
		return false
	}
	for _, r := range name {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return len(name) >= 10 // Cursor uses long epoch-ish ids
}

// ClassifyWorkspace returns why a workspace row is noise, if any.
func ClassifyWorkspace(w Workspace) StaleKind {
	if w.ID == "empty-window" {
		return StaleEmptyWindow
	}
	if IsOrphanFolderPath(w.FolderPath) || IsOrphanFolderPath(w.FolderURI) {
		return StaleOrphanWorkspace
	}
	return StaleNone
}

// ClassifyProject returns why an agent project row is noise, if any.
// Ephemeral names are always stale. Non-ephemeral dirs with zero transcripts
// are hidden from default scan but only pruned when also stub-empty on disk
// (handled by prune, not classification alone).
func ClassifyProject(p AgentProject) StaleKind {
	if IsEphemeralProjectName(p.Name) {
		return StaleEphemeralProject
	}
	if p.TranscriptCount == 0 {
		return StaleEmptyAgentProject
	}
	return StaleNone
}

// FilterInventory returns a copy with stale rows removed when hideStale is true.
// Counts of hidden workspaces/projects are returned for the scan footer.
func FilterInventory(inv *Inventory, hideStale bool) (workspaces []Workspace, projects []AgentProject, hiddenWS, hiddenProj int) {
	if inv == nil {
		return nil, nil, 0, 0
	}
	if !hideStale {
		return append([]Workspace(nil), inv.Workspaces...), append([]AgentProject(nil), inv.Projects...), 0, 0
	}
	for _, w := range inv.Workspaces {
		if ClassifyWorkspace(w) != StaleNone {
			hiddenWS++
			continue
		}
		workspaces = append(workspaces, w)
	}
	for _, p := range inv.Projects {
		if ClassifyProject(p) != StaleNone {
			hiddenProj++
			continue
		}
		projects = append(projects, p)
	}
	return workspaces, projects, hiddenWS, hiddenProj
}

// PruneCandidate is one path the prune command may delete.
type PruneCandidate struct {
	Kind    StaleKind `json:"kind"`
	Label   string    `json:"label"`
	Path    string    `json:"path"`
	Detail  string    `json:"detail,omitempty"`
	Safe    bool      `json:"safe"` // true = default prune set
}

// CollectPruneCandidates lists safe default prune targets from an inventory:
//   - workspaceStorage dirs marked .__rebind_orphan_*
//   - ephemeral agent project dirs (tmp-*, empty-window, numeric) with 0 transcripts
//
// Empty non-ephemeral agent dirs and live missing-path workspaces are listed
// only when includeEmptyProjects is true (still never deletes headers/chats).
func CollectPruneCandidates(inv *Inventory, includeEmptyProjects bool) []PruneCandidate {
	if inv == nil {
		return nil
	}
	var out []PruneCandidate
	wsRoot := inv.Roots.WorkspaceStorage
	for _, w := range inv.Workspaces {
		kind := ClassifyWorkspace(w)
		switch kind {
		case StaleOrphanWorkspace:
			dir := filepath.Join(wsRoot, w.ID)
			out = append(out, PruneCandidate{
				Kind:   kind,
				Label:  w.ID,
				Path:   dir,
				Detail: w.FolderPath,
				Safe:   true,
			})
		case StaleEmptyWindow:
			// empty-window is a synthetic id; only remove if a real dir exists.
			dir := filepath.Join(wsRoot, w.ID)
			out = append(out, PruneCandidate{
				Kind:   kind,
				Label:  w.ID,
				Path:   dir,
				Detail: "empty-window workspaceStorage",
				Safe:   true,
			})
		}
	}
	for _, p := range inv.Projects {
		kind := ClassifyProject(p)
		switch kind {
		case StaleEphemeralProject:
			if p.TranscriptCount > 0 {
				// Keep sessions that still have transcript files.
				continue
			}
			out = append(out, PruneCandidate{
				Kind:   kind,
				Label:  p.Name,
				Path:   p.Dir,
				Detail: "0 transcripts",
				Safe:   true,
			})
		case StaleEmptyAgentProject:
			if !includeEmptyProjects {
				continue
			}
			out = append(out, PruneCandidate{
				Kind:   kind,
				Label:  p.Name,
				Path:   p.Dir,
				Detail: "0 transcripts (path-like slug)",
				Safe:   false,
			})
		}
	}
	return out
}
