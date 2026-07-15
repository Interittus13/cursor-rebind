# Contributing to cursor-rebind

Thanks for helping. This project rebinds Cursor chat identity after path and
machine moves. Mistakes can corrupt local Cursor databases, so we keep the
contribution bar practical and careful.

## Before you start

- Read the [README](README.md) and [machine-move guide](docs/machine-move.md).
- Follow the [Code of Conduct](CODE_OF_CONDUCT.md).
- For vulnerability reports, use [SECURITY.md](SECURITY.md) — **do not** open a public issue.
- By contributing, you agree that your contributions are licensed under the
  project’s [MIT License](LICENSE).

## Development setup

Requirements: **Go 1.24+** (see `go.mod`).

```bash
git clone https://github.com/Interittus13/cursor-rebind.git
cd cursor-rebind
go test ./...
go build -o bin/cursor-rebind ./cmd/cursor-rebind
./bin/cursor-rebind version   # shows "dev" unless built with VERSION=…
./bin/cursor-rebind scan
```

Optional:

```bash
make test
make build                 # VERSION defaults to "dev"
make build VERSION=1.0.0   # bake a release-like version string
make install
```

## How to contribute

1. Prefer an issue first for larger behavior changes (especially anything that
   rewrites more Cursor keys or changes migrate/repair defaults).
2. Branch from `main` (or the branch maintainers name in the issue).
3. Keep PRs focused — one problem per PR when possible.
4. Add or update tests when changing rebind logic (`internal/rebind`, `internal/vscdb`, CLI parsing).
5. Run `go test ./...` before pushing.
6. Update docs (`README.md`, `docs/machine-move.md`, `CHANGELOG.md` under
   `[Unreleased]`) when user-facing behavior or flags change.

Suggested branch names: `fix/…`, `feat/…`, `docs/…`.

### Dogfooding writes

If you test against real Cursor data:

- Fully quit Cursor first (reload is not enough).
- Prefer a copy of storage, or rely on `~/.cursor-rebind/backups/` from migrate/repair.
- Never commit personal `state.vscdb`, chats, tokens, or home-directory paths that identify others.

## Code style

- Match existing Go style in `internal/`.
- Prefer small, named helpers over large duplicated Apply/Repair paths.
- Do not expand Agents identity rewrites without tests and a clear failure mode.
- Avoid drive-by refactors unrelated to the PR.
- Do not commit secrets; local git hooks may scan commits with gitleaks.

## Pull requests

Use the PR template. Include:

- What problem you solve and why
- How you tested (commands + platforms; note if only `--dry-run`)
- Any intentional behavior change or follow-up

CI must pass. Maintainers may ask for a disposable-storage smoke test before
merging migrate/repair changes.

## Issue reports

Use the bug or feature templates under `.github/ISSUE_TEMPLATE/`. For bugs,
include OS, `cursor-rebind version`, exact commands, and whether **IDE** and/or
**Agents Window** failed. Do not paste private transcripts or upload real
Cursor DBs unless asked.

## Security

See [SECURITY.md](SECURITY.md) for private reporting, supported versions, and scope.

## Release process (maintainers)

1. Move `[Unreleased]` notes into a dated section in [CHANGELOG.md](CHANGELOG.md).
2. Tag `vX.Y.Z` on `main` and push the tag.
3. GoReleaser publishes binaries via [`.github/workflows/release.yml`](.github/workflows/release.yml).
4. Confirm `cursor-rebind version` on a release install matches the tag (not `dev`).
