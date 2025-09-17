#!/usr/bin/env bash
set -euo pipefail

THIS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="${THIS_DIR}/compose.yml"
PROJECT_NAME="${PROJECT_NAME:-postgres-broker-smoke}"
KEEP_STACK="${KEEP_STACK:-0}"
POSTGRES_TAG="${POSTGRES_TAG:-latest}"

log() {
  printf '[broker-smoke] %s\n' "$*"
}

cleanup() {
  if [[ "$KEEP_STACK" != "1" ]]; then
    log "Tearing down compose stack"
    docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" down -v >/dev/null 2>&1 || true
  else
    log "KEEP_STACK=1 set; leaving stack running"
  fi
}

trap cleanup EXIT

log "Starting compose stack ($PROJECT_NAME)"
docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" down -v >/dev/null 2>&1 || true
docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" up --build -d

wait_for_client() {
  local attempt=0
  local max_attempts=30
  until docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" exec -T client docker version >/dev/null 2>&1; do
    attempt=$((attempt+1))
    if (( attempt >= max_attempts )); then
      log "client docker daemon did not become ready"
      return 1
    fi
    sleep 2
  done
}

log "Waiting for broker/client to become ready"
wait_for_client

# Pre-pull the allowed image directly via the daemon to avoid streaming issues during policy tests.
log "Installing curl+jq inside client"
docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" exec -T client apk add --no-cache curl jq >/dev/null

JQ_PATH=$(docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" exec -T client which jq | tr -d '\r')
if [[ -z "$JQ_PATH" ]]; then
  log "Failed to locate jq in client container"
  exit 1
fi

log "Pre-pulling postgres image on daemon"
docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" exec -T docker docker pull "postgres:${POSTGRES_TAG}" >/dev/null

run_success() {
  local description="$1"
  shift
  log "EXPECT SUCCESS: ${description}"
  docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" exec -T client "$@"
}

run_failure() {
  local description="$1"
  shift
  log "EXPECT FAILURE: ${description}"
  if docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" exec -T client "$@"; then
    log "Command succeeded unexpectedly"
    return 1
  fi
  log "Failure confirmed (${description})"
}

# Execute broker HTTP requests from the client container
client_exec() {
  docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" exec -T client "$@"
}

broker_curl() {
  local cmd="$1"
  client_exec sh -ec "$cmd"
}

await_running() {
  local container_id="$1"
  local attempts=0
  local max_attempts=30
  while (( attempts < max_attempts )); do
    status=$(broker_curl "curl -s --unix-socket /broker-run/postgres-broker.sock \\
      http://docker/v1.45/containers/${container_id}/json | $JQ_PATH -r .State.Status")
    if [[ "$status" == "running" ]]; then
      return 0
    fi
    sleep 2
    attempts=$((attempts+1))
  done
  log "Container $container_id did not reach running state (last status: $status)"
  return 1
}

# Happy path via Docker HTTP API.
log "Creating postgres container via broker"
POSTGRES_ID=$(broker_curl "curl -s --unix-socket /broker-run/postgres-broker.sock \\
  -H 'Content-Type: application/json' \\
  -d '{\"Image\":\"postgres:${POSTGRES_TAG}\",\"Env\":[\"POSTGRES_PASSWORD=testpass\",\"POSTGRES_USER=testuser\"]}' \\
  http://docker/v1.45/containers/create?name=smoke-postgres | $JQ_PATH -r .Id")

if [[ -z "$POSTGRES_ID" || "$POSTGRES_ID" == "null" ]]; then
  log "Failed to create Postgres container"
  exit 1
fi

log "Starting container $POSTGRES_ID"
broker_curl "curl -s -o /tmp/start.out -w '%{http_code}' --unix-socket /broker-run/postgres-broker.sock \
  -X POST http://docker/v1.45/containers/${POSTGRES_ID}/start" | grep -q '^204$'

log "Verifying container running state"
await_running "$POSTGRES_ID"

log "Stopping container $POSTGRES_ID"
broker_curl "curl -s -o /tmp/stop.out -w '%{http_code}' --unix-socket /broker-run/postgres-broker.sock \
  -X POST http://docker/v1.45/containers/${POSTGRES_ID}/stop" | grep -q '^204$'

log "Removing container $POSTGRES_ID"
broker_curl "curl -s -o /tmp/rm.out -w '%{http_code}' --unix-socket /broker-run/postgres-broker.sock \
  -X DELETE 'http://docker/v1.45/containers/${POSTGRES_ID}?force=1'" | grep -q '^204$'

# Deny other images.
log "Ensuring redis image is blocked"
if broker_curl "curl -s -o /tmp/redis.out -w '%{http_code}' --unix-socket /broker-run/postgres-broker.sock \\
  -H 'Content-Type: application/json' \\
  -d '{\"Image\":\"redis:7\"}' \\
  http://docker/v1.45/containers/create?name=redis-test" | grep -q '^403$'; then
  log "Redis image correctly forbidden"
else
  log "Redis image was allowed unexpectedly"
  exit 1
fi

# Deny privileged mounts.
log "Ensuring host bind is blocked"
if broker_curl "curl -s -o /tmp/mount.out -w '%{http_code}' --unix-socket /broker-run/postgres-broker.sock \\
  -H 'Content-Type: application/json' \\
  -d '{\"Image\":\"postgres:${POSTGRES_TAG}\",\"HostConfig\":{\"Binds\":[\"/:/host:ro\"]}}' \\
  http://docker/v1.45/containers/create?name=forbidden-mount" | grep -q '^403$'; then
  log "Host bind correctly forbidden"
else
  log "Host bind was allowed unexpectedly"
  exit 1
fi

log "All smoke tests passed"
