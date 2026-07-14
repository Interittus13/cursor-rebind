package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Roots holds the standard Cursor data directories on this machine.
type Roots struct {
	UserDataDir     string // .../Cursor/User
	WorkspaceStorage string
	GlobalStorage   string
	GlobalDB        string // globalStorage/state.vscdb
	CursorHome      string // ~/.cursor
	ProjectsDir     string // ~/.cursor/projects
}

// Discover locates Cursor storage roots for the current OS/user.
func Discover() (Roots, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Roots{}, fmt.Errorf("home dir: %w", err)
	}

	userData, err := userDataDir(home)
	if err != nil {
		return Roots{}, err
	}

	r := Roots{
		UserDataDir:      userData,
		WorkspaceStorage: filepath.Join(userData, "workspaceStorage"),
		GlobalStorage:    filepath.Join(userData, "globalStorage"),
		GlobalDB:         filepath.Join(userData, "globalStorage", "state.vscdb"),
		CursorHome:       filepath.Join(home, ".cursor"),
		ProjectsDir:      filepath.Join(home, ".cursor", "projects"),
	}
	return r, nil
}

func userDataDir(home string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Cursor", "User"), nil
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", fmt.Errorf("APPDATA is not set")
		}
		return filepath.Join(appData, "Cursor", "User"), nil
	default: // linux and other unix
		xdg := os.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		return filepath.Join(xdg, "Cursor", "User"), nil
	}
}

// SanitizeProjectPath converts an absolute project path to the ~/.cursor/projects directory name.
// Example: /home/u/Documents/_Others/proj → home-u-Documents-Others-proj
//
// Cursor strips the leading slash, replaces path separators with '-', and drops '_'.
func SanitizeProjectPath(absPath string) string {
	cleaned := filepath.Clean(absPath)
	if cleaned == "/" || cleaned == "." {
		return ""
	}
	if cleaned[0] == filepath.Separator {
		cleaned = cleaned[1:]
	}
	var b []byte
	for i := 0; i < len(cleaned); i++ {
		c := cleaned[i]
		switch {
		case c == '/' || c == '\\':
			b = append(b, '-')
		case c == '_':
			// Cursor omits underscores from project dir names.
			continue
		default:
			b = append(b, c)
		}
	}
	return string(b)
}

// FileURI builds a file:// URI for an absolute path.
func FileURI(absPath string) string {
	cleaned := filepath.ToSlash(filepath.Clean(absPath))
	if cleaned == "" {
		return "file://"
	}
	if cleaned[0] != '/' {
		cleaned = "/" + cleaned
	}
	return "file://" + cleaned
}

// PathFromFileURI extracts an fs path from a file:// or vscode-remote URI folder value.
func PathFromFileURI(uri string) string {
	const prefix = "file://"
	if len(uri) >= len(prefix) && uri[:len(prefix)] == prefix {
		p := uri[len(prefix):]
		// Handle file:///C:/... on Windows later; Linux/macOS are fine.
		return filepath.FromSlash(p)
	}
	return uri
}
