#!/usr/bin/env bash
set -euo pipefail

LAYOUT_FILE=${LAYOUT_FILE:-devkit/kit/examples/orchestration-codex8-worktrees.yaml}
PROJECT=${PROJECT:-codex}
COMPOSE_NAME=${COMPOSE_NAME:-devkit-codex8}
SESSION_NAME=${SESSION_NAME:-devkit_codex8}

if [[ "${DEVKIT_ENABLE_RUNTIME_CONFIG:-}" != "1" ]]; then
  echo "DEVKIT_ENABLE_RUNTIME_CONFIG=1 is required" >&2
  exit 1
fi
if [[ -z "${DEVKIT_WORKTREE_ROOT:-}" ]]; then
  echo "DEVKIT_WORKTREE_ROOT must be set" >&2
  exit 1
fi

ROOT="$(cd "$(dirname "$0")/../.." && pwd -P)"
pushd "$ROOT" >/dev/null

export DEVKIT_DEBUG=${DEVKIT_DEBUG:-0}
unset DEVKIT_NO_TMUX || true

tmux kill-session -t "$SESSION_NAME" 2>/dev/null || true

echo "[verify] applying layout..."
DEVKIT_DEBUG=$DEVKIT_DEBUG scripts/devkit -p "$PROJECT" layout-apply --tmux --file "$LAYOUT_FILE"

echo "[verify] checking tmux session..."
tmux has-session -t "$SESSION_NAME"
if ! tmux list-windows -t "$SESSION_NAME" | grep -q "codex-8"; then
  echo "tmux session missing codex windows" >&2
  exit 2
fi

echo "[verify] running codex smoke..."
docker exec "$COMPOSE_NAME-dev-agent-1" bash -lc 'cd /workspaces/dev/ouroboros-ide && rm -f /workspaces/dev/.devhome/.codex/auth.json && codexw exec --skip-git-repo-check "reply with: ok"' | grep -q '^ok$'

echo "[verify] git pull..."
docker exec "$COMPOSE_NAME-dev-agent-1" bash -lc 'cd /workspaces/dev/ouroboros-ide && git fetch origin && git pull --ff-only'

echo "[verify] cleaning up..."
tmux kill-session -t "$SESSION_NAME"
COMPOSE_PROJECT_NAME="$COMPOSE_NAME" DEVKIT_WORKTREE_ROOT="$DEVKIT_WORKTREE_ROOT" DEVKIT_WORKTREE_CONTAINER_ROOT=/worktrees scripts/devkit -p "$PROJECT" down

echo "[verify] codex layout OK"

popd >/dev/null
