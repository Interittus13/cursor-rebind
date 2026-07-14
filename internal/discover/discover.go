package discover

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Interittus13/cursor-rebind/internal/paths"
	"github.com/Interittus13/cursor-rebind/internal/vscdb"
)

// Inventory is a full machine scan of Cursor chat-related storage.
type Inventory struct {
	Roots      paths.Roots     `json:"roots"`
	Workspaces []Workspace     `json:"workspaces"`
	Projects   []AgentProject  `json:"projects"`
	Headers    HeaderIndex     `json:"headers"`
	ScannedAt  time.Time       `json:"scannedAt"`
}

// Workspace is one workspaceStorage/<hash> entry.
type Workspace struct {
	ID           string              `json:"id"`
	FolderURI    string              `json:"folderUri"`
	FolderPath   string              `json:"folderPath"`
	PathExists   bool                `json:"pathExists"`
	DBPath       string              `json:"dbPath"`
	DBSize       int64               `json:"dbSize"`
	Schema       vscdb.SchemaVersion `json:"schema"`
	LocalChats   int                 `json:"localChats"`   // allComposers in workspace DB (pre-3.0)
	SelectedTabs int                 `json:"selectedTabs"` // selectedComposerIds count
	HeaderChats  int                 `json:"headerChats"`  // chats in global headers for this id/path
	ModTime      time.Time           `json:"modTime"`
}

// AgentProject is a ~/.cursor/projects/<sanitized> directory.
type AgentProject struct {
	Name            string    `json:"name"`
	Dir             string    `json:"dir"`
	TranscriptCount int       `json:"transcriptCount"`
	ModTime         time.Time `json:"modTime"`
	InferredPath    string    `json:"inferredPath,omitempty"`
}

// HeaderIndex summarizes composer.composerHeaders.
type HeaderIndex struct {
	Total          int            `json:"total"`
	ByWorkspaceID  map[string]int `json:"byWorkspaceId"`
	ByPathPrefix   map[string]int `json:"byPathPrefix"` // rough host/user buckets
	MissingPath    int            `json:"missingPath"`
	Loaded         bool           `json:"loaded"`
	Error          string         `json:"error,omitempty"`
	Entries        []HeaderEntry  `json:"-"` // omitted from default JSON dump unless verbose
}

// HeaderEntry is one global chat header.
type HeaderEntry struct {
	ComposerID    string
	Name          string
	WorkspaceID   string
	WorkspacePath string
	LastUpdatedAt int64
}

// Scan builds a full inventory.
func Scan(roots paths.Roots) (*Inventory, error) {
	inv := &Inventory{
		Roots:     roots,
		ScannedAt: time.Now(),
		Headers: HeaderIndex{
			ByWorkspaceID: map[string]int{},
			ByPathPrefix:  map[string]int{},
		},
	}

	ws, err := scanWorkspaces(roots.WorkspaceStorage)
	if err != nil {
		return nil, err
	}
	inv.Workspaces = ws

	projects, err := scanProjects(roots.ProjectsDir)
	if err != nil {
		return nil, err
	}
	inv.Projects = projects

	headers, herr := loadHeaders(roots.GlobalDB)
	if herr != nil {
		inv.Headers.Error = herr.Error()
	} else {
		inv.Headers = headers
	}

	// Attach header chat counts to workspaces (prefer the larger of id vs path match).
	pathToCount := map[string]int{}
	idToCount := map[string]int{}
	for _, e := range inv.Headers.Entries {
		if e.WorkspaceID != "" {
			idToCount[e.WorkspaceID]++
		}
		if e.WorkspacePath != "" {
			pathToCount[filepath.Clean(e.WorkspacePath)]++
		}
	}
	for i := range inv.Workspaces {
		w := &inv.Workspaces[i]
		byID := idToCount[w.ID]
		byPath := 0
		if w.FolderPath != "" {
			byPath = pathToCount[filepath.Clean(w.FolderPath)]
		}
		if byPath > byID {
			w.HeaderChats = byPath
		} else {
			w.HeaderChats = byID
		}
	}

	return inv, nil
}

