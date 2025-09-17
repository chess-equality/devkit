Per-Agent SBT Base — Execution Plan

Problem
- Multiple dev-agent containers share `/home/dev/.sbt` via a named volume, so the launcher lock file is cross-owned and frequently unwritable for other agents.
- Agents that run as different UIDs or that arrive while another agent is mid-launch see `java.io.FileNotFoundException: .../.sbt/boot/sbt.boot.lock (Permission denied)` and cannot compile.

Goals
- Each agent (container) gets a stable, writable SBT global base directory that cannot be clobbered by peers.
- Preserve the shared Ivy and coursier caches so artifacts stay deduplicated.
- Avoid bespoke per-agent compose profiles; the default overlays should "just work" with the new layout.
- Keep the change obvious/documented so future agents know where SBT state lives.

Non-Goals
- Changing the Ivy or coursier cache topology.
- Altering project build tooling beyond pointing SBT at its new base.

Implementation Outline
1. Home Anchoring: Continue resolving each agent's HOME anchor to `/workspace/.devhomes/<hostname>` (or `/workspaces/dev/...` for `dev-all`). Ensure the anchor bootstrap script creates `$target/.sbt` and points `/home/dev/.sbt` at that per-agent directory.
2. Environment Export: Set `SBT_GLOBAL_BASE` to `${HOME}/.sbt` for all agent shells, both in the compose service definitions and in the command exports that `devctl` emits. This keeps SBT on the anchor without hard-coding hostnames.
3. Volume Cleanup: Drop the shared `sbt-cache` named volume mounts from the base compose file and overlays; they are no longer required once SBT writes under the anchored home.
4. Documentation: Update the devkit README (or relevant doc) to describe the per-agent SBT cache behavior and mention the `SBT_GLOBAL_BASE` override so operators know where to find state.
5. Verification: Spin up a fresh compose project (unique project name to avoid conflicts), scale to multiple agents, run `sbt about` (or similar) in each, and confirm that `/workspace/.devhomes/<hostname>/.sbt` contains the boot files while `/home/dev/.sbt` is a symlink to that directory. Snapshot the relevant `ls -ld` outputs in the test log.

Rollback Plan
- Re-add the `sbt-cache` named volume and remove the env var exports if the per-agent approach causes regressions. No data migration is required; the old shared cache directory can be restored from the volume.

