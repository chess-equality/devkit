Contrib Guideline — Quoting and File Writes

Goals
- Eliminate brittle shell quoting and ensure config/files are written correctly inside containers.

Principles
- Prefer Go over shell heredocs for generating config text.
- Use `RunWithInput` (stdin) to write files; always pair with `chmod`.
- Precede every write with `mkdir -p` of the target directory.
- For config formats like ssh_config, write raw paths; do not use shell quotes (OpenSSH treats them literally).
- Keep bash snippets minimal: compose small commands rather than large one‑liners.

Patterns
- Write file safely (stdin + chmod):
  - `exec ... bash -lc "mkdir -p '<dir>' && cat > '<file>' && chmod 600 '<file>'"` with content provided via stdin.
- Avoid compose `--index` for selection; resolve containers by `com.docker.compose.*` labels and `docker exec`.
- Generate config text in Go (e.g., `internal/sshcfg.BuildGitHubConfigFor`).

Checklist for new flows
- [ ] Anchor HOME resolved and exported.
- [ ] `~/.ssh` created and chmod 700.
- [ ] Files written via `RunWithInput` + `mkdir -p` and chmod.
- [ ] No quotes in ssh_config paths; absolute or `~` as appropriate.
- [ ] Global Git set under the effective HOME; repo‑local overrides cleared when necessary.
- [ ] Validate inside the container: `ssh -F ~/.ssh/config -T github.com -o BatchMode=yes` and `git pull --ff-only`.

