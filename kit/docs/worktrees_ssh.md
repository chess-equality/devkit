# SSH + Worktrees Workflow

This guide shows how to run multiple isolated dev agents using Git worktrees and per‑agent SSH/Codex state.

## When to use
- You want N parallel agents, each with its own working copy and independent Codex/SSH state.
- You want quick switching between branches without stepping on other agents.

## One‑time setup (dev‑all overlay)
1) Mount the whole dev root: `scripts/devkit -p dev-all up`
2) Create N worktrees for a repo (e.g., 2 for `ouroboros-ide`):
   - `scripts/devkit worktrees-init ouroboros-ide 2`
3) Open tmux with N windows, one per worktree:
   - `scripts/devkit -p dev-all worktrees-tmux ouroboros-ide 2`
   - Auto‑runs `ssh-setup` for each agent.
   - Per window:
     - Agent 1: `/workspaces/dev/ouroboros-ide`, `HOME=/workspaces/dev/ouroboros-ide/.devhome-agent1`
     - Agent 2: `/workspaces/dev/agent-worktrees/agent2/ouroboros-ide`, `HOME=/workspaces/dev/agent-worktrees/agent2/.devhome-agent2`

## SSH (GitHub) notes
- `ssh-setup` copies your host key and writes a proxy‑aware SSH config (port 443 via tinyproxy).
- It ensures an index‑free HOME anchor `/workspace/.devhome` and runs `git config --global core.sshCommand 'ssh -F ~/.ssh/config'` so Git uses the proxy‑aware SSH config automatically.
- Test: `scripts/devkit ssh-test <index>`

## Common workflows
- Switch worktree branch:
  - `scripts/devkit -p dev-all worktrees-branch ouroboros-ide 2 feature/my-branch`
- Status across worktrees:
  - `scripts/devkit -p dev-all worktrees-status ouroboros-ide`
- Sync:
  - Pull: `scripts/devkit -p dev-all worktrees-sync ouroboros-ide --pull --all`
  - Push: `scripts/devkit -p dev-all worktrees-sync ouroboros-ide --push --all`
- Flip origin to SSH and push (codex overlay):
  - `scripts/devkit repo-config-ssh . && scripts/devkit repo-push-ssh .`

## Alternative: codex overlay (shared mount)
- For quick starts without worktrees:
  - `scripts/devkit open 2`
  - Opens `tmux` with 2 windows and sets per‑container HOME via the index‑free anchor `/workspace/.devhome`.
  - Use this when shared working copy is acceptable (Codex/SSH still isolated).

## Caveats
- Avoid running global Git maintenance (e.g., `git gc`) across worktrees concurrently.
- sbt targets (`target/`) are shared per repo path; parallel builds can contend unless using isolated output dirs.
- Ensure `ssh.github.com` remains in the allowlist; `ssh-setup` manages this idempotently.
