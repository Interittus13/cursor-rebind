# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Machine-move documentation (`docs/machine-move.md`) with backup/restore and prefix vs exact guidance
- Contributing guide, code of conduct, security policy, changelog, and GitHub issue/PR templates

## [1.0.0] - TBD

First public release.

### Added

- `scan`, `doctor`, `map`, `migrate`, `repair`, `verify`, `restore`, `version` CLI
- Exact-mode migrate/repair for IDE composer headers and Agents Window identity
- Prefix-mode path rewrite for home/username changes
- Automatic backups under `~/.cursor-rebind/backups/` with restore
- Cross-platform storage discovery (Linux, macOS, Windows)
- Install script and GoReleaser-based multi-platform binaries

[Unreleased]: https://github.com/Interittus13/cursor-rebind/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/Interittus13/cursor-rebind/releases/tag/v1.0.0
