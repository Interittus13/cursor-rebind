package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/Interittus13/cursor-rebind/internal/backup"
	"github.com/Interittus13/cursor-rebind/internal/discover"
	"github.com/Interittus13/cursor-rebind/internal/doctor"
	"github.com/Interittus13/cursor-rebind/internal/paths"
	"github.com/Interittus13/cursor-rebind/internal/rebind"
)

// Version is set at link time by GoReleaser / -ldflags.
// Local builds fall back to "dev".
var Version = "dev"

// Execute parses args and runs a subcommand.
func Execute() error {
	args := os.Args[1:]
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "version", "--version", "-V":
		fmt.Printf("cursor-rebind %s\n", Version)
		return nil
	case "help", "--help", "-h":
		printUsage()
		return nil
	case "scan":
		return runScan(args[1:])
	case "doctor":
		return runDoctor(args[1:])
	case "map", "preview":
		return runPlan(args[0], args[1:])
	case "migrate":
		return runMigrate(args[1:])
	case "repair":
		return runRepair(args[1:])
	case "verify":
		return runVerify(args[1:])
	case "restore":
		return runRestore(args[1:])
	default:
		return fmt.Errorf("unknown command %q (try: cursor-rebind help)", args[0])
	}
}

func printUsage() {
	fmt.Fprintf(os.Stdout, `cursor-rebind %s — keep Cursor chats after path or machine changes

Usage:
  cursor-rebind scan [--json]
  cursor-rebind doctor [path] [--json]
  cursor-rebind map --from <old> --to <new> [--prefix] [--json]
  cursor-rebind migrate --from <old> --to <new> [--prefix] [--target-id <id>] [--dry-run|--yes]
  cursor-rebind repair --to <path> [--from <old>] [--target-id <id>] --yes
  cursor-rebind verify [path]
  cursor-rebind restore <backup-id>
  cursor-rebind restore --list
  cursor-rebind version

Commands:
  scan      Inventory workspaces and chat identity
  doctor    Diagnose missing chats for a project path
  map       Build a rebind plan (alias: preview)
  migrate   Apply a rebind plan (quit Cursor first)
  repair    Fix open tabs + Agents Window after a partial migrate (quit Cursor first)
  verify    Count headers/transcripts for a path
  restore   Roll back a migrate backup
`, Version)
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

type pathFlags struct {
	from     string
	to       string
	targetID string
	prefix   bool
	json     bool
	dryRun   bool
	yes      bool
	list     bool
	help     bool
}

func parsePathFlags(args []string) (pathFlags, []string, error) {
	var f pathFlags
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--from":
			if i+1 >= len(args) {
				return f, nil, fmt.Errorf("--from requires a path")
			}
			i++
			f.from = args[i]
		case "--to":
			if i+1 >= len(args) {
				return f, nil, fmt.Errorf("--to requires a path")
			}
			i++
			f.to = args[i]
		case "--target-id":
			if i+1 >= len(args) {
				return f, nil, fmt.Errorf("--target-id requires a workspace id")
			}
			i++
			f.targetID = args[i]
		case "--prefix":
			f.prefix = true
		case "--json":
			f.json = true
		case "--dry-run":
			f.dryRun = true
		case "--yes", "-y":
			f.yes = true
		case "--list":
			f.list = true
		case "-h", "--help":
			f.help = true
		default:
			if len(a) > 0 && a[0] == '-' {
				return f, nil, fmt.Errorf("unknown flag %q", a)
			}
			positional = append(positional, a)
		}
	}
	return f, positional, nil
}

func absPath(p string) string {
	if p == "" {
		return p
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return abs
}

func runPlan(cmd string, args []string) error {
	f, _, err := parsePathFlags(args)
	if err != nil {
		return err
	}
	if f.help {
		fmt.Printf("Usage: cursor-rebind %s --from <old> --to <new> [--prefix] [--json]\n", cmd)
		return nil
	}
	if f.from == "" || f.to == "" {
		return fmt.Errorf("%s requires --from and --to", cmd)
	}
	f.from, f.to = absPath(f.from), absPath(f.to)

	roots, err := paths.Discover()
	if err != nil {
		return err
	}
	inv, err := discover.Scan(roots)
	if err != nil {
		return err
	}

	mode := rebind.ModeExact
	if f.prefix {
		mode = rebind.ModePrefix
	}
	plan, err := rebind.BuildPlanWithTarget(inv, f.from, f.to, mode, f.targetID)
	if err != nil {
		return err
	}
	if f.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(plan)
	}
	fmt.Print(rebind.FormatPlan(plan))
	return nil
}

