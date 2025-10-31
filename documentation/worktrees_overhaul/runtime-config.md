# Runtime Configuration

## Goals
- Centralize environment discovery so host-side Git helpers and container path builders agree on where worktrees live.
- Allow per-developer overrides via environment variables or a `.env` file without editing Go code.
- Validate configuration up-front to avoid half-provisioned worktrees or misleading tmux sessions.

## Proposed Inputs
- `DEVKIT_WORKTREE_ROOT` — absolute path on the host where all managed worktrees live (required).
- `DEVKIT_CONFIG_FILE` (optional) — path to a dotenv file to load before command execution. Defaults to `$DEVKIT_ROOT/.env.devkit` if not provided.
- Overlay-provided defaults (repo name, branch prefix, agent count) remain in each repo’s `devkit.yaml`.
- Future: repo-scoped overrides via `<repo>/.devkitrc` if we need per-repo knobs; not required for the first pass.

## Load Order
1. CLI determines the root (`paths.Root`) then looks for `DEVKIT_CONFIG_FILE`; if unset, probe `$ROOT/.env.devkit` and `$ROOT/env/.env`.
2. Parse dotenv (if present) without exporting unset variables. Fail fast on syntax errors.
3. Apply explicit environment variables (they always win over dotenv).
4. Overlay env files (`devkit.yaml -> env_files`) run next, but cannot override `DEVKIT_WORKTREE_ROOT` unless explicitly forced.
5. Validate the resulting configuration once; share the parsed struct with downstream helpers.

## Validation Rules
- `DEVKIT_WORKTREE_ROOT` must be an absolute path. Relative paths trigger an error with instructions to fix the dotenv or export.
- If the directory does not exist, suggest `mkdir -p` and abort. We do not auto-create roots to avoid typos turning into new folders.
- On macOS/WSL, warn when the root lives on network volumes that may not support Git worktree metadata (best-effort detection).
- Dry-run commands still validate config; they simply refrain from modifying disk.

## Expected Behavior
1. CLI startup loads configuration and emits a single line confirming the active root when `DEVKIT_DEBUG=1`.
2. Helper packages (`paths`, `worktrees`, tmux orchestration) consume the shared struct, allowing tests to inject fake roots easily.
3. Host Git commands derive worktree locations from `<root>/<repo>/agent<N>` (exact naming defined in the lifecycle doc).
4. Containers receive mount paths derived from the same config; compose overrides reference `${DEVKIT_WORKTREE_ROOT}` or helper-injected variables.
5. `DEVKIT_WORKTREE_ROOT` and `DEVKIT_WORKTREE_CONTAINER_ROOT` (default `/worktrees`) are exported before overlay env files run, so compose overrides can mount the host worktree root into a stable container location.

## Open Questions
- Where should we source fallback defaults during migration? (e.g., detect legacy `/workspaces/dev/agent-worktrees` but still error loudly.)
- Do we support multiple named roots (per repo) or a single shared root with subdirectories? Current plan assumes single root.
- How do we surface configuration issues in non-interactive commands (CI, tests) where stdout guidance may be ignored?
- Should we cache the parsed config to disk for diagnostics (e.g., `~/.cache/devkit/config.json`) or recompute every run?
- Can overlays opt into read-only roots for pool-style environments, or do we treat the root as always writable?

## Testing Notes
- The first implementation milestone keeps legacy paths as the default. Set `DEVKIT_ENABLE_RUNTIME_CONFIG=1` when exercising the new loader.
- Unit coverage: `go test ./cli/devctl/...` should exercise config parsing, validation, and helper wiring.
- Manual validation: run `COMPOSE_PROJECT_NAME=devkit-wt-test scripts/devkit -p codex verify` (or similar) so Docker Compose uses an isolated project name and does not interfere with active developer agents.
