#!/usr/bin/env bash
# Load layered dotenv files into the current shell.
# Usage: source devkit/env/load.sh [ENV]
set -euo pipefail

_ENV="${1:-${NODE_ENV:-development}}"

_files=(
  ".env"
  ".env.shared"
  ".env.${_ENV}"
  ".env.local"
  ".env.${_ENV}.local"
)

_parse_line() {
  # Parses KEY=VALUE lines (supports quoted values and spaces)
  local line="$1"
  # strip export prefix if present
  line="${line#export }"
  # split on first '='
  local key="${line%%=*}"
  local val="${line#*=}"
  key="${key%% }"; key="${key## }"
  # remove optional quotes around value
  if [[ "$val" =~ ^\".*\"$ ]]; then
    val="${val:1:${#val}-2}"
  elif [[ "$val" =~ ^\'.*\'$ ]]; then
    val="${val:1:${#val}-2}"
  fi
  printf '%s\n' "$key=$val"
}

for f in "${_files[@]}"; do
  if [[ -f "$f" ]]; then
    while IFS= read -r raw || [[ -n "$raw" ]]; do
      [[ -z "$raw" ]] && continue
      [[ "$raw" =~ ^\# ]] && continue
      [[ "$raw" =~ ^[[:space:]]*$ ]] && continue
      kv=$(_parse_line "$raw")
      k="${kv%%=*}"; v="${kv#*=}"
      # only export if not already set in environment
      if [[ -z "${!k-}" ]]; then
        export "$k"="$v"
      fi
    done < "$f"
  fi
done

