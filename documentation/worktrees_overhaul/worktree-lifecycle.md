# Worktree Lifecycle

## Overview
Every agent runs inside a managed worktree. The lifecycle must stay deterministic so repeated commands either succeed or fail with context.

## Host Responsibilities
1. Validate runtime config and repo availability before touching Git.
2. Maintain a canonical control clone per repo (for `git worktree` bookkeeping), kept out of the agent path (e.g., `<worktree root>/<repo>/_control`).
3. For each requested agent index, ensure a worktree directory exists under `<worktree root>/<repo>/agent-<index>` (hyphen chosen to avoid shell collisions).
4. Rewrite `.git` files to use relative paths so container mounts remain portable.
5. Clean up stale directories that no longer point at the repo’s worktree metadata.
6. Record a manifest (`manifest.json`?) summarizing the active worktrees to aid diagnostics and future garbage collection.

## Container Mapping
- Helper functions compute `WORKDIR` and `HOME` based on the resolved worktree path and agent index.
  - `WORKDIR`: `${DEVKIT_WORKTREE_CONTAINER_ROOT:-/worktrees}/<repo>/agent-<index>`
  - `HOME`: `${DEVKIT_WORKTREE_CONTAINER_ROOT:-/worktrees}/<repo>/agent-<index>/.devhome-agent<index>`
- tmux/window builders use those helpers instead of embedding `/workspaces/dev/...`.
- Seeding scripts (`seed.BuildSeedScripts`, SSH setup) operate on the derived HOME; they should require no changes once path helpers are centralized.
- Compose overrides mount `${DEVKIT_WORKTREE_ROOT}` into `${DEVKIT_WORKTREE_CONTAINER_ROOT}` so all agents see the worktree hierarchy; repo-specific directories are selected at runtime by the CLI helpers.

## Failure Handling
- Missing runtime config → abort before running Git.
- Worktree add failure → delete the target directory, retry once, surface stderr if the second attempt fails.
- Worktree cleanup failure → include path information and remediation steps (e.g., “remove manually or run git worktree prune”).
- If the control clone is missing, emit guidance to run `git clone` into the expected location or run a bootstrap command that fetches it.

## Instrumentation
- Log planned actions in dry-run mode so developers can confirm which directories would be touched.
- Diagnostic command `devctl worktrees-plan <repo>` reports host paths and branches (reads manifest + git worktree list).
- Emit structured logs (JSON or key-value) when creating/removing worktrees to aid debugging via `DEVKIT_DEBUG`.
- Integration tests should assert both filesystem layout and `git status`/upstream configuration for each agent to prevent regressions.
- `manifest.json` now captures control + agent paths; future tooling can extend or consume it instead of re-scanning the filesystem.

## Next Milestone Focus
- Add automated checks (`doctor-runtime`, future lint) to ensure compose files mount worktrees before launching agents.
- Design cleanup mechanics that prune stale manifests/worktrees once overlays or counts change.
- Document a rollback path in case runtime-config needs to be temporarily disabled for a repo.
