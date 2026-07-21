# cursor-rebind

[![Release](https://img.shields.io/github/v/release/Interittus13/cursor-rebind?display_name=tag)](https://github.com/Interittus13/cursor-rebind/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/Interittus13/cursor-rebind)](https://github.com/Interittus13/cursor-rebind/blob/main/go.mod)
[![License](https://img.shields.io/github/license/Interittus13/cursor-rebind)](LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/Interittus13/cursor-rebind/ci.yml?branch=main&label=CI)](https://github.com/Interittus13/cursor-rebind/actions/workflows/ci.yml)

**Your Cursor chats. Still yours — wherever the project goes.**

Rename a folder. Move machines. Change a username. The conversations don’t vanish; they just lose their place. cursor-rebind brings them home — IDE sidebar and Agents Window, together again.

Works on **Linux**, **macOS**, and **Windows**.

cursor-rebind is an independent, community project. It is **not** affiliated with, endorsed by, or sponsored by Anysphere, Inc. or Cursor.

## Quick start

```bash
# Install (Linux / macOS)
curl -fsSL https://raw.githubusercontent.com/Interittus13/cursor-rebind/main/scripts/install.sh | bash

# Quit Cursor completely, then:
cursor-rebind map --from /old/path/to/project --to /new/path/to/project
cursor-rebind migrate --from /old/path/to/project --to /new/path/to/project --yes
cursor-rebind verify /new/path/to/project
```

First time? Run `cursor-rebind` with no args for a guided menu.

## When you need this

| Situation | What usually breaks |
|-----------|---------------------|
| Renamed / moved a project folder | IDE history empty or “New Agent” only |
| New laptop / OS reinstall / username change | Chats still under the old home path |
| Agents Window shows repo chats but IDE does not | Dual `workspaceStorage` ids (SPLIT-BRAIN) |

**Not** a Cursor cloud sync tool — it rewrites **local** storage identity (`globalStorage` + `workspaceStorage` + Agents glass state).

## Install

### Quick install (Linux / macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/Interittus13/cursor-rebind/main/scripts/install.sh | bash
```

This downloads the latest release and installs `cursor-rebind` onto your PATH (`~/.local/bin` or `/usr/local/bin`).

```bash
# Optional: pin a version or install location
CURSOR_REBIND_VERSION=v1.0.0 bash scripts/install.sh
CURSOR_REBIND_INSTALL_DIR=$HOME/.local/bin bash scripts/install.sh
```

### Go

```bash
go install github.com/Interittus13/cursor-rebind/cmd/cursor-rebind@latest
```

Ensure `$(go env GOPATH)/bin` is on your PATH.

### From source

```bash
git clone https://github.com/Interittus13/cursor-rebind.git
cd cursor-rebind
make install
```

### Windows

Download the Windows archive from [Releases](https://github.com/Interittus13/cursor-rebind/releases), extract `cursor-rebind.exe`, and place it on your PATH — or use WSL with the quick install above.

## Usage

```bash
# Guided menu (interactive terminal; recommended for first-time users)
cursor-rebind

cursor-rebind scan
cursor-rebind doctor /path/to/project

# Preview a rebind
cursor-rebind map --from /old/path --to /new/path

# Apply (quit Cursor fully first)
cursor-rebind migrate --from /old/path --to /new/path --yes

# Same, and delete orphaned old workspaceStorage afterward (opt-in)
cursor-rebind migrate --from /old/path --to /new/path --yes --cleanup

# Repair Agents/IDE identity after a partial migrate (quit Cursor first)
cursor-rebind repair --to /new/path --from /old/path --target-id <workspace-id> --yes

# Consolidate dual workspace ids for one folder (chats on leftover, Cursor opens empty shell)
cursor-rebind repair --to /path/to/project --yes

cursor-rebind verify /new/path
cursor-rebind restore --list
cursor-rebind version
```

### Guided mode

Running `cursor-rebind` with **no arguments** in a real terminal opens a menu (migrate, repair, scan, machine-move tips). Scripts and piped stdin still get the normal usage text. Flag-based commands stay fully supported for automation.

### Folder rename (same machine)

The primary path. After renaming a project dir:

1. Fully quit Cursor (not just reload).
2. Open the **new** folder once (or note its workspace id from `scan`).
3. Preview, then apply:

```bash
cursor-rebind map --from /old/path/to/xyz --to /new/path/to/abc
cursor-rebind migrate --from /old/path/to/xyz --to /new/path/to/abc --yes
# Optional: also purge orphaned old workspace data
cursor-rebind migrate --from /old/path/to/xyz --to /new/path/to/abc --yes --cleanup
```

4. Prefer `--target-id` when `scan` shows multiple workspace entries for the same folder (`scan` prints each workspace ID).
5. Remove or rename any leftover empty `--from` directory, then reopen only `--to`.

Exact-mode migrate updates both surfaces:

- **IDE:** `composer.composerHeaders` + open tabs/editor
- **Agents:** composer workspace identity, glass projects/tabs, retired `--from` metadata, and agent transcripts when needed

`Updated 0 header(s)` is OK when headers already point at `--to`.

### `--cleanup` (opt-in)

After a successful **exact** migrate/repair, `--cleanup` deletes:

- Orphaned `workspaceStorage/<old-id>/` directories (already pointed at `.__rebind_orphan_*`)
- Leftover `~/.cursor/projects/<old-slug>` if still present

It does **not** delete your project folder on disk, global chat blobs (already retagged), or the target workspace. Refused with `--prefix`. Tool `restore` may not fully recreate purged workspace trees (DB files were backed up; full dir trees were not).

### Machine move / OS reinstall / username change

Copying only `workspaceStorage` is not enough. Chat headers, composer blobs, and Agents glass state live mainly in **globalStorage**. See the full playbook:

**[docs/machine-move.md](docs/machine-move.md)** — what to back up, where it lives on each OS, how to restore, when to use `--prefix` vs exact migrate, and what to do for projects you have not cloned yet.

### Prefix vs exact

| Mode | When | What it does |
|------|------|----------------|
| Exact (default) | One project path → another | Full IDE + Agents identity rebind |
| `--prefix` | Home/username prefix rewrite | Rewrites matching path prefixes in headers / `workspace.json` / projects; Agents may still need exact migrate/repair per project |

```bash
# Preview username change only
cursor-rebind map --from /home/olduser --to /home/newuser --prefix
cursor-rebind migrate --from /home/olduser --to /home/newuser --prefix --yes
```

### Notes

- Quit Cursor completely before `migrate` / `repair` (reload is not enough).
- Prefer `--target-id` when multiple `workspaceStorage` entries exist for the same folder.
- **Never delete the empty shell** Cursor minted for `--to` and consolidate onto the older data leftover — Cursor remints that shell and IDE/Agents stay empty. Migrate/repair attach chats **onto** the emptiest/newest shell, then orphan siblings.
- Exact `migrate` / `repair` run a post-apply **health check** (single live workspace id + named chats on that id). Failure exits non-zero with a `repair --to` hint; use `verify` / `doctor` to detect `SPLIT-BRAIN`.
- `migrate` strategy (`create` / `replace-empty` / `merge`) chooses plan messaging and which chat becomes the primary tab. Apply steps are the same; **merge does not combine two threads into one**.
- Tool backups from migrate/repair live under `~/.cursor-rebind/backups/` and can be listed with `cursor-rebind restore --list`.
- Use `--cleanup` only after you are happy with the migrate; default is to keep path-orphaned storage as a safety net.

## How it works

Cursor ties chat history to a workspace path. cursor-rebind reconciles that identity across local storage — so the sidebar and agent history stay aligned after a move or restore.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Please read the [Code of Conduct](CODE_OF_CONDUCT.md) and [Security policy](SECURITY.md).

## Changelog

See [CHANGELOG.md](CHANGELOG.md).

## Disclaimer

cursor-rebind is an independent open-source tool. It is **not affiliated with, endorsed by, or sponsored by Anysphere, Inc. or Cursor**. “Cursor” and related marks are trademarks of their respective owners. Use at your own risk; always quit Cursor and keep backups before rewriting local storage.
