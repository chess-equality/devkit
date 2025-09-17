Per-Agent SBT Smoke Test
=========================

This smoke test brings up a dedicated compose project, runs `sbt about` in two
agents, and verifies that each container's `/home/dev/.sbt` symlink resolves to
a unique directory under `/workspace/.devhomes/<hostname>/.sbt`.

How to run
----------

```bash
./devkit/kit/tests/per-agent-sbt/run-smoke.sh
```

What it does
------------

1. Generates a unique compose project name plus an override file that renames
   `tinyproxy` and `dns` containers so they do not collide with other stacks.
2. Starts the codex overlay with two `dev-agent` containers.
3. In each container:
   - Anchors `/workspace/.devhome` to `/workspace/.devhomes/$(hostname)`.
   - Symlinks `/home/dev/.sbt` to the agent-local `.sbt` directory.
   - Runs `sbt about` to populate the cache.
   - Asserts the symlink target matches the expected path and that
     `boot/sbt.boot.lock` exists.
4. Tears the stack down (containers, networks, and volumes) even on failure.

Requirements
------------

- Docker Engine 20.10+ (for `docker compose`).
- The codex overlay prerequisites (image build artifacts, host proxy access).
- Network egress to fetch SBT boot artifacts the first time the test runs.

Troubleshooting
---------------

- If SBT fails to download artifacts, ensure the proxy allowlist includes the
  requested domains or run the test again after opening the overlay normally so
  caches are primed.
- Stale containers from a previous run are cleaned up automatically; if the
  test is interrupted, run `docker compose -p per-agent-sbt-<suffix> down -v`
  with the project name printed in the script output.
