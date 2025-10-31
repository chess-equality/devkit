# Overlay Integration

## Current State
- `dev-all` overlay acts as the de facto multiplexer: it mounts the entire dev root and carries defaults for repo/branch prefixes.
- Other overlays (e.g., `ouroboros-static-front-end`) mount a single `${WORKSPACE_DIR}` pointing to a repo checkout, with no awareness of worktrees.
- Layout templates embed hard-coded paths like `agent-worktrees/agentN/<repo>`, tying window definitions to today’s directory layout.

## Desired Changes
1. **Per-repo overlays own their workspace instructions.** `devkit.yaml` files continue to set `workspace`, but the CLI rewrites `${WORKSPACE_DIR}` to point at `<worktree root>/<repo>/agent<idx>` for the active agent. Overlay authors no longer reference `agent-worktrees` directly.
2. **Layout metadata expresses worktree needs per overlay.** We either expand the existing `worktrees` stanza to allow any overlay to opt-in, or add a `worktree` block inside `devkit.yaml` (single source TBD). Layout apply reads this data and orchestrates host setup before tmux windows spawn.
3. **Overlay env application injects runtime config.** `applyOverlayEnv` exports `DEVKIT_WORKTREE_ROOT` (host) and `DEVKIT_WORKTREE_CONTAINER_ROOT` (defaults to `/worktrees`) so compose files and scripts can mount the worktree hierarchy without guessing.
4. **Examples reflect mixed overlays without dev-all.** Update orchestration templates to show multiple repos with independent counts using the new helper functions.
5. **Compose mounts worktrees generically.** Overlays reference `${DEVKIT_WORKTREE_ROOT}` and `${DEVKIT_WORKTREE_CONTAINER_ROOT}` to ensure every agent sees the correct host directories.

## Task Breakdown
| Task | Owner | Status | Notes |
| --- | --- | --- | --- |
| Decide metadata home (`layout` vs `devkit.yaml`) | Platform | ✅ | Keep `worktrees` in layout overlays; fall back to overlay defaults when values omitted. |
| Update `applyOverlayEnv` to surface runtime vars | Platform | ✅ | CLI now exports `DEVKIT_WORKTREE_ROOT` / `DEVKIT_WORKTREE_CONTAINER_ROOT` before overlays apply. |
| Refactor compose overrides to use injected paths | Repo owners | ⏳ | Provide migration guidance + lint check. Use `${DEVKIT_WORKTREE_ROOT}` helpers for flagged mode. |
| Update layout templates (`kit/examples/...`) | Platform | ✅ | Example now shows per-overlay worktrees. |
| Add integration tests for mixed overlays | QA | ✅ | Dry-run integration asserts secondary overlay worktree setup. |

## Dependencies
- Runtime config loader (see `runtime-config.md`) must exist before overlays can rely on new variables.
- Worktree lifecycle rewrite must define the canonical naming convention for agent directories.
- Documentation updates should ship with the compose/layout changes to avoid confusing new users.

## Milestone 3 Deliverables
- Extend layout parsing so any overlay can declare a `worktrees` block (or equivalent) that maps to runtime-config directories.
- Ensure `layout-apply` provisions worktrees by reading overlay metadata, even when `project != dev-all`.
- Update at least one example overlay (e.g., ouroboros static front end) and mixed layout template to illustrate per-overlay worktrees.
- Add regression tests that run `layout-apply --dry-run` with `DEVKIT_ENABLE_RUNTIME_CONFIG=1` and confirm worktree creation commands use the new paths.
- Ship operator tooling (`devctl worktrees-plan <repo>`) so developers can inspect runtime-config worktrees without running into the container.