func scanWorkspaces(dir string) ([]Workspace, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read workspaceStorage: %w", err)
	}

	var out []Workspace
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		wsDir := filepath.Join(dir, id)
		w := Workspace{
			ID:     id,
			DBPath: filepath.Join(wsDir, "state.vscdb"),
		}

		metaPath := filepath.Join(wsDir, "workspace.json")
		if raw, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				Folder    string `json:"folder"`
				Workspace string `json:"workspace"`
			}
			if json.Unmarshal(raw, &meta) == nil {
				w.FolderURI = meta.Folder
				if w.FolderURI == "" {
					w.FolderURI = meta.Workspace
				}
				w.FolderPath = paths.PathFromFileURI(w.FolderURI)
				if w.FolderPath != "" {
					if st, err := os.Stat(w.FolderPath); err == nil && st.IsDir() {
						w.PathExists = true
					}
				}
			}
		}

		if st, err := os.Stat(w.DBPath); err == nil {
			w.DBSize = st.Size()
			w.ModTime = st.ModTime()
		} else if st, err := os.Stat(wsDir); err == nil {
			w.ModTime = st.ModTime()
		}

		if data, ok := readWorkspaceComposer(w.DBPath); ok {
			w.Schema = vscdb.DetectWorkspaceSchema(data)
			w.LocalChats = len(data.AllComposers)
			w.SelectedTabs = len(data.SelectedComposerIDs)
		} else {
			w.Schema = vscdb.SchemaUnknown
		}

		out = append(out, w)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].HeaderChats != out[j].HeaderChats {
			return out[i].HeaderChats > out[j].HeaderChats
		}
		return out[i].ModTime.After(out[j].ModTime)
	})
	return out, nil
}

func readWorkspaceComposer(dbPath string) (*vscdb.ComposerData, bool) {
	db, err := vscdb.OpenReadOnly(dbPath)
	if err != nil {
		return nil, false
	}
	defer db.Close()

	var data vscdb.ComposerData
	ok, err := vscdb.GetItemJSON(db, "composer.composerData", &data)
	if err != nil || !ok {
		return nil, false
	}
	return &data, true
}

func scanProjects(dir string) ([]AgentProject, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read projects dir: %w", err)
	}

	var out []AgentProject
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		pdir := filepath.Join(dir, name)
		p := AgentProject{
			Name:         name,
			Dir:          pdir,
			InferredPath: inferPathFromProjectName(name),
		}
		if st, err := e.Info(); err == nil {
			p.ModTime = st.ModTime()
		}
		transcripts := filepath.Join(pdir, "agent-transcripts")
		if tEntries, err := os.ReadDir(transcripts); err == nil {
			for _, t := range tEntries {
				if !t.IsDir() && (strings.HasSuffix(t.Name(), ".txt") || strings.HasSuffix(t.Name(), ".jsonl") || strings.HasSuffix(t.Name(), ".json")) {
					p.TranscriptCount++
				} else if t.IsDir() {
					// Some Cursor builds nest transcript dirs by composer id.
					p.TranscriptCount++
				}
			}
		}
		out = append(out, p)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].TranscriptCount != out[j].TranscriptCount {
			return out[i].TranscriptCount > out[j].TranscriptCount
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func inferPathFromProjectName(name string) string {
	// Skip ephemeral / numeric session folders.
	if name == "empty-window" || strings.HasPrefix(name, "tmp-") {
		return ""
	}
	if len(name) > 0 && name[0] >= '0' && name[0] <= '9' && !strings.Contains(name, "-") {
		return ""
	}
	// Reverse sanitization is lossy (can't recover which '-' were separators),
	// but home-user-... is usually restorable by joining after the first two segments for Linux.
	parts := strings.Split(name, "-")
	if len(parts) < 2 {
		return ""
	}
	// Common pattern: home-<user>-Documents-...
	if parts[0] == "home" && len(parts) >= 3 {
		return "/" + strings.Join(parts, "/")
	}
	if parts[0] == "Users" && len(parts) >= 3 {
		return "/" + strings.Join(parts, "/")
	}
	return "/" + strings.Join(parts, "/")
}

func loadHeaders(globalDB string) (HeaderIndex, error) {
	idx := HeaderIndex{
		ByWorkspaceID: map[string]int{},
		ByPathPrefix:  map[string]int{},
	}
	db, err := vscdb.OpenReadOnly(globalDB)
	if err != nil {
		return idx, err
	}
	defer db.Close()

	var headers vscdb.ComposerHeaders
	ok, err := vscdb.GetItemJSON(db, "composer.composerHeaders", &headers)
	if err != nil {
		return idx, err
	}
	if !ok {
		idx.Loaded = true
		return idx, nil
	}

	idx.Loaded = true
	idx.Total = len(headers.AllComposers)
	for _, c := range headers.AllComposers {
		e := HeaderEntry{
			ComposerID:    c.ComposerID,
			Name:          c.Name,
			LastUpdatedAt: c.LastUpdatedAt,
		}
		if c.WorkspaceIdentifier != nil {
			e.WorkspaceID = c.WorkspaceIdentifier.ID
			if c.WorkspaceIdentifier.URI != nil {
				e.WorkspacePath = c.WorkspaceIdentifier.URI.FsPath
				if e.WorkspacePath == "" {
					e.WorkspacePath = c.WorkspaceIdentifier.URI.Path
				}
			}
		}
		if e.WorkspaceID != "" {
			idx.ByWorkspaceID[e.WorkspaceID]++
		}
		if e.WorkspacePath == "" {
			idx.MissingPath++
		} else {
			bucket := pathBucket(e.WorkspacePath)
			idx.ByPathPrefix[bucket]++
		}
		idx.Entries = append(idx.Entries, e)
	}
	return idx, nil
}

func pathBucket(p string) string {
	cleaned := filepath.Clean(p)
	parts := strings.Split(cleaned, string(filepath.Separator))
	// /home/<user>/... or /Users/<user>/...
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "home" || parts[i] == "Users" {
			return strings.Join(parts[i:i+2], "/")
		}
	}
	if len(parts) >= 3 {
		return strings.Join(parts[:3], "/")
	}
	return cleaned
}

