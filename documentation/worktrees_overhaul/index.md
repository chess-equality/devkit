# Worktree Orchestration Initiative

This space tracks the effort to make multi-repo, multi-agent development predictable without relying on overlay quirks or hard-coded paths.

- [Overview](overview.md) — intent, guardrails, and the end-state we are steering toward.
- [Progress Tracker](progress.md) — phases, owners, and current status.
- [Runtime Configuration](runtime-config.md) — how the devkit discovers worktree roots and per-repo settings.
- [Overlay Integration](overlay-integration.md) — expected changes for repository overlays and layout files.
- [Worktree Lifecycle](worktree-lifecycle.md) — host Git operations and container path mapping.
- [Onboarding Checklists](checklists/overlay-onboarding.md) — steps for overlay owners migrating to runtime-config worktrees.
- [Testing Plan](testing/test-plan.md) — roadmap for container-backed runtime tests.
- [Testing Retrospective](testing/testing-retro.md) — notes from the failed compose-based test attempt.
- [Codex Layout Retrospective](retrospective-codex-layout.md) — November 2025 lessons learned from the `orchestration-codex8-worktrees` rollout, covering subnet reuse failures and tmux attach gaps.

Each page is intentionally high signal: capture unknowns, record decisions, and link to implementation work items as they spin up.
