#!/usr/bin/env bash
set -Eeuo pipefail
root="$(cd -- "$(dirname -- "$0")/.." && pwd)"
out="${1:-$root/dist}"
resolved="$(readlink -m -- "$out")"
case "$resolved" in
  /|"$root")
    echo "unsafe release output: $resolved" >&2
    exit 1
    ;;
esac
if [[ -L "$out" ]]; then
  echo "refusing symlink release output: $out" >&2
  exit 1
fi
parent="$(dirname -- "$resolved")"
name="$(basename -- "$resolved")"
mkdir -p "$parent"
stage="$(mktemp -d "$parent/.${name}.stage.XXXXXX")"
cleanup() { rm -rf -- "$stage"; }
trap cleanup EXIT
source_sha="$(git -C "$root" rev-parse --verify HEAD^{commit})"
if [[ ! "$source_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "source HEAD is not an exact Git commit" >&2
  exit 1
fi
if [[ -n "$(git -C "$root" status --porcelain --untracked-files=all)" ]]; then
  echo "release build requires a clean source worktree" >&2
  exit 1
fi
ldflags="-s -w -X github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts.BuildSourceRevision=$source_sha"
for cmd in gpt-tunnel gpt-tunnel-gatewayd gpt-tunnelctl; do
  go build -trimpath -ldflags "$ldflags" -o "$stage/$cmd" "$root/cmd/$cmd"
done
(
  cd "$stage"
  sha256sum gpt-tunnel gpt-tunnel-gatewayd gpt-tunnelctl > SHA256SUMS
  sha256sum -c SHA256SUMS
)
if [[ -e "$resolved" ]]; then
  [[ -d "$resolved" && ! -L "$resolved" ]] || { echo "release output is not a normal directory: $resolved" >&2; exit 1; }
  while IFS= read -r existing; do
    case "$(basename -- "$existing")" in
      gpt-tunnel|gpt-tunnel-gatewayd|gpt-tunnelctl|SHA256SUMS) ;;
      *) echo "refusing to replace non-release artifact: $existing" >&2; exit 1 ;;
    esac
  done < <(find "$resolved" -mindepth 1 -maxdepth 1 -print)
  rm -rf -- "$resolved"
fi
mv -- "$stage" "$resolved"
trap - EXIT
