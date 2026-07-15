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

# Apply (quit Cursor fully first)
cursor-rebind migrate --from /old/path --to /new/path --yes

# Repair Agents/IDE identity after a partial migrate (quit Cursor first)
cursor-rebind repair --to /new/path --from /old/path --target-id <workspace-id> --yes

cursor-rebind verify /new/path
cursor-rebind restore --list
cursor-rebind version
```

### Folder rename (same machine)

The primary path. After renaming a project dir:

1. Fully quit Cursor (not just reload).
2. Open the **new** folder once (or note its workspace id from `scan`).
3. Preview, then apply:

```bash
cursor-rebind map --from /old/path/to/xyz --to /new/path/to/abc
cursor-rebind migrate --from /old/path/to/xyz --to /new/path/to/abc --yes
```

4. Prefer `--target-id` when `scan` shows multiple workspace entries for the same folder.
5. Remove or rename any leftover empty `--from` directory, then reopen only `--to`.

Exact-mode migrate updates both surfaces:

- **IDE:** `composer.composerHeaders` + open tabs/editor
- **Agents:** composer workspace identity, glass projects/tabs, retired `--from` metadata, and agent transcripts when needed

`Updated 0 header(s)` is OK when headers already point at `--to`.

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
- `migrate` strategy (`create` / `replace-empty` / `merge`) chooses plan messaging and which chat becomes the primary tab. Apply steps are the same; **merge does not combine two threads into one**.
- Tool backups from migrate/repair live under `~/.cursor-rebind/backups/` and can be listed with `cursor-rebind restore --list`.

## How it works

Cursor ties chat history to a workspace path. cursor-rebind reconciles that identity across local storage — so the sidebar and agent history stay aligned after a move or restore.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Please read the [Code of Conduct](CODE_OF_CONDUCT.md) and [Security policy](SECURITY.md).

## Changelog

See [CHANGELOG.md](CHANGELOG.md).

## License

[MIT](LICENSE)
