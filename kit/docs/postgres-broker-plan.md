# Postgres Test Broker Plan

## Intent
- Provide Scala integration tests with a Docker endpoint that only permits the blessed Postgres image required for Liquibase and Skunk validation.
- Preserve the dual-network sandbox guarantees: agents stay on `dev-internal`, egress still traverses the proxy, and the broker never joins `dev-egress`.
- Keep test code unchanged; clients continue to speak the standard Docker API/CLI while the broker enforces policy.

## Constraints
- Direct access to the Docker daemon is root-equivalent; every call must be explicitly inspected and filtered.
- The devkit runs with proxy-regulated networking and may use rootless or user-namespace remapped Docker hosts.
- CI exercises must remain lightweight—we should not need the full Scala project to validate broker behavior.
- Agents already mount `/var/run/docker.sock`; the redirect to the broker must be transparent.

## Implementation Plan

### 1. Broker Service
- Implemented in `devkit/brokers/postgres-broker` as a small Go reverse proxy that inspects Docker API calls before forwarding them through the host socket.
- The service listens on a private Unix socket (`/broker-run/postgres-broker.sock`) and only allows the whitelisted image/tag and container options; all other API calls return 403.
- Read-only Docker API access is granted for metadata endpoints that Testcontainers expects (`/_ping`, `/info`, `/version`, `/images/json`, `/containers/json`, `GET /networks/<id>` and image digests). Any write operation outside of the Postgres lifecycle is still rejected.
- `POST /containers/prune`, `/networks/prune`, `/volumes/prune`, and `/images/prune` are answered with stubbed 200 responses so Testcontainers' fallback cleanup hooks do not error, while still avoiding destructive pruning on the host daemon.
- The compose overlay builds the image locally (`devkit/overlays/codex/compose.override.yml`) and mounts both the shared broker socket volume and `/var/run/docker.sock` from the host.
- The runtime container uses a distroless base image, runs with a read-only root filesystem, drops all Linux capabilities, enforces `no-new-privileges`, and allows image pulls only for the whitelisted Postgres tag so first-run suites can fetch the required image.
- On start the broker sets its unix socket to `0666`, then serves it from the dedicated `broker-run` volume so non-root dev agents can reach the proxy without opening up broader filesystem access.

### 2. Agent Wiring
- Codex and dev-all overlays mount the shared `broker-run` volume into each `dev-agent` container and export `DOCKER_HOST=unix:///broker-run/postgres-broker.sock` so existing tooling (Testcontainers, docker CLI) talks to the broker transparently.
- Those overlays also export `TESTCONTAINERS_RYUK_DISABLED=true` so Testcontainers skips Ryuk by default; Ryuk is blocked by the broker and leaving it enabled causes 403s when suites first touch Docker.
- Overlays may set `BROKER_ATTACH_NETWORKS` (for dev-all we default to `devkit-devall_dev-internal`) so the broker automatically connects any approved Postgres container to the internal network that the requesting agent uses. This keeps Testcontainers reachable even when the host bridge (`172.17.0.1`) is blocked in hardened profiles.
- The broker service only connects to the internal Docker socket and joins the `dev-internal` network; it has no path to `dev-egress`.

### 3. Daemon Hardening
- Prefer running the Docker daemon in rootless mode or with `--userns-remap` so a broker bypass still maps to an unprivileged UID on the host.
- Disable arbitrary image pulls or mirror only the approved Postgres tags to prevent unvetted workloads from appearing.

### 4. Testing Strategy
- Harness: `kit/tests/postgres-broker/` starts the broker, a DinD daemon, and a CLI client on the internal network.
- Smoke script: `run-smoke.sh` creates/starts/stops the blessed Postgres container via raw Docker API calls and exercises rejection paths (redis image, host bind).
- Add unit/contract tests around the broker's request filtering to assert the whitelist without needing Docker in the loop.
- Optional CI check: run the harness, confirm Postgres reaches ready state, then tear everything down.

**Status:** unit tests covering the policy layer live alongside the broker (`go test ./...`). `run-smoke.sh` runs end-to-end without impacting live sessions. CI wiring remains outstanding.

### 5. Maintenance & Operations
- Document how to update the approved Postgres versions and revoke access; keep credentials or API tokens in managed secrets.
- Emit structured logs for every request and summarize them for audit trails.
- Provide troubleshooting guidance (common error codes, broker log locations, steps to restart the rootless daemon).

## Ryuk and Cleanup Semantics
- Ryuk (Testcontainers' resource reaper) stays disabled because the broker only whitelists the Postgres image/tag and forbids the socket mount and `HostConfig` flags that Ryuk requires. Allowing it would hand tests a general-purpose Docker control plane, breaking the "Postgres only" contract.
- Developers wiring new overlays must either set `TESTCONTAINERS_RYUK_DISABLED=true` or ensure suites pass the same flag so the broker never sees the Ryuk image request.
- The broker answers the prune endpoints with inert 200s so Testcontainers tolerates the missing reaper without letting suites delete arbitrary host resources. Re-enabling Ryuk would force us to expose those destructive verbs again.
- Happy-path runs still clean up: every suite wraps `PostgreSQLContainer` in `Resource.make`, so `stop()` executes even when assertions fail.
- The risk is limited to hard crashes (e.g., JVM exit, container kill) where finalizers never run. Operators should remove orphaned Postgres containers via the broker socket or the host daemon in those cases; a dedicated broker-owned sweeper can be added later if manual cleanup becomes noisy.

## Hands-on Smoke Test (Manual)
- Run the scripted harness locally: `devkit/kit/tests/postgres-broker/run-smoke.sh` (respects `KEEP_STACK=1` for debugging).
- The script installs `curl/jq` inside the client container, pre-pulls the Postgres image, runs the happy path, and asserts denial cases.
- For ad-hoc experimentation, bring the compose stack up manually: `docker compose -f devkit/kit/tests/postgres-broker/compose.yml -p broker-smoke up -d` and send `curl --unix-socket /broker-run/postgres-broker.sock` requests from the client container.
- Tear down with `docker compose -f devkit/kit/tests/postgres-broker/compose.yml -p broker-smoke down -v`.

## Next Steps
- Harden the policy checks based on real Testcontainers traffic and capture container lifecycle coverage.
- Integrate the harness into CI and publish troubleshooting tips alongside the script outputs.
