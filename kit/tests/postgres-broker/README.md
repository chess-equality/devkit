# Postgres Broker Smoke Harness

This directory contains a compose-driven verification stack for the Postgres broker. The harness brings up a Docker-in-Docker daemon, the policy-enforcing broker, and a CLI client that talks to the broker’s Unix socket.

## What It Covers
- Happy path: create/start/stop/remove the approved `postgres:latest` container with required env vars via raw Docker API calls.
- Denial paths: attempts to launch an unapproved image or bind the host filesystem are rejected with `403 Forbidden`.
- Broker unit tests (`go test ./...`) cover request-filtering logic without bringing up Docker.

## Quick Start
```bash
# From repo root
./devkit/kit/tests/postgres-broker/run-smoke.sh
```

The script installs `curl`/`jq` inside the client container, pre-pulls the Postgres image, exercises the happy path, and asserts the rejection cases. Set `KEEP_STACK=1` to leave the compose stack running for manual debugging.

## Manual Exploration
```bash
# Bring up the stack manually
cd devkit/kit/tests/postgres-broker
BROKER_PROJECT=broker-debug
 docker compose -p ${BROKER_PROJECT} up --build -d

# Send Docker API traffic via curl inside the client container
 docker compose -p ${BROKER_PROJECT} exec client apk add --no-cache curl jq
 docker compose -p ${BROKER_PROJECT} exec client \
   curl --unix-socket /broker-run/postgres-broker.sock \
        -H 'Content-Type: application/json' \
        -d '{"Image":"postgres:latest","Env":["POSTGRES_PASSWORD=test"]}' \
        http://docker/v1.45/containers/create?name=apitest

# Tear down
 docker compose -p ${BROKER_PROJECT} down -v
```

## Next Steps
- Wire `run-smoke.sh` into CI.
- Expand negative coverage as new policy rules are added.
- Document troubleshooting (common failures, log inspection) alongside the harness output.
