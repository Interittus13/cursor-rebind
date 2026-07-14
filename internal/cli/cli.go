package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/Interittus13/cursor-rebind/internal/discover"
	"github.com/Interittus13/cursor-rebind/internal/doctor"
	"github.com/Interittus13/cursor-rebind/internal/paths"
)

const version = "1.0.0-dev"

// Execute parses args and runs a subcommand.
func Execute() error {
	args := os.Args[1:]
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "version", "--version", "-V":
		fmt.Printf("cursor-rebind %s\n", version)
		return nil
	case "help", "--help", "-h":
		printUsage()
		return nil
	case "scan":
		return runScan(args[1:])
	case "doctor":
		return runDoctor(args[1:])
	default:
		return fmt.Errorf("unknown command %q (try: cursor-rebind help)", args[0])
	}
}

func printUsage() {
	fmt.Fprintf(os.Stdout, `cursor-rebind %s — keep Cursor chats after path or machine changes

Usage:
  cursor-rebind scan [--json]
  cursor-rebind doctor [path] [--json]
  cursor-rebind version

Commands:
  scan     Inventory workspaces and chat identity
  doctor   Diagnose missing chats for a project path
`, version)
}

func runScan(args []string) error {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		case "-h", "--help":
			fmt.Println("Usage: cursor-rebind scan [--json]")
			return nil
		default:
			return fmt.Errorf("scan: unknown flag %q", a)
		}
	}

	roots, err := paths.Discover()
	if err != nil {
		return err
	}
	inv, err := discover.Scan(roots)
	if err != nil {
		return err
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		// Avoid dumping every header entry in default JSON (can be large).
		type scanOut struct {
			Roots      paths.Roots            `json:"roots"`
			Workspaces []discover.Workspace   `json:"workspaces"`
			Projects   []discover.AgentProject `json:"projects"`
			Headers    discover.HeaderIndex   `json:"headers"`
			ScannedAt  interface{}            `json:"scannedAt"`
		}
		return enc.Encode(scanOut{
			Roots:      inv.Roots,
			Workspaces: inv.Workspaces,
			Projects:   inv.Projects,
			Headers:    inv.Headers,
			ScannedAt:  inv.ScannedAt,
		})
	}

	fmt.Printf("cursor-rebind scan\n")
	fmt.Printf("==================\n")
	fmt.Printf("User data:   %s\n", roots.UserDataDir)
	fmt.Printf("Global DB:   %s\n", roots.GlobalDB)
	fmt.Printf("Projects:    %s\n", roots.ProjectsDir)
	fmt.Printf("Workspaces:  %d\n", len(inv.Workspaces))
	fmt.Printf("Agent dirs:  %d\n", len(inv.Projects))
	if inv.Headers.Loaded {
		fmt.Printf("Headers:     %d chats", inv.Headers.Total)
		if inv.Headers.MissingPath > 0 {
			fmt.Printf(" (%d without path)", inv.Headers.MissingPath)
		}
		fmt.Println()
		if len(inv.Headers.ByPathPrefix) > 0 {
			fmt.Printf("Path buckets:\n")
			for k, n := range inv.Headers.ByPathPrefix {
				fmt.Printf("  %-40s %d\n", k, n)
			}
		}
	} else if inv.Headers.Error != "" {
		fmt.Printf("Headers:     error: %s\n", inv.Headers.Error)
	}
	fmt.Println()

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "EXISTS\tHEADERS\tSCHEMA\tPATH")
	for _, ws := range inv.Workspaces {
		ex := "no"
		if ws.PathExists {
			ex = "yes"
		}
		path := ws.FolderPath
		if path == "" {
			path = ws.FolderURI
		}
		if path == "" {
			path = "(no folder)"
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", ex, ws.HeaderChats, ws.Schema, path)
	}
	_ = w.Flush()

	fmt.Println()
	fmt.Println("Agent projects:")
	w = tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TRANSCRIPTS\tNAME")
	for _, p := range inv.Projects {
		fmt.Fprintf(w, "%d\t%s\n", p.TranscriptCount, p.Name)
	}
	_ = w.Flush()
	return nil
}

func runDoctor(args []string) error {
	asJSON := false
	var pathArg string
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		case "-h", "--help":
			fmt.Println("Usage: cursor-rebind doctor [path] [--json]")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return fmt.Errorf("doctor: unknown flag %q", a)
			}
			if pathArg != "" {
				return fmt.Errorf("doctor: unexpected extra argument %q", a)
			}
			pathArg = a
		}
	}
	if pathArg == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		pathArg = wd
	}

	roots, err := paths.Discover()
	if err != nil {
		return err
	}
	inv, err := discover.Scan(roots)
	if err != nil {
		return err
	}

	// Resolve relative paths for clearer reports.
	if !filepath.IsAbs(pathArg) {
		abs, err := filepath.Abs(pathArg)
		if err == nil {
			pathArg = abs
		}
	}

	rep, err := doctor.Analyze(inv, pathArg)
	if err != nil {
		return err
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	fmt.Print(doctor.FormatHuman(rep))
	return nil
}
