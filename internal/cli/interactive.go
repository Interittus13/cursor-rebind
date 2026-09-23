package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Interittus13/cursor-rebind/internal/discover"
	"github.com/Interittus13/cursor-rebind/internal/paths"
	"github.com/Interittus13/cursor-rebind/internal/rebind"
)

func isInteractiveTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// runInteractiveMenu is the no-args TTY entry point for non-technical users.
func runInteractiveMenu() error {
	in := bufio.NewReader(os.Stdin)
	fmt.Println("cursor-rebind — guided setup")
	fmt.Println("Quit Cursor completely before migrate or repair.")
	fmt.Println()
	for {
		fmt.Println("What do you want to do?")
		fmt.Println("  1) Migrate a renamed or moved project")
		fmt.Println("  2) Repair Agents/IDE after a partial migrate")
		fmt.Println("  3) Scan workspaces")
		fmt.Println("  4) Prune stale leftovers (dry-run)")
		fmt.Println("  5) Machine-move backup tips")
		fmt.Println("  6) Quit")
		fmt.Print("Choice [1-6]: ")
		choice, err := readLine(in)
		if err != nil {
			return err
		}
		switch strings.TrimSpace(choice) {
		case "1":
			if err := wizardMigrate(in); err != nil {
				return err
			}
		case "2":
			if err := wizardRepair(in); err != nil {
				return err
			}
		case "3":
			if err := runScan(nil); err != nil {
				fmt.Fprintf(os.Stderr, "scan: %v\n", err)
			}
		case "4":
			if err := runPrune(nil); err != nil {
				fmt.Fprintf(os.Stderr, "prune: %v\n", err)
			}
		case "5":
			printMachineMoveTips()
		case "6", "q", "quit", "":
			fmt.Println("Bye.")
			return nil
		default:
			fmt.Println("Please enter a number from 1 to 6.")
		}
		fmt.Println()
	}
}

func wizardMigrate(in *bufio.Reader) error {
	fmt.Println()
	fmt.Println("Migrate chats from an old folder path to a new one.")
	from, err := promptPath(in, "Old project path (--from)")
	if err != nil {
		return err
	}
	to, err := promptPath(in, "New project path (--to)")
	if err != nil {
		return err
	}
	if from == to {
		return fmt.Errorf("old and new paths are the same")
	}

	roots, err := paths.Discover()
	if err != nil {
		return err
	}
	inv, err := discover.Scan(roots)
	if err != nil {
		return err
	}

	targetID, err := promptTargetID(in, inv, to)
	if err != nil {
		return err
	}

	plan, err := rebind.BuildPlanWithTarget(inv, from, to, rebind.ModeExact, targetID)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Print(rebind.FormatPlan(plan))
	fmt.Println()

	ok, err := promptYesNo(in, "Apply this migrate now? Cursor must be fully quit.", false)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("Cancelled. Nothing was changed.")
		return nil
	}
	cleanup, err := promptYesNo(in,
		"Remove old Cursor workspace data for the previous path? (recommended after a successful migrate; does not delete your project folder)",
		false)
	if err != nil {
		return err
	}

	res, err := rebind.Apply(inv, plan, true, false, cleanup)
	printMigrateResult(res)
	if err != nil {
		return err
	}
	return nil
}

func wizardRepair(in *bufio.Reader) error {
	fmt.Println()
	fmt.Println("Repair IDE tabs + Agents Window when chats already moved partially.")
	to, err := promptPath(in, "Current project path (--to)")
	if err != nil {
		return err
	}
	fromRaw, err := promptOptional(in, "Old path (--from), or leave blank if unknown")
	if err != nil {
		return err
	}
	from := fromRaw
	if from == "" {
		from = to + ".__rebind_from_unknown"
	} else {
		from = absPath(expandHome(from))
	}

	roots, err := paths.Discover()
	if err != nil {
		return err
	}
	inv, err := discover.Scan(roots)
	if err != nil {
		return err
	}

	targetID, err := promptTargetID(in, inv, to)
	if err != nil {
		return err
	}

	plan, err := rebind.BuildPlanWithTarget(inv, from, to, rebind.ModeExact, targetID)
	if err != nil {
		return err
	}
	fmt.Printf("\nRepair plan\nTo: %s\nTarget ID: %s\n", plan.To, plan.TargetWSID)
	if len(plan.SourceWSIDs) > 0 {
		fmt.Printf("Sources: %s\n", strings.Join(plan.SourceWSIDs, ", "))
	}
	fmt.Println()

	ok, err := promptYesNo(in, "Apply repair now? Cursor must be fully quit.", false)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("Cancelled. Nothing was changed.")
		return nil
	}
	cleanup, err := promptYesNo(in,
		"Remove old Cursor workspace data for the previous path?",
		false)
	if err != nil {
		return err
	}

	res, err := rebind.RepairTabs(inv, plan, true, cleanup)
	fmt.Println("Done. Primary IDE tab + Agents Window glass identity repaired.")
	if res != nil && res.BackupID != "" {
		fmt.Printf("Backup: %s\n", res.BackupID)
	}
	if res != nil && res.SourceStoragePurged > 0 {
		fmt.Printf("Removed %d old workspaceStorage / project leftover(s).\n", res.SourceStoragePurged)
	}
	if res != nil && res.HealthOK {
		fmt.Println("Workspace health: healthy (single live id for --to).")
	}
	fmt.Println("Reopen this project path in Cursor.")
	if err != nil {
		return err
	}
	return nil
}

