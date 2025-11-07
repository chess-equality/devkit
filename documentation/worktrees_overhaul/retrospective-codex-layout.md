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

## Current Status (Dec 2025)
- Layout retries now wrap the entire bring-up: `withNetwork` restarts CIDR selection and `docker compose up` whenever Docker reports "Address already in use" and only reserves the subnet once the stack is healthy.
- Layout entries own their compose args end-to-end, so YAML requesting `project: dev-all` behaves the same regardless of which overlay name was passed to `-p`.
- `--tmux` force-overrides `DEVKIT_NO_TMUX`, while `--attach` still skips non-TTY invocations. No more manual env flipping is required to see the codex windows.
- `kit/tests/codex-layout-verify.sh` is the regression test: it finds the correct `scripts/devkit` shim, resolves the layout file, forces tmux, asks Codex to reply "ok", runs `git fetch && git pull`, and tears the stack down. Run it with:
  ```bash
  DEVKIT_ENABLE_RUNTIME_CONFIG=1 \
  DEVKIT_WORKTREE_ROOT=$HOME/devkit-worktrees \
  devkit/kit/tests/codex-layout-verify.sh
  ```

Keep that verifier green before shipping binaries or docs so networking, tmux, and credential regressions are caught immediately.

`layout-apply` / `layout-validate` no longer require `-p`; as long as the YAML names its overlays/windows explicitly, you can omit the flag entirely. If you rely on the CLI default overlay (windows without `project:`) keep passing `-p <overlay>` so the validator knows which compose stack to assume.

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
