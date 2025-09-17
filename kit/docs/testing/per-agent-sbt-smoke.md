# Per-Agent SBT Cache Smoke Test

Use this test to confirm that each dev-agent container writes SBT state to its
own anchored home and that `/home/dev/.sbt` resolves to that location.

```bash
./devkit/kit/tests/per-agent-sbt/run-smoke.sh
```

The script:

1. Generates a unique compose project name and temporary override so `tinyproxy`
   and `dns` containers do not collide with other stacks.
2. Starts the codex overlay with two dev-agent instances.
3. For each agent index:
   - Anchors `/workspace/.devhome` to `/workspace/.devhomes/$(hostname)`
     (mirroring the CLI anchor plan).
   - Symlinks `/home/dev/.sbt` into the agent-local target.
   - Runs `sbt about` (non-interactive) to populate the boot cache.
   - Verifies `readlink -f /home/dev/.sbt` matches the expected path and the
     `boot/sbt.boot.lock` file exists.
4. Tears down all containers, volumes, and networks.

> **Tip:** The first run may download SBT launcher artifacts. Subsequent runs
> will be faster thanks to the shared Ivy and coursier caches.

If the script fails, check the stderr output for missing lock files or proxy
issues and rerun after correcting the environment. The script always attempts to
clean up the temporary stack via a `trap`.