func promptTargetID(in *bufio.Reader, inv *discover.Inventory, to string) (string, error) {
	to = filepath.Clean(to)
	var matches []discover.Workspace
	for _, w := range inv.Workspaces {
		fp := filepath.Clean(w.FolderPath)
		if fp == to || strings.HasPrefix(fp, to+".__rebind_orphan_") {
			// Prefer live rows in the picker; still show orphans for awareness.
			matches = append(matches, w)
		}
	}
	// Live (non-orphan) first for picking the keep id.
	var live []discover.Workspace
	for _, w := range matches {
		if !discover.IsOrphanFolderPath(w.FolderPath) && !discover.IsOrphanFolderPath(w.FolderURI) {
			live = append(live, w)
		}
	}
	pickFrom := live
	if len(pickFrom) == 0 {
		pickFrom = matches
	}

	if len(pickFrom) <= 1 {
		if len(pickFrom) == 1 {
			fmt.Printf("Using workspace id %s\n", pickFrom[0].ID)
			return pickFrom[0].ID, nil
		}
		raw, err := promptOptional(in, "Target workspace id (optional; leave blank to auto-pick)")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(raw), nil
	}

	recommended := ""
	if h := rebind.AssessPathHealth(inv, to, ""); h != nil && h.KeepID != "" {
		recommended = h.KeepID
	}

	fmt.Printf("SPLIT-BRAIN: %d workspace entries for %s\n", len(pickFrom), to)
	fmt.Println("Pick the shell Cursor opens (usually fewer named chats / the newer empty id).")
	for i, w := range pickFrom {
		ex := "no"
		if w.PathExists {
			ex = "yes"
		}
		mark := ""
		if w.ID == recommended {
			mark = "  ← recommended keep"
		}
		fmt.Printf("  %d) %s  headers=%d  exists=%s%s\n", i+1, w.ID, w.HeaderChats, ex, mark)
	}
	fmt.Print("Pick a number (or paste a workspace id; blank = recommended): ")
	line, err := readLine(in)
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" && recommended != "" {
		return recommended, nil
	}
	if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(pickFrom) {
		return pickFrom[n-1].ID, nil
	}
	return line, nil
}

func promptPath(in *bufio.Reader, label string) (string, error) {
	for {
		fmt.Printf("%s: ", label)
		line, err := readLine(in)
		if err != nil {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			fmt.Println("Path is required.")
			continue
		}
		if paths.LooksLikeWorkspaceID(line) {
			fmt.Println(paths.ErrWorkspaceIDAsPath("path", line))
			fmt.Println("Enter the project folder path instead.")
			continue
		}
		return absPath(expandHome(line)), nil
	}
}

func promptOptional(in *bufio.Reader, label string) (string, error) {
	fmt.Printf("%s: ", label)
	return readLine(in)
}

func promptYesNo(in *bufio.Reader, question string, defaultYes bool) (bool, error) {
	hint := "y/N"
	if defaultYes {
		hint = "Y/n"
	}
	fmt.Printf("%s [%s]: ", question, hint)
	line, err := readLine(in)
	if err != nil {
		return false, err
	}
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultYes, nil
	}
	return line == "y" || line == "yes", nil
}

func expandHome(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		if p == "~" {
			return home
		}
		return filepath.Join(home, p[2:])
	}
	return p
}

func readLine(in *bufio.Reader) (string, error) {
	line, err := in.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func printMachineMoveTips() {
	fmt.Println()
	fmt.Println("Machine move / OS reinstall — quick tips")
	fmt.Println("----------------------------------------")
	fmt.Println("1. Fully quit Cursor on the old machine.")
	fmt.Println("2. Back up ALL of these (not just workspaceStorage):")
	fmt.Println("   - User/globalStorage/   (headers + composerData + Agents glass)")
	fmt.Println("   - User/workspaceStorage/")
	fmt.Println("   - ~/.cursor/            (especially projects/)")
	fmt.Println("3. Restore onto the new machine, quit Cursor, then:")
	fmt.Println("   - Prefix home rewrite if the username changed")
	fmt.Println("   - Exact migrate per project you will use")
	fmt.Println("4. Full playbook: docs/machine-move.md in the cursor-rebind repo")
	fmt.Println("   https://github.com/Interittus13/cursor-rebind/blob/main/docs/machine-move.md")
}
