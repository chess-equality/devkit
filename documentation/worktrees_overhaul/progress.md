# Progress Tracker

| Phase | Description | Owner(s) | Status | Notes |
| --- | --- | --- | --- | --- |
| Discovery | Capture current flows, pain points, and desired end-state (this doc set). | Platform | ✅ Done | Session notes 2024-XX. |
| Runtime Config Design | Decide on variable names, dotenv precedence, validation rules. | TBD | ⏳ Planned | Outcome feeds worktree helper rewrite. |
| **Milestone 1: Config Loader Skeleton** | Implement config parsing/validation behind a feature flag; wire helpers to consume the struct while defaulting to existing paths. | Platform | ✅ Done | Feature-flagged loader + path integration landed; validated via `go test ./...`. |
| **Milestone 2: Worktree Helper Rewrite** | Teach `worktrees.Setup` and related helpers to honor runtime config naming (`agent-<n>`), create control clones, and emit manifests while legacy paths remain as fallback. | Platform | ✅ Done | Validated with `DEVKIT_ENABLE_RUNTIME_CONFIG=1 go test ./cli/devctl/internal/worktrees -run TestSetup_RuntimeConfig_TwoAgents` and full `go test ./...`. |
| **Milestone 3: Overlay / Layout Integration** | Update overlays and layout parsing to request worktrees per repo instead of relying on `dev-all`. | Platform | ✅ Done | Validated via `go test ./...` (with updated dry-run integration) and layout worktree assertions. |
| Command Surface Updates | Align CLI commands (`run`, `worktrees-*`, layout apply, verify) with the new lifecycle. | Platform | ✅ Done | Runtime-config aware commands merged; `worktrees-plan` added. |
| Migration & Cleanup | Remove legacy docs, delete unused paths, verify onboarding flow. | Platform | ⏳ Planned | Next: overlay author checklist + doc refresh. |

Use this table to record decision PRs, blockers, and hand-offs as we execute. Update statuses and notes rather than creating parallel trackers.
