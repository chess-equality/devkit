# Runtime Testing Attempt Retrospective

## What Happened
- Implemented an initial `doctor-runtime` integration test that attempted to run the full devkit compose stack inside `go test`.
- The helper generated random Docker network subnets, but the test exited early and left proxy services (`tinyproxy`, `dns`) stopped, breaking egress for live containers.
- Detected when the user reported loss of network; investigated `docker network`/`docker ps` to confirm devkit agents were still running but proxies were missing.

## Remediation
- Restarted `devkit_dns` and `devkit_tinyproxy` in place (`docker compose ... up -d tinyproxy dns`) without touching the running dev agents.
- Documented cleanup commands and the temp networks created by the tests.

## Lessons Learned
- Full compose stacks are heavy and interfere with live environments when launched from tests.
- Tests must isolate Docker resources (project name, subnet) _and_ guarantee teardown even when they fail early.
- Before adopting runtime-config integration tests, build lightweight fixtures (e.g., minimal compose file) or mock services that do not touch shared overlays.

## Next Steps
1. Design a minimal container fixture dedicated to testing worktree commands, separate from production overlays.
2. Wrap test commands in stricter context timeouts and `defer` cleanups that run even after fatal errors.
3. Add pre-flight checks (`doctor-runtime`) to ensure proxies are healthy before and after tests, failing fast if they aren’t.

---

# Runtime Proxy Outage (2025-XX-XX)

## What Happened
- The new runtime integration harness exercised `layout-apply`, which invokes `CleanupSharedInfra` before starting containers.
- `CleanupSharedInfra` unconditionally removes the globally named proxy containers (`devkit_tinyproxy`, `devkit_dns`) regardless of the compose project.
- Our fixture reused the production compose files (same container names), so the test suite stopped the live proxy stack backing `devkit-ouro4`, severing outbound access for four long-running agents.

## Remediation
- Manually restarted proxies with `docker compose -p devkit-ouro4 -f kit/compose.yml -f kit/compose.dns.yml -f overlays/dev-all/compose.override.yml up -d tinyproxy dns`.
- Verified `devkit_tinyproxy` health and restored egress from an affected agent (`curl https://example.com`).

## Lessons Learned
- Isolation must account for container names, not just compose project names. Shared helper cleanup routines can still target global resources.
- Reusing production compose definitions in tests requires either unique container names or explicit guards to skip destructive steps.
- Documentation should capture the required guardrails before others attempt to opt into runtime tests.

## Follow-Up Actions
1. ✅ Introduced `DEVKIT_SKIP_SHARED_CLEANUP` so `CleanupSharedInfra` skips the global proxy teardown unless explicitly requested.
2. ✅ Runtime fixture exports the guard by default and the test kit now ships proxy stubs with compose-project-scoped container names.
3. Document the pitfall for maintainers and link this retrospective from the design index.
