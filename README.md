Dev Kit — Reusable Containerized Dev Environment

This dev kit extracts the dual‑network, allowlisted egress development setup into a reusable package you can apply to any project in your `dev/` or `projects/` folder via small per‑project overlays.

## Before You Begin

Make sure the base toolchain is available on your host before trying to launch the kit:

- Docker Engine with the Compose plugin (or Docker Desktop) and permission to run containers.
- Go 1.21+ and `make` to build the CLI binary.
- `tmux` (optional but required for the tmux helpers to function).
- SSH key and optional Codex auth material in your home directory (`~/.ssh/id_ed25519` / `~/.ssh/id_rsa`, `~/.codex/auth.json`).

Run a quick compatibility check once the repo is cloned:

```
scripts/devkit preflight
```

The preflight command validates Docker/Compose, tmux, SSH keys, and Codex credentials and prints concrete follow-up steps for anything that is missing.

## Quick Start (codex overlay)

- Build the CLI (one time): `cd devkit/cli/devctl && make build` (outputs `devkit/kit/bin/devctl`).
- Verify the environment: `scripts/devkit preflight`.
- Start the default stack: `devkit/kit/scripts/devkit up` (defaults to `-p codex`).
- Open a shell inside agent 0: `devkit/kit/scripts/devkit exec 0 bash`.
- Allow a new domain through the proxy: `devkit/kit/scripts/devkit allow example.com`.
- Opt into extra hardening: `devkit/kit/scripts/devkit up --profile hardened,dns`.
- Shut everything down: `devkit/kit/scripts/devkit down`.

Tip: both `scripts/devkit` and `devkit/kit/scripts/devkit` now build the Go CLI automatically if the binary is missing, so a fresh clone can jump straight to `scripts/devkit up`.

### Overlay configuration helpers

Overlay metadata in `overlays/<project>/devkit.yaml` can now set:

- `workspace`: path (relative to the overlay by default) that the CLI resolves to an absolute `WORKSPACE_DIR` before compose runs.
- `env`: key/value pairs exported on the host unless already set, making it easy to share defaults like `AWS_PROFILE` across repos.

Compose overrides should prefer `${WORKSPACE_DIR}` when mounting the repo into `/workspace`; the template overlay has been updated to illustrate the pattern.

Credential pool (proposal, opt‑in):
- For teams needing multiple Codex identities, see `kit/docs/proposals/codex-credential-pool.md`.
- Summary: mount a read‑only pool of prepared Codex homes and seed agents from slots by index or per‑run shuffle. Defaults remain unchanged.
 - Usage (opt‑in):
   - `export DEVKIT_CODEX_CRED_MODE=pool`
   - `export DEVKIT_CODEX_POOL_DIR=/abs/path/to/pool`
   - Optional: `export DEVKIT_CODEX_POOL_STRATEGY=shuffle DEVKIT_CODEX_POOL_SEED=123`
   - Dry run: `devkit/kit/scripts/devkit --dry-run fresh-open 2`
   - Details: `kit/docs/README.md` and `kit/docs/testing/credential-pool.md`.

Essentials (batteries-included paths):
- Hard reset + open N agents (alias): `devkit/kit/scripts/devkit reset 3` (same as `fresh-open 3`).
- Scale agents without teardown: `devkit/kit/scripts/devkit scale 4`.
- Scale and sync tmux windows: `devkit/kit/scripts/devkit scale 5 --tmux-sync`.
- Per-agent SSH over 443: `devkit/kit/scripts/devkit ssh-setup --index 1` then `ssh-test 1`.

Tooling caches:
- SBT now writes to each agent's anchored home (`/workspace/.devhome/.sbt` or `/workspaces/dev/.devhome/.sbt` under `dev-all`) via `SBT_GLOBAL_BASE`. Ivy (`/home/dev/.ivy2`) and coursier (`/home/dev/.cache/coursier`) remain shared volumes to reuse downloaded artifacts.
- Verify the setup end-to-end: `devkit/kit/tests/per-agent-sbt/run-smoke.sh` spins up two codex agents and checks that each container's `/home/dev/.sbt` resolves to its own anchor.

Worktrees (isolated branches per agent, dev-all overlay):
- Defaults live in `overlays/dev-all/devkit.yaml` (repo, agents, base_branch, branch_prefix).
- Bootstrap end-to-end: `devkit/kit/scripts/devkit -p dev-all bootstrap` (uses defaults) or `bootstrap ouroboros-ide 3`.
- Create/verify manually:
  - Setup: `devkit/kit/scripts/devkit -p dev-all worktrees-setup ouroboros-ide 3`
  - Open windows: `devkit/kit/scripts/devkit -p dev-all worktrees-tmux ouroboros-ide 3`

Tmux ergonomics (new):
- Sync windows to running agents: `devkit/kit/scripts/devkit tmux-sync [--session NAME] [--count N] [--name-prefix PFX] [--cd PATH]`.
  - Defaults: session `devkit:<project>`, names `agent-<n>`, cd to `/workspace` (codex) or `/workspaces/dev[/agentN]` (dev-all).
- Add a single window at a path: `devkit/kit/scripts/devkit tmux-add-cd <index> <subpath> [--session NAME] [--name NAME]`.
  - Example (dev-all): `devkit/kit/scripts/devkit -p dev-all tmux-add-cd 2 dumb-onion-hax --name doh-2`.
  - Use the same `--session` across overlays to mix images in one tmux.
