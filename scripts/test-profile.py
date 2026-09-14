#!/usr/bin/env python3
"""Run the uncached correctness corpus and report package/test durations."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import platform
import subprocess
import sys
import time
from typing import Any


def environment(root: pathlib.Path) -> dict[str, Any]:
    go_version = subprocess.run(["go", "version"], cwd=root, capture_output=True, text=True, check=True)
    return {
        "go_version": go_version.stdout.strip(),
        "goos": os.environ.get("GOOS", platform.system().lower()),
        "goarch": os.environ.get("GOARCH", platform.machine()),
        "gomaxprocs": os.environ.get("GOMAXPROCS", ""),
        "cpu_count": os.cpu_count(),
        "platform": platform.platform(),
        "python": platform.python_version(),
    }


def parse_events(output: str) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    packages: list[dict[str, Any]] = []
    tests: list[dict[str, Any]] = []
    for line in output.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("Action") != "pass" or not isinstance(event.get("Elapsed"), (int, float)):
            continue
        item = {
            "package": event.get("Package", ""),
            "test": event.get("Test", ""),
            "elapsed_seconds": event["Elapsed"],
        }
        if event.get("Test"):
            tests.append(item)
        elif event.get("Package"):
            packages.append(item)
    return packages, tests


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=pathlib.Path, default=pathlib.Path.cwd())
    parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args()
    root = args.root.resolve()
    command = ["go", "test", "./...", "-count=1", "-json"]
    started = time.monotonic()
    result = subprocess.run(command, cwd=root, capture_output=True, text=True)
    packages, tests = parse_events(result.stdout)
    report = {
        "kind": "correctness-profile",
        "status": "passed" if result.returncode == 0 else "failed",
        "exit_code": result.returncode,
        "command": command,
        "environment": environment(root),
        "wall_seconds": round(time.monotonic() - started, 3),
        "packages": packages,
        "tests": tests,
        "top_slow_packages": sorted(packages, key=lambda item: item["elapsed_seconds"], reverse=True)[:10],
        "top_slow_tests": sorted(tests, key=lambda item: item["elapsed_seconds"], reverse=True)[:20],
    }
    encoded = json.dumps(report, indent=2, sort_keys=True) + "\n"
    if args.output:
        args.output.write_text(encoded, encoding="utf-8")
    else:
        print(encoded, end="")
    if result.returncode != 0:
        sys.stderr.write(result.stdout)
        sys.stderr.write(result.stderr)
    return result.returncode


if __name__ == "__main__":
    sys.exit(main())
