# Contributing to cursor-rebind

Thanks for helping. This project rebinds Cursor chat identity after path and machine moves. Mistakes can corrupt local Cursor databases, so we keep the contribution bar practical and careful.

## Before you start

- Read the [README](README.md) and [machine-move guide](docs/machine-move.md).
- Follow the [Code of Conduct](CODE_OF_CONDUCT.md).
- For vulnerability reports, use [SECURITY.md](SECURITY.md) — do not open a public issue.

## Development setup

Requirements: Go 1.22+ (see `go.mod`).

```bash
git clone https://github.com/Interittus13/cursor-rebind.git
cd cursor-rebind
go test ./...
go build -o cursor-rebind ./cmd/cursor-rebind
./cursor-rebind version
./cursor-rebind scan
```

Optional:

```bash
make test
make build
make install
```

## How to contribute

1. Open an issue first for larger behavior changes (especially anything that rewrites more Cursor keys).
2. Branch from `main` (or the current feature branch maintainers are merging).
3. Keep PRs focused — one problem per PR when possible.
4. Add or update tests when changing rebind logic.
5. Run `go test ./...` before pushing.
6. Update docs when user-facing behavior or flags change.

### Dogfooding writes

If you test against real Cursor data:

- Fully quit Cursor first.
- Prefer a copy of storage, or rely on `~/.cursor-rebind/backups/` from migrate/repair.
- Never commit personal `state.vscdb`, chats, or home-directory paths that identify others.

## Code style

- Match existing Go style in `internal/`.
- Prefer small, named helpers over large duplicated Apply/Repair paths.
- Do not expand Agents identity rewrites without tests and a clear failure mode.
- Avoid drive-by refactors unrelated to the PR.

## Pull requests

Use the PR template. Include:

- What problem you solve
- How you tested (commands + platforms)
- Any intentional behavior change or follow-up

CI must pass (tests + release dry pieces where configured).

## Issue reports

Use the bug or feature templates under `.github/ISSUE_TEMPLATE/`. For bugs, include OS, Cursor version if known, exact commands, and whether IDE and/or Agents Window failed.

## Release process (maintainers)

Releases are tagged (`vX.Y.Z`) and published via GoReleaser (see `.github/workflows/release.yml`). Update [CHANGELOG.md](CHANGELOG.md) before tagging.
