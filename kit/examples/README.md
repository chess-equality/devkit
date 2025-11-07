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
devkit/kit/scripts/devkit layout-apply \
  --file devkit/kit/examples/orchestration-codex8-worktrees.yaml \
  --tmux --attach
```

`--tmux` forces window creation even if `DEVKIT_NO_TMUX=1`, and `--attach` drops you straight into the `devkit_codex8` session. Because the layout lists `project: dev-all`, no `-p` flag is required; if you omit `project` fields in your own layout, pass `-p <overlay>` so the CLI knows which stack to reuse. Use `COMPOSE_PROJECT_NAME=devkit-codex8 devkit/kit/scripts/devkit -p codex down` when you're done.

### Automated check
`kit/tests/codex-layout-verify.sh` runs the full workflow (layout apply, tmux session creation, Codex “ok”, `git pull`, tmux/compose teardown) and auto-detects the correct `scripts/devkit` shim. Example:

```bash
DEVKIT_ENABLE_RUNTIME_CONFIG=1 \
DEVKIT_WORKTREE_ROOT=$HOME/devkit-worktrees \
devkit/kit/tests/codex-layout-verify.sh
```

The script resolves the layout path, forces tmux, verifies the eight codex windows exist, runs `codexw exec --skip-git-repo-check "reply with: ok"`, performs `git fetch && git pull`, and tears the stack down so the next run starts clean.
