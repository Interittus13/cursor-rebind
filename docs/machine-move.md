# Machine move, OS reinstall, and username change

Use this when Cursor data moves to a new machine, a fresh OS install, or a different home directory / username.

Exact `migrate` for a single renamed folder is covered in the [README](../README.md). This guide is about **backing up the right directories**, restoring them, then rebinding paths.

## Quit Cursor first

Fully quit Cursor on the old machine before copying anything (reload is not enough). Databases under `User/` are SQLite and can be half-written while the app is open.

## What to back up

Chats are **not** only in `workspaceStorage`.

| Path (relative) | Why it matters |
|-----------------|----------------|
| `User/globalStorage/` (especially `state.vscdb` + `-wal`/`-shm` if present) | Composer headers, composerData, Agents glass / project lists |
| `User/workspaceStorage/` | Per-workspace DBs, open tabs, workspace folder metadata |
| `~/.cursor/` (especially `projects/`) | Agent project dirs and transcripts used by Agents Window history |

Optional but useful:

- `User/settings.json`, keybindings, snippets — editor prefs, not chat identity
- Extension data under `User/globalStorage/<publisher>.<ext>/` — only if you care about those extensions

### Where Cursor stores `User/` by OS

| OS | Typical `User` directory |
|----|---------------------------|
| Linux | `~/.config/Cursor/User` |
| macOS | `~/Library/Application Support/Cursor/User` |
| Windows | `%APPDATA%\Cursor\User` |

`~/.cursor` is under the user’s home on all platforms.

### Example: archive on the old machine (Linux)

```bash
# Fully quit Cursor first
OLD_HOME="$HOME"   # or /home/olduser
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
OUT="$HOME/cursor-chat-backup-$STAMP.tar.gz"

tar -czf "$OUT" \
  -C "$OLD_HOME/.config/Cursor" User/globalStorage User/workspaceStorage \
  -C "$OLD_HOME" .cursor

ls -lh "$OUT"
```

Adjust roots for macOS/Windows to the table above. Prefer copying both `globalStorage` and `workspaceStorage`; copying only workspaces usually leaves the IDE with nothing useful to rebind.

## Restore on the new machine

1. Install Cursor and open it once (creates empty storage), then **fully quit** again.
2. Restore the archived dirs into the **new** user’s Cursor locations (same relative layout).
3. If the username / home changed, paths inside the DBs still say `/home/olduser/...` until you run cursor-rebind.
4. Install [cursor-rebind](../README.md), then `cursor-rebind scan` to confirm workspaces and headers appear.

Do not start a long coding session until rebind is done if you care about keeping old chat identity.

## Rebind after restore

### A. Only the home prefix changed

Projects still live at the same relative paths under the new home:

```text
/home/olduser/Documents/Projs/foo  →  /home/newuser/Documents/Projs/foo
```

1. Preview and apply a **prefix** rewrite:

```bash
cursor-rebind map --from /home/olduser --to /home/newuser --prefix
# Quit Cursor
cursor-rebind migrate --from /home/olduser --to /home/newuser --prefix --yes
```

2. For each project you actively use (especially Agents Window), open it in Cursor once, quit again, then run an **exact** migrate or repair so glass / composer identity matches:

```bash
cursor-rebind migrate \
  --from /home/olduser/Documents/Projs/foo \
  --to   /home/newuser/Documents/Projs/foo \
  --yes
```

After a successful prefix pass, `--from` may already look like the new path in storage; use whatever `map` / `scan` / `doctor` still report as the old identity. Prefer `--target-id` from `scan` when multiple workspace ids point at the same folder.

### B. Some projects also changed folder layout

Example: old `…/Documents/Projs/foo`, new `…/Documents/Git/foo`.

Prefix alone is not enough for that project. After (or instead of) the home prefix step, exact migrate:

```bash
cursor-rebind migrate \
  --from /home/olduser/Documents/Projs/foo \
  --to   /home/newuser/Documents/Git/foo \
  --yes
```

Use one exact migrate per destination path. There is no single command that retargets five unrelated folders at once.

### C. Projects you have not cloned yet

You do not need empty Cursor workspaces for uncloned projects.

- Identity can already point at the new path after a prefix rewrite.
- Clone and open the folder when you need it.
- If Agents / IDE look wrong after the first open, quit Cursor and run exact `migrate` or `repair` for that `--to` path.

## Tool-made backups (migrate / repair)

Each write creates a snapshot under `~/.cursor-rebind/backups/`:

```bash
cursor-rebind restore --list
cursor-rebind restore <backup-id>
```

Use these to undo a bad migrate/repair on the **current** machine. They are not a substitute for the machine-move archive above.

## Optional `--cleanup` after exact migrate

Exact migrate path-orphans old `workspaceStorage` dirs by default (safe). After you confirm IDE + Agents look right, you can delete those orphans:

```bash
cursor-rebind migrate --from … --to … --yes --cleanup
```

Or answer Yes to the cleanup prompt in the guided menu (`cursor-rebind` with no args).

`--cleanup` never deletes your project folder on disk. It is refused with `--prefix`. Prefer archiving `globalStorage` + `workspaceStorage` for machine moves; cleanup is not a substitute for that archive.

## Checklist

- [ ] Cursor fully quit on the old machine
- [ ] Backed up `globalStorage` + `workspaceStorage` + `~/.cursor`
- [ ] Restored onto the new machine’s matching paths
- [ ] Cursor fully quit before any migrate/repair
- [ ] Ran prefix migrate if home/username changed
- [ ] Ran exact migrate/repair per project you care about (especially Agents)
- [ ] Optional: `--cleanup` after confirming chats look correct
- [ ] Verified with `cursor-rebind doctor <path>` / `verify <path>` and a real reopen in Cursor
- [ ] No `SPLIT-BRAIN` in verify/doctor (if seen: `repair --to <path> --yes` after quitting Cursor)

## Dual workspace ids (split-brain)

After a rename or machine move, Cursor may leave **two** `workspaceStorage/<id>/` folders whose `workspace.json` both point at the same path: an empty shell it opens, and a leftover that still holds named chats. IDE history then looks empty while Agents Window may group chats under the GitHub repo name.

**Rule:** always attach chats onto the shell Cursor opens (fewest named chats / newest empty id), then orphan the other. Do not delete the shell and keep the leftover.

```bash
cursor-rebind doctor /path/to/project   # look for SPLIT-BRAIN
# quit Cursor fully
cursor-rebind repair --to /path/to/project --yes
cursor-rebind verify /path/to/project
```