func runMigrate(args []string) error {
	f, _, err := parsePathFlags(args)
	if err != nil {
		return err
	}
	if f.help {
		fmt.Println("Usage: cursor-rebind migrate --from <old> --to <new> [--prefix] [--target-id <id>] [--dry-run|--yes]")
		return nil
	}
	if f.from == "" || f.to == "" {
		return fmt.Errorf("migrate requires --from and --to")
	}
	if f.dryRun && f.yes {
		return fmt.Errorf("use either --dry-run or --yes, not both")
	}
	f.from, f.to = absPath(f.from), absPath(f.to)

	roots, err := paths.Discover()
	if err != nil {
		return err
	}
	inv, err := discover.Scan(roots)
	if err != nil {
		return err
	}

	mode := rebind.ModeExact
	if f.prefix {
		mode = rebind.ModePrefix
	}
	plan, err := rebind.BuildPlanWithTarget(inv, f.from, f.to, mode, f.targetID)
	if err != nil {
		return err
	}
	fmt.Print(rebind.FormatPlan(plan))
	fmt.Println()

	res, err := rebind.Apply(inv, plan, f.yes, f.dryRun)
	if err != nil {
		return err
	}
	if f.dryRun {
		fmt.Println("Dry run only — no files were changed.")
		fmt.Println("Quit Cursor, then re-run with --yes to apply.")
		return nil
	}
	fmt.Printf("Done. Updated %d header(s)", res.HeadersUpdated)
	if res.HeadersAdded > 0 {
		fmt.Printf(", added %d", res.HeadersAdded)
	}
	fmt.Printf(".")
	if res.HeadersUpdated == 0 && res.HeadersAdded == 0 {
		fmt.Printf(" (headers already on --to)")
	}
	if res.ComposersRewritten > 0 {
		fmt.Printf(" Rewrote %d composerData blob(s).", res.ComposersRewritten)
	}
	if res.GlassProjectsUpdated > 0 || res.GlassKeysMoved > 0 {
		fmt.Printf(" Agents Window: %d project(s), %d glass key(s).", res.GlassProjectsUpdated, res.GlassKeysMoved)
	}
	if res.ProjectMoved {
		fmt.Printf(" Agent project dir remapped.")
	}
	fmt.Println()
	if res.BackupID != "" {
		fmt.Printf("Backup: %s (cursor-rebind restore %s)\n", res.BackupID, res.BackupID)
	}
	if res.TargetWSID != "" {
		fmt.Printf("Target workspace id: %s\n", res.TargetWSID)
	}
	fmt.Println("Fully quit Cursor (not just reload), then reopen this project path.")
	return nil
}

