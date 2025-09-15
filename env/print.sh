#!/usr/bin/env bash
# Print effective environment variables with redaction.
set -euo pipefail

pattern='(SECRET|TOKEN|PASSWORD|PASS|KEY|ACCESS|PRIVATE|CREDENTIAL|API)'

env | sort | while IFS='=' read -r k v; do
  if [[ "$k" =~ $pattern ]]; then
    echo "$k=***redacted***"
  else
    echo "$k=$v"
  fi
done

