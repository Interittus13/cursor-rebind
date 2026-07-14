# cursor-rebind

Move a project. Switch machines. Keep your Cursor chats.

When a folder path changes, Cursor treats it as a new workspace. Your conversations are still on disk — they just lose their identity. cursor-rebind finds them and puts them back where they belong.

Works on Linux, macOS, and Windows.

## Install

### Binary

Download the latest release for your platform:

https://github.com/Interittus13/cursor-rebind/releases

```bash
# Linux (x86_64)
curl -fsSL -o cursor-rebind.tar.gz \
  https://github.com/Interittus13/cursor-rebind/releases/latest/download/cursor-rebind_Linux_x86_64.tar.gz
tar -xzf cursor-rebind.tar.gz
sudo install -m 755 cursor-rebind /usr/local/bin/cursor-rebind
```

```bash
# macOS (Apple Silicon)
curl -fsSL -o cursor-rebind.tar.gz \
  https://github.com/Interittus13/cursor-rebind/releases/latest/download/cursor-rebind_Darwin_arm64.tar.gz
tar -xzf cursor-rebind.tar.gz
sudo install -m 755 cursor-rebind /usr/local/bin/cursor-rebind
```

### Go

```bash
go install github.com/Interittus13/cursor-rebind/cmd/cursor-rebind@latest
```

### From source

```bash
git clone https://github.com/Interittus13/cursor-rebind.git
cd cursor-rebind
go build -o cursor-rebind ./cmd/cursor-rebind
```

## Usage

```bash
cursor-rebind scan
cursor-rebind doctor
cursor-rebind doctor /path/to/project
```

## How it works

Cursor ties chat history to a workspace path. cursor-rebind reconciles that identity across local storage — so the sidebar and agent history stay aligned after a move or restore.

## License

MIT
