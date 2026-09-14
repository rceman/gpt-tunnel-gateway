#!/usr/bin/env python3
"""Run the cache-aware correctness lane for the assigned Go worktree."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import subprocess
import sys
from typing import Any


SAFE_DOC_PREFIXES = ("docs/",)
SAFE_DOC_NAMES = {"README.md", "CHANGELOG.md"}


def run(root: pathlib.Path, argv: list[str], *, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, cwd=root, check=check, text=True)


def default_base(root: pathlib.Path) -> str:
    configured = os.environ.get("GPT_TEST_BASE")
    if configured:
        return configured
    for candidate in ("origin/main", "main", "HEAD^"):
        result = subprocess.run(
            ["git", "rev-parse", "--verify", candidate],
            cwd=root,
            check=False,
            capture_output=True,
            text=True,
        )
        if result.returncode == 0:
            return candidate
    return "HEAD"


def changed_files(root: pathlib.Path, base: str) -> list[str]:
    result = subprocess.run(
        ["git", "diff", "--name-only", f"{base}...HEAD"],
        cwd=root,
        check=True,
        capture_output=True,
        text=True,
    )
    working = subprocess.run(
        ["git", "diff", "--name-only"],
        cwd=root,
        check=True,
        capture_output=True,
        text=True,
    )
    staged = subprocess.run(
        ["git", "diff", "--cached", "--name-only"],
        cwd=root,
        check=True,
        capture_output=True,
        text=True,
    )
    return sorted(
        {
            path
            for output in (result.stdout, working.stdout, staged.stdout)
            for path in output.splitlines()
            if path
        }
    )


def package_graph(root: pathlib.Path) -> list[dict[str, Any]]:
    result = subprocess.run(
        ["go", "list", "-json", "./..."],
        cwd=root,
        check=True,
        capture_output=True,
        text=True,
    )
    decoder = json.JSONDecoder()
    packages: list[dict[str, Any]] = []
    offset = 0
    while offset < len(result.stdout):
        while offset < len(result.stdout) and result.stdout[offset].isspace():
            offset += 1
        if offset == len(result.stdout):
            break
        value, end = decoder.raw_decode(result.stdout, offset)
        packages.append(value)
        offset = end
    return packages


def affected_packages(root: pathlib.Path, files: list[str]) -> tuple[list[str], str | None]:
    if not files:
        return [], "no changed files"
    go_files: list[str] = []
    for path in files:
        normalized = path.replace(os.sep, "/")
        if normalized in {"go.mod", "go.sum", "vendor/modules.txt"}:
            return [], f"dependency manifest changed: {normalized}"
        if normalized.endswith(".go"):
            go_files.append(normalized)
            continue
        if normalized.startswith(SAFE_DOC_PREFIXES) or normalized in SAFE_DOC_NAMES:
            continue
        return [], f"unknown test impact: {normalized}"
    if not go_files:
        return [], "documentation-only change"

    packages = package_graph(root)
    by_dir: dict[pathlib.Path, str] = {}
    for package in packages:
        directory = pathlib.Path(package["Dir"]).resolve()
        by_dir[directory] = package["ImportPath"]

    directly_changed: set[str] = set()
    for path in go_files:
        directory = (root / path).parent.resolve()
        package = by_dir.get(directory)
        if package is None:
            return [], f"changed Go directory is not a package: {path}"
        directly_changed.add(package)

    reverse: dict[str, set[str]] = {package["ImportPath"]: set() for package in packages}
    for package in packages:
        for dependency in package.get("Deps", []):
            if dependency in reverse:
                reverse[dependency].add(package["ImportPath"])

    selected = set(directly_changed)
    queue = list(directly_changed)
    while queue:
        dependency = queue.pop()
        for dependent in reverse[dependency]:
            if dependent not in selected:
                selected.add(dependent)
                queue.append(dependent)
    return sorted(selected), None


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=pathlib.Path, default=pathlib.Path.cwd())
    parser.add_argument("--base", help="Git base for committed affected-file comparison (default: origin/main or main)")
    parser.add_argument("--all", action="store_true", help="run all packages using Go's native cache")
    parser.add_argument("--affected", action="store_true", help="select changed packages and their dependents (the default)")
    args = parser.parse_args()
    root = args.root.resolve()
    base = args.base or default_base(root)

    if args.all:
        packages = ["./..."]
        reason = "explicit all-packages cache-aware run"
    else:
        files = changed_files(root, base)
        packages, reason = affected_packages(root, files)
        if reason is None:
            reason = f"affected changes from {base}"
        if reason is not None and reason.startswith("unknown test impact"):
            packages = ["./..."]
        elif reason is not None and reason.startswith("dependency manifest changed"):
            packages = ["./..."]
        if not packages:
            print(json.dumps({"status": "skipped", "reason": reason}, sort_keys=True))
            return 0

    print(json.dumps({"status": "running", "packages": packages, "reason": reason}, sort_keys=True))
    result = run(root, ["go", "test", *packages], check=False)
    if result.returncode != 0:
        return result.returncode
    return 0


if __name__ == "__main__":
    sys.exit(main())