- Target a different service (non-default): append `--service <name>` to `tmux-sync`, `tmux-add-cd`, or `scale --tmux-sync`.
- Apply a layout file (YAML): `devkit/kit/scripts/devkit tmux-apply-layout --file tmux.yaml [--session NAME]`.
  - Example tmux.yaml:
    session: devkit:mixed
    windows:
      - index: 1
        path: ouroboros-ide
        name: ouro-1
        service: dev-agent
        # project: dev-all     # optional; defaults to current -p
      - index: 2
        path: dumb-onion-hax
        name: doh-2
        service: dev-agent

Declarative orchestration (new):
- Bring up overlays and then attach tmux from a single YAML:
  - `devkit/kit/scripts/devkit layout-apply --file orchestration.yaml`
  - Generate a YAML from running containers: `devkit/kit/scripts/devkit layout-generate --service dev-agent --output orchestration.yaml`
  - orchestration.yaml example:
    session: devkit:mixed
    overlays:
      - project: codex
        service: dev-agent
        count: 5
        profiles: dns,hardened
        compose_project: devkit-codex
      - project: dumb-onion-hax
        service: dev-agent
        count: 1
        profiles: dns
        compose_project: devkit-doh
      - project: pokeemerald
        service: dev-agent
        count: 2
        profiles: dns
        compose_project: devkit-emerald
      - project: dev-all
        service: dev-agent
        count: 3
        profiles: dns
        compose_project: devkit-devall
        # Optional: prepare host git worktrees before windows (dev-all only)
        worktrees:
          repo: dumb-onion-hax
          count: 3              # defaults to overlays.count when omitted
          base_branch: main     # optional; falls back to overlays/dev-all/devkit.yaml
          branch_prefix: agent  # optional; falls back to overlays/dev-all/devkit.yaml
    windows:
      - index: 1
        project: codex
        service: dev-agent
        path: /workspace
        name: ouro-1
      - index: 2
        project: codex
        service: dev-agent
        path: /workspace
        name: ouro-2
      - index: 3
        project: codex
        service: dev-agent
        path: /workspace
        name: ouro-3
      - index: 4
        project: codex
        service: dev-agent
        path: /workspace
        name: ouro-4
      - index: 5
        project: codex
        service: dev-agent
        path: /workspace
        name: ouro-5
      - index: 1
        project: dumb-onion-hax
        service: dev-agent
        path: /workspace
        name: doh-1
      # Example: windows targeting dev-all agents after worktrees
      - index: 1
        project: dev-all
        service: dev-agent
        path: dumb-onion-hax
        name: doh-wt-1
      - index: 2
        project: dev-all
        service: dev-agent
        path: agent-worktrees/agent2/dumb-onion-hax
        name: doh-wt-2
      - index: 1
        project: pokeemerald
        service: dev-agent
        path: /workspace
        name: emerald-1
      - index: 2
        project: pokeemerald
        service: dev-agent
        path: /workspace
        name: emerald-2

SSH (GitHub) quickstart:
- One-time per agent: `scripts/devkit ssh-setup --index 1` then `scripts/devkit ssh-test 1`
- Flip origin to SSH and push: `scripts/devkit repo-push-ssh .`

Layout:
- `kit/`: base Compose, proxy, DNS, scripts, and docs.
- `overlays/<project>/`: per‑project overrides (`compose.override.yml`, `devkit.yaml`).
  - Optional: `service: <name>` sets the default service for CLI exec/attach/ssh/repo commands (defaults to `dev-agent`).

Key design:
- Dual networks: `dev-internal` (internal: true) for agents; `dev-egress` for internet‑facing proxy.
- Proxy (Tinyproxy by default) is dual‑homed; agents only join `dev-internal` and must egress via proxy.
- Optional DNS allowlist (dnsmasq) and hardened profile (read‑only root, resource limits).

See `kit/docs/README.md` for more details.

New overlay guide:
- Step-by-step: `kit/docs/new-overlay-guide.md` (service selection, compose paths, networking, SSH/Git gotchas, and tmux tips.)

Overlay reuse:
- Keep the compiled kit in one checkout and point `DEVKIT_OVERLAYS_DIR` at your project-specific overlays (relative paths resolve against `DEVKIT_ROOT`; default is `<DEVKIT_ROOT>/overlays`).

Retrospectives and contributor guidance:
- Reliability retrospective: `kit/docs/retrospective-ssh-git-anchor.md`
- Contrib guideline (quoting + file writes): `kit/docs/contrib-quoting-and-file-writes.md`


Retrospective: Journey & Lessons
- Summary of the migration, networking fixes, Codex seeding/env work, tests, and next steps.
- See: `kit/docs/journey-retrospective.md`.

Postgres test broker plan
- Restricted Docker endpoint design for integration tests that require Postgres.
- See: `kit/docs/postgres-broker-plan.md`.


Proposal: Bash → Go CLI Migration
- Rationale, scope, and plan to migrate `kit/scripts/devctl` to a typed CLI while keeping shell shims.
- See: `kit/docs/proposals/devkit-cli-migration.md`.

## Portability and Onboarding Updates

Recent improvements:

- Wrapper scripts now rely on POSIX-compatible path resolution, eliminating the GNU `readlink -f` dependency on macOS and other BSD systems.
- Overlay configs populate `WORKSPACE_DIR` and default environment variables automatically, removing most hard-coded relative paths from compose overrides.
- Wrapper entrypoints auto-build the `devctl` binary with `make` when it is missing, so newcomers can launch the kit without a manual compile step.

Next focus areas:

- Add cross-platform smoke tests that exercise the wrapper scripts on macOS/Linux runners.
- Provide `devctl doctor` diagnostics that bundle the preflight checks with workspace validation, reducing guesswork for first-time contributors.
- Expand documentation with a troubleshooting matrix for common Docker/Compose startup failures.

Contributions that help exercise these flows across operating systems are welcome; see the proposals in `kit/docs/` for discussion threads and status updates.
