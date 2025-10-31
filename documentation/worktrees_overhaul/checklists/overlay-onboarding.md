# Overlay Onboarding Checklist

Use this checklist when enabling a repository overlay for runtime-config worktrees.

## Repository Defaults
- [ ] Overlay `devkit.yaml` sets `defaults.repo` (name under the dev root).
- [ ] Overlay `devkit.yaml` sets `defaults.agents` (agent count bootstrap/run should target).
- [ ] Optional: override `defaults.base_branch` / `defaults.branch_prefix` if the repo diverges from `main`/`agent`.

## Compose Mounts
- [ ] `compose.override.yml` mounts the workspace as before (`${WORKSPACE_DIR}` → `/workspace`).
- [ ] `compose.override.yml` mounts `${DEVKIT_WORKTREE_ROOT}` into `${DEVKIT_WORKTREE_CONTAINER_ROOT:-/worktrees}` (rw).
- [ ] Containers reference worktrees via CLI helpers (no hard-coded `/workspaces/dev/...`).

## Layout and Worktrees
- [ ] Layouts or scripts declare a `worktrees` block when multiple agents are needed.
- [ ] `layout-apply` dry-run verified with `DEVKIT_ENABLE_RUNTIME_CONFIG=1 DEVKIT_WORKTREE_ROOT=...`.
- [ ] Worktree commands (`worktrees-status`, `worktrees-sync`, `worktrees-plan`) succeed outside containers when runtime config is enabled.

## Tooling & Docs
- [ ] Repository readme/onboarding references `DEVKIT_ENABLE_RUNTIME_CONFIG=1` and setting `DEVKIT_WORKTREE_ROOT`.
- [ ] Overlay-specific docs mention expected branch prefix / default agents.
- [ ] Any custom scripts updated to rely on `devctl worktrees-plan` or runtime helpers (no bespoke paths).

## Validation
- [ ] `go test ./cli/devctl/...` (from devkit root) passes.
- [ ] `DEVKIT_ENABLE_RUNTIME_CONFIG=1 DEVKIT_WORKTREE_ROOT=$(mktemp -d) go test ./cli/devctl/internal/worktrees -run TestSetup_RuntimeConfig_TwoAgents` passes.
- [ ] Manual smoke test: `DEVKIT_ENABLE_RUNTIME_CONFIG=1 DEVKIT_WORKTREE_ROOT=/path/to/worktrees scripts/devkit -p <overlay> layout-apply --dry-run ...` shows expected host commands.
- [ ] Run `scripts/devkit doctor-runtime` with containers up to confirm volumes mount inside the agent.
