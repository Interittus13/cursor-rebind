package discover

import (
	"path/filepath"
	"strings"
)

// FilterByPath keeps workspaces/projects related to absPath (exact folder match,
// orphan siblings for that folder, and agent dirs whose inferred path matches).
func FilterByPath(workspaces []Workspace, projects []AgentProject, absPath string) ([]Workspace, []AgentProject) {
	want := filepath.Clean(absPath)
	if want == "" || want == "." {
		return workspaces, projects
	}
	var wsOut []Workspace
	for _, w := range workspaces {
		fp := filepath.Clean(w.FolderPath)
		if fp == want || strings.HasPrefix(fp, want+".__rebind_orphan_") {
			wsOut = append(wsOut, w)
		}
	}
	var projOut []AgentProject
	for _, p := range projects {
		inf := filepath.Clean(p.InferredPath)
		if inf == want || strings.HasPrefix(inf, want+string(filepath.Separator)) {
			projOut = append(projOut, p)
			continue
		}
		if projectNameLikelyPath(p.Name, want) {
			projOut = append(projOut, p)
		}
	}
	return wsOut, projOut
}

func projectNameLikelyPath(name, absPath string) bool {
	if name == "" || absPath == "" {
		return false
	}
	parts := strings.Split(strings.Trim(filepath.ToSlash(absPath), "/"), "/")
	if len(parts) < 2 {
		return false
	}
	slug := strings.Join(parts, "-")
	if name == slug {
		return true
	}
	last := parts[len(parts)-1]
	return strings.HasSuffix(name, "-"+last) && strings.Contains(name, parts[0]+"-")
}
