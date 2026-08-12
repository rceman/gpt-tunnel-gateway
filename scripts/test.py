#!/usr/bin/env python3
"""Canonical repository test runner.

The full mode is the authoritative Train closeout entrypoint.  It deliberately
delegates test scheduling to ``go test``: ``-p`` schedules independent Go
packages and ``-parallel`` only schedules tests that explicitly opt into
testing.T parallelism.  No test-name regex sharding is used.
"""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import subprocess
import sys
import time


ROOT = Path(__file__).resolve().parents[1]


def positive_int(value: str) -> int:
    try:
        parsed = int(value)
    except ValueError as exc:
        raise argparse.ArgumentTypeError("must be an integer") from exc
    if parsed < 1:
        raise argparse.ArgumentTypeError("must be at least 1")
    return parsed


def default_jobs() -> int:
    configured = os.environ.get("GPT_TUNNEL_TEST_JOBS")
    if configured:
        return positive_int(configured)
    return max(1, min(os.cpu_count() or 1, 4))


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Run repository Go tests.")
    parser.add_argument(
        "mode",
        nargs="?",
        choices=("full", "service"),
        default="full",
        help="test scope (default: full)",
    )
    parser.add_argument(
        "--jobs",
        type=positive_int,
        default=default_jobs(),
        help="maximum independent Go packages scheduled by go test -p",
    )
    parser.add_argument(
        "--parallel",
        type=positive_int,
        default=None,
        help="maximum explicitly parallel Go tests passed to -parallel",
    )
    parser.add_argument(
        "--race",
        action="store_true",
        help="enable the Go race detector",
    )
    return parser


def command(args: argparse.Namespace) -> list[str]:
    packages = "./..." if args.mode == "full" else "./internal/service"
    parallel = args.parallel if args.parallel is not None else args.jobs
    result = ["go", "test", "-count=1", f"-p={args.jobs}", f"-parallel={parallel}"]
    if args.race:
        result.append("-race")
    result.append(packages)
    return result


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    cmd = command(args)
    started = time.monotonic()
    completed = subprocess.run(cmd, cwd=ROOT)
    elapsed = time.monotonic() - started
    print(f"test mode={args.mode} elapsed={elapsed:.3f}s exit={completed.returncode}")
    return completed.returncode


if __name__ == "__main__":
    raise SystemExit(main())
