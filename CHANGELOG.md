# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2026-07-15

First public release.

### Added

- `scan`, `doctor`, `map`, `migrate`, `repair`, `verify`, `restore`, `version` CLI
- Exact-mode migrate/repair for IDE composer headers and Agents Window identity
- Prefix-mode path rewrite for home/username changes
- Automatic backups under `~/.cursor-rebind/backups/` with restore
- Cross-platform storage discovery (Linux, macOS, Windows)
- Install script and GoReleaser-based multi-platform binaries
- Guided interactive menu when `cursor-rebind` is run with no args in a TTY
- Opt-in `--cleanup` on exact migrate/repair to purge orphaned source `workspaceStorage` and leftover project slugs
- Human `scan` table includes workspace `ID` for `--target-id`
- Post-apply workspace health check on exact migrate/repair (fails on dual live `workspaceStorage` ids / off-target named chats)
- `verify` and `doctor` detect **SPLIT-BRAIN** dual workspace identity and suggest `repair --to`
- `repair --to <path>` (without `--from`) consolidates dual workspace ids onto the shell Cursor opens
- Machine-move documentation (`docs/machine-move.md`) with backup/restore and prefix vs exact guidance
- Contributing guide, code of conduct, security policy, changelog, and GitHub issue/PR templates

[Unreleased]: https://github.com/Interittus13/cursor-rebind/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/Interittus13/cursor-rebind/releases/tag/v1.0.0
