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
