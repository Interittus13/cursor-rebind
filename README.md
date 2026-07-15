# cursor-rebind

Move a project. Switch machines. Keep your Cursor chats.

When a folder path changes, Cursor treats it as a new workspace. Your conversations are still on disk — they just lose their identity. cursor-rebind finds them and puts them back where they belong.

Works on Linux, macOS, and Windows.

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
cursor-rebind scan
cursor-rebind doctor /path/to/project

# Preview a rebind
cursor-rebind map --from /old/path --to /new/path

# Machine move (path-prefix rewrite only; Agents Window may still need exact migrate/repair)
cursor-rebind map --from /home/olduser --to /home/newuser --prefix

# Apply (quit Cursor fully first)
cursor-rebind migrate --from /old/path --to /new/path --yes

# Repair Agents/IDE identity after a partial migrate (quit Cursor first)
cursor-rebind repair --to /new/path --from /old/path --target-id <workspace-id> --yes

cursor-rebind verify /new/path
cursor-rebind restore --list
```

**Notes**
- Quit Cursor completely before `migrate` / `repair` (reload is not enough).
- Prefer `--target-id` when multiple `workspaceStorage` entries exist for the same folder.
- After a folder rename, delete or rename any leftover empty `--from` directory, then reopen only `--to`.
- `migrate` strategy (`create` / `replace-empty` / `merge`) chooses plan messaging and which chat becomes the primary tab. Apply steps are the same; **merge does not combine two threads into one**.
- Exact-mode migrate/repair updates both surfaces:
  - IDE: `composer.composerHeaders` + open tabs/editor
  - Agents: `composerData.workspaceIdentifier` + `trackedGitRepos.repoUrl`, glass projects/tabs, and retired `--from` metadata
- `Updated 0 header(s)` is OK when headers already point at `--to`.

## How it works

Cursor ties chat history to a workspace path. cursor-rebind reconciles that identity across local storage — so the sidebar and agent history stay aligned after a move or restore.

## License

MIT
