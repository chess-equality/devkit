Examples for tmux layouts and orchestration

- Dry-run: append `--dry-run` to print docker/tmux commands without executing.

Useful commands
- Preview orchestration: `devkit/kit/scripts/devkit --dry-run layout-apply --file devkit/kit/examples/orchestration.yaml`
- Apply orchestration: `devkit/kit/scripts/devkit layout-apply --file devkit/kit/examples/orchestration.yaml`
- Preview layout-only windows: `devkit/kit/scripts/devkit --dry-run tmux-apply-layout --file devkit/kit/examples/tmux.yaml`
- Apply layout-only windows: `devkit/kit/scripts/devkit tmux-apply-layout --file devkit/kit/examples/tmux.yaml`

## Codex (8 agents + worktrees)

`orchestration-codex8-worktrees.yaml` launches the `dev-all` overlay with eight codex agents and prepares matching `ouroboros-ide` worktrees. No manual subnet tweaking is required; the file pins a non-conflicting range so multiple layouts can be applied in parallel.

```bash
export DEVKIT_ENABLE_RUNTIME_CONFIG=1
export DEVKIT_WORKTREE_ROOT="$HOME/devkit-worktrees"
mkdir -p "$DEVKIT_WORKTREE_ROOT"
devkit/kit/scripts/devkit -p codex layout-apply --file devkit/kit/examples/orchestration-codex8-worktrees.yaml
```

Use `COMPOSE_PROJECT_NAME=devkit-codex8 devkit/kit/scripts/devkit -p codex down` when you're done.
