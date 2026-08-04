#!/usr/bin/env python3
"""Validate byte-identical canonical release and CI tools for a project."""
from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path


REQUIRED_RELEASE_COMMANDS = {
    "check",
    "check-source",
    "check-release-ready",
    "check-tag-ready",
    "prepare",
    "commit",
    "tag",
    "verify-tag",
}
REQUIRED_CI_OPTIONS = {"--sha", "--sha-from-git", "--policy", "--wait", "--format"}


def fail(message: str) -> int:
    print(f"ERROR: {message}", file=sys.stderr)
    return 1


def regular(path: Path, label: str) -> str | None:
    if path.is_symlink() or not path.is_file():
        return f"{label} must be a regular file: {path}"
    return None


def run_help(path: Path, label: str) -> tuple[str | None, str]:
    try:
        result = subprocess.run(
            [sys.executable, str(path), "--help"],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )
    except OSError as exc:
        return f"{label} --help could not execute: {exc}", ""
    if result.returncode != 0:
        return f"{label} --help failed: {result.stderr.strip() or 'unknown error'}", result.stdout
    return None, result.stdout


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release-script", required=True, type=Path)
    parser.add_argument("--ci-script", required=True, type=Path)
    parser.add_argument(
        "--canonical-script",
        type=Path,
        default=Path(__file__).resolve().with_name("release.py"),
    )
    args = parser.parse_args(argv)

    release_argument = args.release_script
    ci_argument = args.ci_script
    canonical_argument = args.canonical_script
    for label, path in (
        ("release script", release_argument),
        ("CI script", ci_argument),
        ("canonical release script", canonical_argument),
    ):
        error = regular(path, label)
        if error:
            return fail(error)

    release_script = release_argument.resolve()
    ci_script = ci_argument.resolve()
    canonical_script = canonical_argument.resolve()
    canonical_ci = canonical_script.with_name("check-github-ci.py")
    error = regular(canonical_ci, "canonical CI script")
    if error:
        return fail(error)
    for label, path in (("resolved release script", release_script), ("resolved CI script", ci_script)):
        error = regular(path, label)
        if error:
            return fail(error)
    try:
        if release_script.read_bytes() != canonical_script.read_bytes():
            return fail("project release script is not byte-identical to the planner canonical script")
        if ci_script.read_bytes() != canonical_ci.read_bytes():
            return fail("project CI script is not byte-identical to the planner canonical script")
    except OSError as exc:
        return fail(f"cannot read canonical tools: {exc}")

    error, release_help = run_help(release_script, "release script")
    if error:
        return fail(error)
    if not all(command in release_help for command in REQUIRED_RELEASE_COMMANDS):
        return fail("release script help does not advertise every canonical lifecycle command")
    error, ci_help = run_help(ci_script, "CI script")
    if error:
        return fail(error)
    if not all(option in ci_help for option in REQUIRED_CI_OPTIONS):
        return fail("CI script help does not advertise the canonical SHA and policy options")
    print(f"PASS: release and CI tools conform to {canonical_script}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
