# Developer Onboarding

## Runtime Config Quick Start
- Install the CLI via the usual `make -C devkit/cli/devctl build` step.
- Set `DEVKIT_ENABLE_RUNTIME_CONFIG=1` and choose a host directory for worktrees:
  ```bash
  export DEVKIT_ENABLE_RUNTIME_CONFIG=1
  export DEVKIT_WORKTREE_ROOT="$HOME/devkit-worktrees"
  mkdir -p "$DEVKIT_WORKTREE_ROOT"
  ```
- Optional: place the exports in your shell profile or `.env.devkit` file.

## Overlay Checklist
- Follow the [worktree overlay onboarding checklist](worktrees_overhaul/checklists/overlay-onboarding.md) when enabling runtime-config for a repository.
- Ensure compose overrides mount `${DEVKIT_WORKTREE_ROOT}` into `${DEVKIT_WORKTREE_CONTAINER_ROOT:-/worktrees}`.

## Validation Commands
- Runtime doctor: `scripts/devkit doctor-runtime` (requires `DEVKIT_ENABLE_RUNTIME_CONFIG=1`).
- Worktree summary: `scripts/devkit worktrees-plan <repo>`.
- Integration smoke test: `DEVKIT_ENABLE_RUNTIME_CONFIG=1 DEVKIT_WORKTREE_ROOT=$(mktemp -d) go test ./cli/devctl/internal/worktrees -run TestSetup_RuntimeConfig_TwoAgents` (run from devkit root).
- Runtime integration suite: `make test-runtime` (builds the lightweight test image, provisions a temporary worktree root, and runs docker compose–backed checks; this suite now runs automatically in CI).

## Codex Credential Refresh
- The `codexw` wrapper copies credentials from the host mount (`/var/auth.json` or `/var/host-codex/auth.json`) only on first run. If you bump the Codex CLI version or see `Failed to refresh token: 401 Unauthorized`, remove the per-agent cache and rerun:
  ```bash
  docker exec <container> rm -f /workspace/.devhome/.codex/auth.json
  docker exec <container> codexw exec 'reply with: ok'
  ```
- The next invocation re-seeds the cache from the host copy, avoiding stale refresh tokens without forcing a full reauth.
