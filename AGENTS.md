Devkit Agent Notes

Principles
- Single path: use `kit/scripts/devkit` as the canonical entrypoint. It execs the compiled CLI at `kit/bin/devctl`.
- No fallbacks: wrappers must not silently fall back to alternative scripts or binaries. If the binary is missing, fail loudly with a clear message and instructions to build it.
- Minimal wrappers: keep shell wrappers as thin exec shims only; no hidden logic.

Canonical flow
- Build once: `make -C devkit/cli/devctl build` (outputs `devkit/kit/bin/devctl`).
- Run: `devkit/kit/scripts/devkit …` (this calls the binary). Directly invoking the binary is allowed for power users but the wrapper path is the supported interface.

Break-glass behavior
- If `kit/bin/devctl` is missing or not executable, the wrapper must exit non‑zero and print build instructions. Do not add alternative code paths.

Why no fallbacks?
- Multiple code paths cause confusion and mask failures. A single, enforced path ensures errors are obvious and fixes are straightforward.

