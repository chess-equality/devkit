#!/usr/bin/env bash
set -euo pipefail

# Simple heredoc lint focused on runtime scripts we migrated away from heredocs.
# Fails if heredocs are found in environment-setup paths or specific setup scripts.

paths=(
  "ouroboros-ide/scripts/environment-setup/**"
  "agent-worktrees/agent*/ouroboros-ide/scripts/environment-setup/**"
  "dumb-onion-hax/Codex_Environment_Setup.sh"
  "ouroboros-ide/scripts/configure_codex_credential_file.sh"
  "agent-worktrees/agent*/ouroboros-ide/scripts/configure_codex_credential_file.sh"
  "terraform/scripts/configure_codex_credential_file.sh"
)

pattern="<<[[:space:]]*'?EO[A-Z]+"

fail=0
for p in "${paths[@]}"; do
  # Use ripgrep to search for heredoc tokens in the path
  if rg -n -S --glob "$p" "$pattern" >/tmp/heredoc_hits.txt 2>/dev/null; then
    echo "Heredoc detected in disallowed path: $p" >&2
    cat /tmp/heredoc_hits.txt >&2 || true
    fail=1
  fi
done

rm -f /tmp/heredoc_hits.txt || true

if [[ $fail -ne 0 ]]; then
  echo "\nHeredoc lint failed." >&2
  exit 1
fi

echo "Heredoc lint passed for environment-setup scripts."