func runRepair(args []string) error {
	f, _, err := parsePathFlags(args)
	if err != nil {
		return err
	}
	if f.help {
		fmt.Println("Usage: cursor-rebind repair --to <path> [--from <old>] [--target-id <id>] --yes")
		return nil
	}
	if f.to == "" {
		return fmt.Errorf("repair requires --to")
	}
	if !f.yes {
		return fmt.Errorf("repair requires --yes (quit Cursor first)")
	}
	f.to = absPath(f.to)
	if f.from == "" {
		// Headers already on --to are enough; invent a non-equal from for BuildPlan.
		f.from = f.to + ".__rebind_from_unknown"
	} else {
		f.from = absPath(f.from)
	}

	roots, err := paths.Discover()
	if err != nil {
		return err
	}
	inv, err := discover.Scan(roots)
	if err != nil {
		return err
	}

	plan, err := rebind.BuildPlanWithTarget(inv, f.from, f.to, rebind.ModeExact, f.targetID)
	if err != nil {
		return err
	}
	fmt.Printf("cursor-rebind repair\n")
	fmt.Printf("====================\n")
	fmt.Printf("To:        %s\n", plan.To)
	fmt.Printf("Target ID: %s\n", plan.TargetWSID)
	if len(plan.SourceWSIDs) > 0 {
		fmt.Printf("Sources:   %s\n", strings.Join(plan.SourceWSIDs, ", "))
	}
	fmt.Println()

	res, err := rebind.RepairTabs(inv, plan, f.yes)
	if err != nil {
		return err
	}
	fmt.Println("Done. Primary IDE tab + Agents Window glass identity repaired.")
	if res != nil && res.PrimaryComposerID != "" {
		fmt.Printf("Primary composer: %s\n", res.PrimaryComposerID)
	}
	if res != nil && res.ComposersRewritten > 0 {
		fmt.Printf("Rewrote %d composerData blob(s) (trackedGitRepos / embedded paths).\n", res.ComposersRewritten)
	}
	if res != nil && (res.GlassProjectsUpdated > 0 || res.GlassKeysMoved > 0) {
		fmt.Printf("Agents Window: %d project(s), %d glass key(s).\n", res.GlassProjectsUpdated, res.GlassKeysMoved)
	}
	fmt.Println("Fully quit Cursor was required; reopen this project path now.")
	if _, err := os.Stat(f.from); err == nil && !strings.HasSuffix(f.from, ".__rebind_from_unknown") {
		fmt.Printf("\nIMPORTANT: %s still exists on disk.\n", f.from)
		fmt.Println("Rename or delete that leftover folder (after quitting Cursor), or Agents Window")
		fmt.Println("will keep an \"On <old-name>\" bucket and reopen old workspaceStorage.")
	}
	return nil
}

func runVerify(args []string) error {
	f, pos, err := parsePathFlags(args)
	if err != nil {
		return err
	}
	if f.help {
		fmt.Println("Usage: cursor-rebind verify [path]")
		return nil
	}
	pathArg := ""
	if len(pos) > 0 {
		pathArg = pos[0]
	}
	if pathArg == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		pathArg = wd
	}
	pathArg = absPath(pathArg)

	roots, err := paths.Discover()
	if err != nil {
		return err
	}
	inv, err := discover.Scan(roots)
	if err != nil {
		return err
	}
	exact, loose, agent := rebind.Verify(inv, pathArg)
	fmt.Printf("cursor-rebind verify\n")
	fmt.Printf("====================\n")
	fmt.Printf("Path:              %s\n", pathArg)
	fmt.Printf("Exact headers:     %d\n", exact)
	fmt.Printf("Same-name (other): %d\n", loose)
	fmt.Printf("Agent transcripts: %d\n", agent)
	if exact > 0 && loose == 0 {
		fmt.Println("Status:            healthy")
	} else if loose > 0 {
		fmt.Println("Status:            orphans remain — consider migrate")
	} else {
		fmt.Println("Status:            no headers for this path")
	}
	return nil
}

func runRestore(args []string) error {
	f, pos, err := parsePathFlags(args)
	if err != nil {
		return err
	}
	if f.help {
		fmt.Println("Usage: cursor-rebind restore <backup-id>")
		fmt.Println("       cursor-rebind restore --list")
		return nil
	}
	if f.list || (len(pos) == 1 && pos[0] == "list") {
		mans, err := backup.List()
		if err != nil {
			return err
		}
		if len(mans) == 0 {
			fmt.Println("No backups found.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tCREATED\tNOTE")
		for _, m := range mans {
			fmt.Fprintf(w, "%s\t%s\t%s\n", m.ID, m.CreatedAt.Format("2006-01-02 15:04"), m.Note)
		}
		return w.Flush()
	}
	if len(pos) != 1 {
		return fmt.Errorf("restore requires a backup id (see: cursor-rebind restore --list)")
	}
	if err := rebind.Restore(pos[0]); err != nil {
		return err
	}
	fmt.Printf("Restored backup %s. Reopen Cursor to load it.\n", pos[0])
	return nil
}

