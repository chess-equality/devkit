#!/usr/bin/env bash
# Initialize local env file from example.
set -euo pipefail

src=".env.example"
dst=".env.local"

if [[ ! -f "$src" ]]; then
  echo "No $src found in current directory." >&2
  exit 0
fi

if [[ -f "$dst" ]]; then
  echo "$dst already exists; not overwriting." >&2
  exit 0
fi

cp "$src" "$dst"
echo "Created $dst from $src."

