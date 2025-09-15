#!/usr/bin/env bash
# Validate required environment variables are set.
# Usage: ./devkit/env/validate.sh [path/to/env.required]
set -euo pipefail

req_file="${1:-env.required}"

if [[ ! -f "$req_file" ]]; then
  echo "No required env file found at $req_file (skipping)." >&2
  exit 0
fi

missing=()
while IFS= read -r key || [[ -n "$key" ]]; do
  key="${key%%#*}"; key="${key%% }"; key="${key## }"
  [[ -z "$key" ]] && continue
  # Support alternatives with '|', e.g., A|B passes if either is present
  if [[ "$key" == *"|"* ]]; then
    IFS='|' read -r -a alts <<< "$key"
    ok=false
    for alt in "${alts[@]}"; do
      if [[ -n "${!alt-}" ]]; then ok=true; break; fi
    done
    if [[ "$ok" != true ]]; then
      missing+=("$key")
    fi
  else
    if [[ -z "${!key-}" ]]; then
      missing+=("$key")
    fi
  fi
done < "$req_file"

if (( ${#missing[@]} > 0 )); then
  echo "Missing required environment variables:" >&2
  for k in "${missing[@]}"; do echo "- $k" >&2; done
  exit 1
fi

echo "All required env vars present."
