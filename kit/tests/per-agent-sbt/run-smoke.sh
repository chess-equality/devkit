#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DEVKIT_ROOT="$(cd -- "$SCRIPT_DIR/../../.." && pwd)"

COMPOSE_BASE="$DEVKIT_ROOT/kit/compose.yml"
COMPOSE_DNS="$DEVKIT_ROOT/kit/compose.dns.yml"
COMPOSE_OVERLAY="$DEVKIT_ROOT/overlays/codex/compose.override.yml"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker CLI not found" >&2
  exit 1
fi
if ! docker compose version >/dev/null 2>&1; then
  echo "docker compose plugin not available" >&2
  exit 1
fi

PROJECT="per-agent-sbt-$(date +%s%N | tail -c 6)"
OVERRIDE_FILE="$(mktemp)"
cat <<EOT >"$OVERRIDE_FILE"
services:
  tinyproxy:
    container_name: ${PROJECT}_tinyproxy
  dns:
    container_name: ${PROJECT}_dns
EOT

cleanup() {
  set +e
  DEVKIT_INTERNAL_SUBNET="$DEVKIT_INTERNAL_SUBNET" DEVKIT_DNS_IP="$DEVKIT_DNS_IP" \
    docker compose -p "$PROJECT" -f "$COMPOSE_BASE" -f "$COMPOSE_DNS" -f "$COMPOSE_OVERLAY" -f "$OVERRIDE_FILE" down -v >/dev/null 2>&1
  rm -f "$OVERRIDE_FILE"
}
trap cleanup EXIT

echo "[per-agent-sbt] project: $PROJECT"
SUBNET_OCTET=$(( (RANDOM % 200) + 20 ))
DEVKIT_INTERNAL_SUBNET="172.28.${SUBNET_OCTET}.0/24"
DEVKIT_DNS_IP="172.28.${SUBNET_OCTET}.3"

DEVKIT_INTERNAL_SUBNET="$DEVKIT_INTERNAL_SUBNET" DEVKIT_DNS_IP="$DEVKIT_DNS_IP" docker compose -p "$PROJECT" \
  -f "$COMPOSE_BASE" \
  -f "$COMPOSE_DNS" \
  -f "$COMPOSE_OVERLAY" \
  -f "$OVERRIDE_FILE" \
  up -d --remove-orphans --scale dev-agent=2

target_check() {
  local index="$1"
  DEVKIT_INTERNAL_SUBNET="$DEVKIT_INTERNAL_SUBNET" DEVKIT_DNS_IP="$DEVKIT_DNS_IP" docker compose -p "$PROJECT" \
    -f "$COMPOSE_BASE" \
    -f "$COMPOSE_DNS" \
    -f "$COMPOSE_OVERLAY" \
    -f "$OVERRIDE_FILE" \
    exec -T --index "$index" dev-agent bash -lc '
set -euo pipefail
host=$(hostname)
target="/workspace/.devhomes/${host}"
mkdir -p "$target/.ssh" "$target/.cache" "$target/.config" "$target/.local" "$target/.sbt"
chmod 700 "$target/.ssh"
ln -sfn "$target" /workspace/.devhome
rm -rf /home/dev/.sbt
ln -sfn "$target/.sbt" /home/dev/.sbt
echo "[per-agent-sbt] agent ${host}: running sbt about" >&2
sbt about </dev/null >/tmp/sbt-about.log 2>&1 || { cat /tmp/sbt-about.log >&2; exit 1; }
actual=$(readlink -f /home/dev/.sbt || true)
expected="$target/.sbt"
if [[ "$actual" != "$expected" ]]; then
  echo "expected $expected but found $actual" >&2
  exit 1
fi
if [[ ! -f "$target/.sbt/boot/sbt.boot.lock" ]]; then
  echo "missing $target/.sbt/boot/sbt.boot.lock" >&2
  exit 1
fi
printenv SBT_GLOBAL_BASE >&2
'
}

for idx in 1 2; do
  echo "[per-agent-sbt] validating agent index $idx"
  target_check "$idx"
done

echo "[per-agent-sbt] success"
