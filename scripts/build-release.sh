#!/usr/bin/env bash
set -Eeuo pipefail
root="$(cd -- "$(dirname -- "$0")/.." && pwd)"
out="${1:-$root/dist}"
mkdir -p "$out"
for cmd in gpt-tunnel gpt-tunnel-gatewayd gpt-tunnelctl; do
  go build -trimpath -ldflags "-s -w" -o "$out/$cmd" "$root/cmd/$cmd"
done
sha256sum "$out"/* > "$out/SHA256SUMS"
