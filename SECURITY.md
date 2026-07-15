# Security Policy

## Supported versions

Security fixes are applied to the **latest released version** on
[GitHub Releases](https://github.com/Interittus13/cursor-rebind/releases).

| Version | Supported |
|---------|-----------|
| Latest `vX.Y.Z` release | Yes |
| Prior releases | Best-effort only (please upgrade) |
| `main` / unreleased builds | Best-effort; may already include the fix |

We do not maintain long-term support (LTS) branches for this project.

## Scope

cursor-rebind is a **local** CLI. It reads and writes Cursor storage under the
user’s config directory and `~/.cursor` (SQLite DBs, JSON, agent project
trees). We treat the following as security-relevant even when they are not
remote code execution:

- Unexpected modification or deletion of Cursor / project data
- Integrity failures that drop or mis-attribute chats without a clear restore path
- Path traversal or writing outside intended Cursor storage roots
- Unexpected network access (this tool is intended to work offline against local files)
- Secrets or personal chat content leaking into logs, backups committed to git, or public issues

### Out of scope (examples)

- Bugs that only affect UI wording or non-security diagnostics
- Issues that require an already-compromised local machine or Cursor install
- Vulnerabilities solely in upstream Cursor itself (report those to Cursor)
- Social engineering against individual users

## Reporting a vulnerability

**Do not** open a public GitHub issue or discussion for security problems.

### Preferred: GitHub Private vulnerability reporting

1. Open the repository **Security** tab → **Advisories** / **Report a vulnerability**  
   Direct link (once enabled):  
   https://github.com/Interittus13/cursor-rebind/security/advisories/new
2. Or use GitHub’s **Private vulnerability reporting** flow if the button appears on that page.

If private reporting is not available yet, contact the maintainer privately via the GitHub profile for [Interittus13](https://github.com/Interittus13) and mark the message as a **security report** only.

### What to include

- Affected version (`cursor-rebind version`) or commit SHA
- OS and, if relevant, Cursor build
- Clear reproduction steps
- Impact (data loss, overwrite, path escape, unexpected network, etc.)
- Whether a fix or workaround is known

**Do not** attach real `state.vscdb` files, chat transcripts, or credentials unless a maintainer explicitly asks. Prefer a **redacted copy** of storage or a minimal synthetic reproduction.

### Our process

| Step | Target |
|------|--------|
| Acknowledgment | Within **72 hours** (usually sooner) |
| Initial assessment | Within **7 days** |
| Fix / advisory | Coordinated with reporter; we aim to patch the latest release promptly |
| Disclosure | After a fix is available, or by mutual agreement |

We follow responsible disclosure. Please give us a reasonable window to ship a
fix before publishing full exploit details.

We appreciate good-faith research. Reports made in line with this policy will
not result in legal action from the maintainers for accessing only test data
you control.

## Safe handling for contributors

When dogfooding or debugging:

- Fully quit Cursor before writing storage.
- Prefer copies of Cursor data; use tool backups under `~/.cursor-rebind/backups/`.
- Never commit personal DBs, tokens, or machine-specific home paths that identify others.

## Preferential thanks

We are happy to credit reporters in release notes or advisories unless you ask
to remain anonymous.
