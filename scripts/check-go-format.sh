#!/usr/bin/env bash
set -Eeuo pipefail

base="${1:-HEAD}"
mapfile -t files < <(
	{
		git diff --name-only --diff-filter=ACMR "$base" -- '*.go'
		git ls-files --others --exclude-standard -- '*.go'
	} | sort -u
)

if ((${#files[@]} == 0)); then
	exit 0
fi

exec go run ./cmd/gofmt-struct --check "${files[@]}"
