#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 0 ]]; then
  printf 'test-full.sh does not accept test-selection arguments\n' >&2
  exit 2
fi

exec python3 scripts/test-full.py
