#!/usr/bin/env bash
set -Eeuo pipefail

base="${1:-HEAD}"
if ! resolved_base="$(git rev-parse --verify "$base")"; then
	echo "format check: unavailable comparison base: $base" >&2
	exit 1
fi
if ! git cat-file -e "${resolved_base}^{commit}"; then
	echo "format check: comparison base is not a commit: $base" >&2
	exit 1
fi

if ! diff_files="$(git diff --name-only --diff-filter=ACMR "$base" -- '*.go')"; then
	echo "format check: failed to enumerate changed files against: $base" >&2
	exit 1
fi
if ! untracked_files="$(git ls-files --others --exclude-standard -- '*.go')"; then
	echo "format check: failed to enumerate untracked files" >&2
	exit 1
fi

files=()
sorted_files="$(printf '%s\n%s\n' "$diff_files" "$untracked_files" | sort -u)"
while IFS= read -r path; do
	if [[ -n "$path" ]]; then
		files+=("$path")
	fi
done <<< "$sorted_files"

if ((${#files[@]} == 0)); then
	exit 0
fi

exec go run ./cmd/gofmt-struct --check "${files[@]}"
