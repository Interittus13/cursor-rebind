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

# Machine move (rewrite a path prefix)
cursor-rebind map --from /home/olduser --to /home/newuser --prefix

# Apply (quit Cursor first)
cursor-rebind migrate --from /old/path --to /new/path --yes

cursor-rebind verify /new/path
cursor-rebind restore --list
```

## How it works

Cursor ties chat history to a workspace path. cursor-rebind reconciles that identity across local storage — so the sidebar and agent history stay aligned after a move or restore.

## License

MIT
