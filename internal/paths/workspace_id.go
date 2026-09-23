package paths

import (
	"fmt"
	"path/filepath"
	"strings"
)

// LooksLikeWorkspaceID reports whether s is (or ends with) a Cursor
// workspaceStorage hash: 32 lowercase hex chars. Users sometimes paste that
// into --from/--to instead of a project folder path.
func LooksLikeWorkspaceID(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Raw id or basename after accidental abs-path expansion.
	base := filepath.Base(filepath.Clean(s))
	return isWorkspaceHash(base)
}

func isWorkspaceHash(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// ErrWorkspaceIDAsPath is returned when --from/--to looks like a workspace id.
func ErrWorkspaceIDAsPath(flag, value string) error {
	id := filepath.Base(filepath.Clean(strings.TrimSpace(value)))
	return fmt.Errorf(
		"%s %q looks like a workspace id, not a folder path\n"+
			"  Use the project directory for --from/--to, and pass the id with --target-id:\n"+
			"  cursor-rebind repair --to /path/to/project --target-id %s --yes",
		flag, value, id,
	)
}
