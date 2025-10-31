# Runtime Worktree Testing Plan

## Goals
- Exercise the runtime-config workflow end-to-end (host + container) rather than relying on dry-run output.
- Detect missing mounts or misconfigured env early by running `doctor-runtime`, `worktrees-plan`, and git operations inside containers.
- Ensure tests run in isolation, even when a developer already has a compose stack active (unique compose project name, temp worktree root).
- Make the test suite maintainable and type-safe by using Go for orchestration where possible (wrapping docker compose / doctor commands).

## Proposed Structure

### 1. Package Layout
- Add `cli/devctl/integration/runtime` package containing Go tests that bring up a purpose-made, minimal compose stack (not the production overlays) with `DEVKIT_ENABLE_RUNTIME_CONFIG=1` and a temp `DEVKIT_WORKTREE_ROOT`.
- Use a helper fixture to generate a unique `COMPOSE_PROJECT_NAME`, pick a safe subnet, and register cleanup via `t.Cleanup`.
- Tests execute real CLI commands via Go (`exec.CommandContext`) and assert on stderr/stdout.

### 2. Test Cases
1. **doctor-runtime**
   - Bring up codex overlay with unique project/env.
   - Run `scripts/devkit doctor-runtime` and assert zero exit, presence of “container … mounts /worktrees”.
2. **layout-apply**
   - Prepare a layout referencing two overlays (dev-all + front) with worktree blocks.
   - Run `layout-apply --dry-run` followed by actual `layout-apply` (with unique compose project) and confirm worktrees exist on host (using manifest / git status).
3. **worktrees-sync/status**
   - After layout, run `worktrees-status <repo>` / `worktrees-sync <repo> --pull` (host mode) to ensure git commands succeed.
4. **Verify**
   - Execute `scripts/devkit verify` for the overlay and ensure codex exec passes.

### 3. Test Utilities
- Helper to set env (`DEVKIT_ENABLE_RUNTIME_CONFIG`, `DEVKIT_WORKTREE_ROOT`, `COMPOSE_PROJECT_NAME`, isolated subnet).
- `RuntimeFixture` wraps stack bring-up and registers `t.Cleanup` to call `docker compose down --remove-orphans` no matter how the test exits.
- Helper to run CLI commands (wrapping `exec.CommandContext`, returning stdout/stderr, exit code).

### 4. Non-interference
- Always set `COMPOSE_PROJECT_NAME=devkit-test-<random>`.
- Worktree root lives under a temp dir (`t.TempDir()`), cleaned after test.
- Compose commands use the dedicated test compose file and env scoped to the fixture.
- Each fixture allocates its own subnet/IP range to avoid colliding with developer networks.

### 5. Type Safety & Maintainability
- Tests defined in Go (strong typing, reuse existing exec helpers).
- Central helper functions live in a new package (`internal/testutil`) and encapsulate the `t.Cleanup` lifecycle.
- Use contexts/timeouts to avoid hanging docker commands.

### 6. CI Integration
- Add a make target (`make test-runtime`) that runs the new test package, optionally guarded by `DEVKIT_RUNTIME_TESTS=1` so developers can opt-in.
- Document prerequisites: docker available, no conflicting compose project names.

### 7. Documentation
- Update `documentation/developer-onboarding.md` and `worktrees_overhaul` docs to reference `make test-runtime` as part of validation.

## Next Steps
1. Implement `internal/testutil/fixture.go` providing `RuntimeFixture` with `t.Cleanup` guarantees.
2. Create a minimal compose file under `testing/runtime/compose.test.yml` for the fixture.
3. Write `runtime/doctor_test.go` covering the doctor command.
3. Add additional tests iteratively (layout apply, worktrees sync).
4. Wire `make test-runtime` target and update CI config.
