# Codex Layout Apply Retrospective (Nov 2025)

## Context
- Objective: ship a “one command” orchestration (`devkit/kit/examples/orchestration-codex8-worktrees.yaml`) that launches eight codex agents with dedicated worktrees and tmux windows.
- Constraints: developers expect to run `scripts/devkit -p codex layout-apply --file ... --attach` with **no** manual tweaks to `DEVKIT_INTERNAL_SUBNET`/`DEVKIT_DNS_IP`, even when other stacks are already running.
- Observed failures:
  - `Address already in use` / `pool overlaps` when Docker reuses the previous subnet.
  - Layout entries with `project: dev-all` still resolve to the default `codex` overlay, so compose never picks up the intended stack.
  - `--attach` crashes in non-interactive sessions (`open terminal failed`), making automation flaky.

## What We Tried
1. **Added subnet selection + retry loop** in `layout-apply`. We detect existing networks, pick a CIDR, and re-run `docker compose` when Docker complains.
2. **Enhanced `netutil`** to read Docker-managed CIDRs and probe availability by creating temporary networks.
3. **Documented the codex8 example** and noted the runtime credential refresh requirement.
4. **Automatically seed SSH + worktrees** before tmux attaches so agents come up ready to `git pull`.

## Why It Still Fails
- Docker only throws the networking error **after** containers start (when tmux seeding runs), so the retry loop never gets a second CIDR.
- The layout overlay resolver still honors the CLI `-p` value (“codex”) instead of the YAML entry (“dev-all”), so we never switch compose stacks.
- `--attach` runs even when stdout isn’t a TTY, causing immediate failure in headless contexts (CI, agent shells).

## Next Steps for the On-Call Agent
1. **Move subnet retries earlier.**
   - Detect Docker errors during `compose up` *and* during the subsequent seeding commands. If any step hits `Address already in use`, tear everything down, mark the CIDR as bad, and retry with a fresh one before continuing.
2. **Honor layout overlay projects.**
   - When `layout.yaml` specifies `project: dev-all`, resolve `dev-all`’s `devkit.yaml` / compose override even if the CLI was invoked with `-p codex`.
3. **Graceful tmux attach.**
   - Skip `--attach` automatically when `DEVKIT_NO_TMUX=1` or stdout isn’t a TTY; print a notice instead of failing.
4. **Add regression coverage.**
   - Write a Go test (or script) that:
     1. Runs `layout-apply` on `orchestration-codex8-worktrees.yaml`.
     2. Ensures eight containers stay up.
     3. Confirms `codexw exec "reply with: ok"` works in at least one container.
     4. Tears the stack down cleanly.

## Useful Artifacts
- Failure log (captured from customer run): `/tmp/layout.log` shows only “attempt 1” and no fallback CIDR.
- Example command to reproduce:
  ```bash
  export DEVKIT_ENABLE_RUNTIME_CONFIG=1
  export DEVKIT_WORKTREE_ROOT="$HOME/devkit-worktrees"
  scripts/devkit -p codex layout-apply \
    --file devkit/kit/examples/orchestration-codex8-worktrees.yaml \
    --attach
  ```

Keep this document updated as we tighten the workflow. The goal remains “no manual env, no subnet tweaks, codex layout applies first try.”
