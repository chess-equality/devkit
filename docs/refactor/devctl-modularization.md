# devctl Modularization Roadmap

This document tracks the gradual decomposition of `cli/devctl/main.go` into
smaller, testable packages. Each phase lands as its own change set to keep the
CLI usable throughout the migration.

## Phase 1 – Shared runner helpers *(completed)*
- Extracted host/docker helper functions into `internal/runner`.
- `main.go` now delegates all direct host and compose invocations to that
  package, shrinking its helper surface.

## Phase 2 – Command registry *(in progress)*
- Introduced `internal/cmdregistry` with a lightweight registry and shared
  context struct.
- `main.go` constructs a registry/context before falling back to the legacy
  `switch` to enable incremental command extractions.

## Phase 3 – Command packages *(in progress)*
- `preflight`, `verify-all`, `allow`, `proxy`, `check-net`, `check-codex`,
  `warm`, and `maintain` now live under `internal/commands/*` and register with
  the command registry.
- Next: continue migrating additional low-risk commands and document patterns
  for future handlers.

### Command Package Index

| Package | Commands |
| --- | --- |
| `internal/commands/allow` | `allow` |
| `internal/commands/composecmd` | `up`, `down`, `restart`, `status`, `logs` |
| `internal/commands/network` | `proxy`, `check-net`, `check-codex` |
| `internal/commands/hooks` | `warm`, `maintain` |
| `internal/commands/tmuxcmd` | `tmux-sync`, `tmux-add-cd`, `tmux-apply-layout` |

## Phase 4 – Flow orchestration *(planned)*
- Extract multi-step flows (e.g. `fresh-open`, `reset`) into orchestrator
  packages that compose smaller command helpers.

## Phase 5 – CLI front door cleanup *(planned)*
- Simplify `main.go` to flag parsing + registry dispatch, making it feasible to
  adopt a structured CLI framework later if desired.

Each phase updates this roadmap with links to commits/PRs and any follow-up
notes.
