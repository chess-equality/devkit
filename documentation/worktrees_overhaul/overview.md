# Overview

## Intent
- Let new engineers spin up any combination of repos and agent counts without tribal knowledge.
- Replace the special-case `dev-all` overlay with a consistent per-repo experience.
- Ensure failure modes are loud: missing binaries, uncloned repos, and misconfigured worktree roots should stop early with actionable fixes.

## Guiding Principles
- **Single source of truth**: a repo-agnostic runtime config (dotenv or environment) provides the canonical worktree root and global toggles.
- **Per-repo ownership**: overlays ship the docker build instructions, dependencies, and repo defaults; devkit orchestration only stitches them together.
- **Worktree-only agents**: every agent runs inside a managed worktree directory; the base checkout exists solely to host `git worktree` metadata.
- **Predictable paths**: container helpers derive their working directory and HOME from the runtime config rather than hard-coded `/workspaces/dev/...`.
- **No silent fallbacks**: if the runtime config or overlay data is incomplete, commands fail with next steps instead of guessing.

## End-State Snapshot
1. A developer configures `DEVKIT_WORKTREE_ROOT` (or the agreed variable) once; the CLI validates the path before doing work.
2. Repository overlays expose everything compose needs (workspace mount, env files, profiles) while referencing the dynamic worktree location.
3. `devkit` commands (layout apply, run, tmux helpers, etc.) ensure the required number of worktrees exist per repo before starting agents.
4. Adding a new repo means adding its overlay plus optional overlay-specific config—not touching shared Go code for bespoke paths.
5. Documentation and CLIs default to the new flow; legacy instructions are removed once this plan lands.
