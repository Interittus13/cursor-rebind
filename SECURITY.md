# Security Policy

## Supported versions

Security fixes are applied to the latest released version on
[GitHub Releases](https://github.com/Interittus13/cursor-rebind/releases).

| Version | Supported |
|---------|-----------|
| latest release | Yes |
| older releases | Best-effort |

## What this tool touches

cursor-rebind reads and writes local Cursor SQLite databases and related files
under the user’s Cursor config and `~/.cursor`. A bug can corrupt chat identity
or backups. Treat reports that affect data integrity as security-relevant even
if they are not remote code execution.

## Reporting a vulnerability

Please **do not** open a public GitHub issue for security problems.

Preferred options:

1. GitHub **Private vulnerability reporting** on this repository (Security tab), if enabled.
2. Open a private channel to the maintainer via the GitHub profile for
   [Interittus13](https://github.com/Interittus13) (security-related contact only).

Include:

- Affected version / commit
- OS and Cursor build if relevant
- Reproduction steps (use a **copy** of storage; do not attach real chat DBs unless asked)
- Impact (data loss, overwrite, unexpected network, etc.)

You should receive an acknowledgment within a few days. We will coordinate a fix
and disclosure timeline.
