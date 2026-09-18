#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 0 ]]; then
  printf 'test-e2e.sh does not accept test-selection arguments\n' >&2
  exit 2
fi

exec go test -tags=livee2e -count=1 -run '^TestCandidate' ./cmd/gpt-tunnel-gatewayd
