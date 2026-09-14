#!/usr/bin/env python3
"""Run explicit live local-code performance checks with cold/warm evidence."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import platform
import subprocess
import sys
import tempfile
import time
from typing import Any


COMMAND = [
    "go",
    "test",
    "-tags=liveperformance",
    "./internal/service",
    "./internal/mcp",
    "-run=^Test(LocalCodeInspectionPerformanceProfile|PublicCodeLatencyPerformanceProfile)$",
    "-count=1",
    "-json",
]


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
        (tests if event.get("Test") else packages).append(item)
    return packages, tests


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


def run_once(root: pathlib.Path, cache: pathlib.Path) -> dict[str, Any]:
    started = time.monotonic()
    result = subprocess.run(
        COMMAND,
        cwd=root,
        env={**os.environ, "GOCACHE": str(cache)},
        capture_output=True,
        text=True,
    )
    packages, tests = parse_events(result.stdout)
    return {
        "status": "passed" if result.returncode == 0 else "failed",
        "exit_code": result.returncode,
        "wall_seconds": round(time.monotonic() - started, 3),
        "packages": packages,
        "tests": tests,
        "top_slow_tests": sorted(tests, key=lambda item: item["elapsed_seconds"], reverse=True)[:20],
        "top_slow_packages": sorted(packages, key=lambda item: item["elapsed_seconds"], reverse=True)[:10],
        "stderr": result.stderr[-4000:],
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=pathlib.Path, default=pathlib.Path.cwd())
    parser.add_argument("--output", type=pathlib.Path)
    parser.add_argument("--warm-budget-seconds", type=float, default=10.0)
    args = parser.parse_args()
    root = args.root.resolve()
    with tempfile.TemporaryDirectory(prefix="gpt-tunnel-performance-") as cache_dir:
        cache = pathlib.Path(cache_dir)
        cold = run_once(root, cache)
        warm = run_once(root, cache)
    report = {
        "kind": "live-performance-e2e",
        "command": COMMAND,
        "environment": environment(root),
        "cold": cold,
        "warm": warm,
        "warm_budget_seconds": args.warm_budget_seconds,
        "warm_budget_passed": warm["status"] == "passed" and warm["wall_seconds"] <= args.warm_budget_seconds,
    }
    encoded = json.dumps(report, indent=2, sort_keys=True) + "\n"
    if args.output:
        args.output.write_text(encoded, encoding="utf-8")
    else:
        print(encoded, end="")
    if cold["status"] != "passed" or warm["status"] != "passed":
        return 1
    if not report["warm_budget_passed"]:
        print("live performance warm budget exceeded", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
