#!/usr/bin/env python3
"""Canonical dependency-free release lifecycle implementation.

The source lifecycle and publication lifecycle are deliberately separate:
``check-source`` validates an unreleased implementation state, while
``prepare``/``check-release-ready``/``commit``/``check-tag-ready``/``tag``
implement the owner-authorized publication path.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import stat
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from datetime import date, datetime, timezone
from pathlib import Path, PurePosixPath
from typing import Any, Iterable


SEMVER_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
    r"(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)
LIFECYCLE_MODES = {"implementation_unreleased", "release_publication"}


class ReleaseError(RuntimeError):
    pass


@dataclass(frozen=True)
class VersionFile:
    path: str
    kind: str
    optional: bool = False
    pointer: str | None = None
    table: str | None = None
    key: str | None = None
    pattern: str | None = None


@dataclass(frozen=True)
class StatusRecord:
    status: str
    paths: tuple[str, ...]


def _unique_pairs(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ReleaseError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def run_git(repo: Path, *args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(
        ["git", "-C", str(repo), *args],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if check and result.returncode != 0:
        message = result.stderr.strip() or result.stdout.strip() or "git command failed"
        raise ReleaseError(message)
    return result