// FindWorkspacesByPath returns workspaces whose folder path matches (exact or suffix).
func (inv *Inventory) FindWorkspacesByPath(query string) []Workspace {
	q := filepath.Clean(query)
	base := filepath.Base(q)
	var exact, fuzzy []Workspace
	for _, w := range inv.Workspaces {
		if w.FolderPath == "" {
			continue
		}
		fp := filepath.Clean(w.FolderPath)
		if fp == q {
			exact = append(exact, w)
			continue
		}
		if strings.EqualFold(filepath.Base(fp), base) {
			fuzzy = append(fuzzy, w)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return fuzzy
}

// FindProjectsMatching returns agent project dirs that look related to a path.
func (inv *Inventory) FindProjectsMatching(query string) []AgentProject {
	sanitized := paths.SanitizeProjectPath(query)
	base := strings.ReplaceAll(filepath.Base(filepath.Clean(query)), "_", "")
	suffix := "-" + base
	var out []AgentProject
	for _, p := range inv.Projects {
		if sanitized != "" && p.Name == sanitized {
			out = append(out, p)
			continue
		}
		// Match old-machine spellings of the same project, not sibling folders
		// (e.g. Stambha-plugins must not match Stambha).
		if base != "" && strings.HasSuffix(p.Name, suffix) {
			out = append(out, p)
		}
	}
	return out
}

// HeadersForPath returns global header entries for a folder path (exact match).
func (inv *Inventory) HeadersForPath(folderPath string) []HeaderEntry {
	want := filepath.Clean(folderPath)
	var out []HeaderEntry
	for _, e := range inv.Headers.Entries {
		if e.WorkspacePath != "" && filepath.Clean(e.WorkspacePath) == want {
			out = append(out, e)
		}
	}
	return out
}

// HeadersForPathLoose matches by project basename across user/path prefixes.
func (inv *Inventory) HeadersForPathLoose(folderPath string) []HeaderEntry {
	base := filepath.Base(filepath.Clean(folderPath))
	if base == "" || base == "." || base == "/" {
		return nil
	}
	var out []HeaderEntry
	for _, e := range inv.Headers.Entries {
		if e.WorkspacePath == "" {
			continue
		}
		if filepath.Base(filepath.Clean(e.WorkspacePath)) == base {
			out = append(out, e)
		}
	}
	return out
}
