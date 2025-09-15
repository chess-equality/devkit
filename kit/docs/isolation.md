# Per‑Agent Isolation Plan (Medium Term)

Goal: give each running dev container its own working copy and its own Codex/SSH state, while keeping startup simple and resource usage low.

## Approach: Git Worktrees + Per‑Agent HOME

- Use Git worktrees to fork a working copy per agent while sharing the object database.
- Set a distinct HOME for each agent (e.g., `/workspace/.devhome-agentN`) so Codex sessions and SSH config are isolated.
- Keep the proxy/DNS policy and shared caches (Ivy/sbt/Coursier) as is.

### Why worktrees?
- Lightweight: forks working directories without cloning full history.
- Efficient: shared object store minimizes disk usage.
- Practical: agents can work on different branches concurrently without `.git/index` contention.

## Proposed Layout

On the host (under the same dev root):

```
/dev
  /ouroboros-ide                     # primary clone (origin)
  /agent-worktrees/agent2/ouroboros-ide  # worktree for agent #2
  /agent-worktrees/agent3/ouroboros-ide  # worktree for agent #3
  ...
```

- Create worktrees from the primary clone:
  - `cd ouroboros-ide && git worktree add ../agent-worktrees/agent2/ouroboros-ide <branch>`
  - Optionally create branches per agent (e.g., `agent2/main`).

## Container Mapping

Two options (not mutually exclusive):

1) dev-all overlay (recommended initial)
- Mount the whole dev root at `/workspaces/dev`.
- Exec into each subpath per agent:
  - `scripts/devkit -p dev-all exec-cd 1 ouroboros-ide bash`
  - `scripts/devkit -p dev-all exec-cd 2 agent-worktrees/agent2/ouroboros-ide bash`
- Set per‑agent HOME via codex wrapper or env when launching commands:
  - Agent 1: `HOME=/workspaces/dev/ouroboros-ide/.devhome-agent1`
  - Agent 2: `HOME=/workspaces/dev/agent-worktrees/agent2/.devhome-agent2`

2) per-agent overlays (polished follow‑up)
- Create tiny overlays `overlays/agent2` and `overlays/agent3` that mount the worktree path at `/workspace`.
- Each overlay sets `DEV_HOME=/workspace/.devhome` and resolves to its own working copy.

## Codex & SSH State

- Codex: use index‑free HOME anchor: `/workspace/.devhome` (symlink to `.devhomes/<container-id>`). The CLI ensures this on exec/attach.
- SSH: run `scripts/devkit ssh-setup --index N` once per agent window. The CLI writes SSH config using `~/.ssh/...` and sets global `git config core.sshCommand` so it never depends on an index.

```
Host github.com
  HostName ssh.github.com
  Port 443
  ProxyCommand nc -X connect -x tinyproxy:8888 %h %p
  IdentityFile ~/.ssh/id_ed25519
  IdentitiesOnly yes
  StrictHostKeyChecking accept-new
```

- Git: configure per‑container global SSH usage (index‑free):
  - `git config --global core.sshCommand 'ssh -F ~/.ssh/config'`

## tmux Integration

- tmux-shells N auto-runs `ssh-setup` for each index already.
- Enhancement (planned): detect dev-all and set per‑agent HOME env for each window (e.g., export `HOME=/workspaces/dev/agent-worktrees/agentN/.devhome-agentN`).

## Transition Steps

1) Pilot with dev-all + worktrees
- Create `agent2` (and agent3 if needed) worktrees under `agent-worktrees/agentN`.
- Use `exec-cd` to enter each repo path.
- Manually set Codex HOME per session if needed (wrapper picks up `/workspace/.devhome` by default).

2) Add per‑agent overlays (optional)
- Create `overlays/agent2/compose.override.yml` pointing to `../agent-worktrees/agent2/ouroboros-ide`.
- Set `DEV_HOME` or bake codex wrapper to use `/workspace/.devhome`.

3) Add helpers (optional)
- `scripts/devkit worktrees init N` to create N worktrees.
- `tmux-shells` to export per‑agent HOME and `cd` into the right path automatically.

## Risks & Mitigations

- Shared object store contention: avoid running `git gc` or global maintenance concurrently.
- Parallel builds: shared `target/` directories can race; ensure `sbt` runs are not overlapping across agents unless you use isolated target dirs.
- Developer UX: ensure prompts and aliases reflect which agent you’re in (e.g., PS1 or window title).

## Acceptance Criteria

- Two agents can concurrently work on separate branches without interfering.
- Codex sessions and SSH config are isolated per agent.
- Disk usage remains within reasonable limits (worktree delta only).
- Basic ops (ssh-setup, repo-push-ssh) work unchanged within each agent.
