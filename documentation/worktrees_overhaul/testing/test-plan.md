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
- Add a make target (`make test-runtime`) that runs the new test package and wire it into CI so the runtime suite executes on every PR.
- Document prerequisites: docker available, no conflicting compose project names.

### 7. Documentation
- Update `documentation/developer-onboarding.md` and `worktrees_overhaul` docs to reference `make test-runtime` as part of validation.

## Status
- ✅ `internal/testutil/fixture.go` now provides a `RuntimeFixture` that provisions repositories, generates unique compose project names, and wires cleanup via `t.Cleanup`.
- ✅ Minimal compose and overlay assets live under `testing/runtime/` (image, compose stubs, codex shim, proxy stubs) to keep runtime stacks lightweight and isolated.
- ✅ Runtime integration tests (`doctor-runtime`, `layout-apply`, `worktrees-status/sync`, `verify`) reside in `cli/devctl/integration/runtime`.
- ✅ `make test-runtime` now runs the suite automatically; CI invokes it on every change, and developers can run it locally with the same command.
- ✅ `CleanupSharedInfra` honors `DEVKIT_SKIP_SHARED_CLEANUP=1`, preventing runtime tests from tearing down the globally-named proxy containers (`devkit_tinyproxy`, `devkit_dns`). The fixture exports this guard by default.
- ✅ Dedicated proxy stubs in `testing/runtime/kit/` use compose-project-scoped container names, so even if cleanup runs, it only touches the isolated test stack.
