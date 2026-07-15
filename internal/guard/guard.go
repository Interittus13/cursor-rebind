package guard

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ErrCursorRunning is returned when a write would be unsafe.
var ErrCursorRunning = fmt.Errorf("Cursor appears to be running — quit Cursor completely and retry")

// EnsureCursorClosed returns ErrCursorRunning if Cursor processes are detected.
func EnsureCursorClosed() error {
	running, detail, err := IsCursorRunning()
	if err != nil {
		// Best-effort: if detection fails, warn but do not block.
		fmt.Fprintf(os.Stderr, "warning: could not check if Cursor is running: %v\n", err)
		return nil
	}
	if running {
		return fmt.Errorf("%w (%s)", ErrCursorRunning, detail)
	}
	return nil
}

// IsCursorRunning detects Cursor IDE processes on this OS.
func IsCursorRunning() (bool, string, error) {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("tasklist").CombinedOutput()
		if err != nil {
			return false, "", err
		}
		s := strings.ToLower(string(out))
		if strings.Contains(s, "cursor.exe") {
			return true, "cursor.exe", nil
		}
		return false, "", nil
	default:
		// Prefer cmdline match: Linux Electron often shows comm=cursor, but some
		// helpers only appear under /usr/share/cursor/cursor in args.
		if detail, ok := cursorRunningByArgs(); ok {
			return true, detail, nil
		}
		out, err := exec.Command("ps", "-A", "-o", "comm=").CombinedOutput()
		if err != nil {
			// Fallback
			out, err = exec.Command("ps", "-ax", "-o", "comm=").CombinedOutput()
			if err != nil {
				return false, "", err
			}
		}
		for _, line := range strings.Split(string(out), "\n") {
			name := strings.TrimSpace(line)
			base := name
			if i := strings.LastIndex(name, "/"); i >= 0 {
				base = name[i+1:]
			}
			lower := strings.ToLower(base)
			switch lower {
			case "cursor", "cursor helper", "cursor helper (gpu)", "cursor helper (renderer)", "cursor helper (plugin)":
				return true, base, nil
			}
			// Electron main often shows as "Cursor" with capital C already covered.
			if lower == "cursor.exe" {
				return true, base, nil
			}
		}
		return false, "", nil
	}
}

func cursorRunningByArgs() (string, bool) {
	out, err := exec.Command("ps", "-A", "-o", "args=").CombinedOutput()
	if err != nil {
		return "", false
	}
	self := os.Getpid()
	_ = self
	for _, line := range strings.Split(string(out), "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		lower := strings.ToLower(s)
		// Ignore our own migration tooling and sandboxed helper wrappers that
		// are not the IDE main process holding state.vscdb open.
		if strings.Contains(lower, "cursor-rebind") || strings.Contains(lower, "cursorsandbox") {
			continue
		}
		if strings.Contains(s, "/usr/share/cursor/cursor") ||
			strings.Contains(s, "/cursor/Cursor") ||
			strings.Contains(s, "Cursor.app/Contents/MacOS/Cursor") ||
			strings.HasPrefix(lower, "cursor ") ||
			lower == "cursor" {
			// Skip crashpad / chrome-sandbox only helpers.
			if strings.Contains(lower, "crashpad") || strings.Contains(lower, "chrome-sandbox") {
				continue
			}
			if len(s) > 80 {
				s = s[:80] + "…"
			}
			return s, true
		}
	}
	return "", false
}
