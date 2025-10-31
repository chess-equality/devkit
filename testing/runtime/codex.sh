#!/bin/sh
set -eu

case "${1:-}" in
  exec)
    printf 'ok\n'
    ;;
  --version|-V)
    echo "codex stub 0.0.0"
    ;;
  *)
    echo "codex stub"
    ;;
esac
