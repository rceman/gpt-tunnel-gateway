#!/usr/bin/env bash
set -Eeuo pipefail

release_dir="${1:-}"
source_root="${2:-$(pwd)}"
if [[ -z "$release_dir" || ! -d "$release_dir" ]]; then
  echo "usage: upgrade-bootstrap.sh <verified-release-directory> [source-root]" >&2
  exit 2
fi
case "$(readlink -m -- "$release_dir")" in
  /|/tmp|/home|/home/*/git|"$(readlink -m -- "$source_root")")
    echo "refusing unsafe release directory" >&2
    exit 1
    ;;
esac
for name in gpt-tunnelctl gpt-tunnel-gatewayd gpt-tunnel; do
  test -f "$release_dir/$name" && test -x "$release_dir/$name"
done
(
  cd "$release_dir"
  sha256sum -c SHA256SUMS
  ! grep -Eq '  /' SHA256SUMS
)
test "$(git -C "$source_root" branch --show-current)" = main
cd -- "$source_root"
exec "$release_dir/gpt-tunnelctl" upgrade
