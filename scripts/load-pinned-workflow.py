#!/usr/bin/env python3
"""Verify and retrieve the workflow declared by .gpt-workflow.lock."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import sys
from pathlib import Path, PurePosixPath
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


MAX_DOCUMENT_BYTES = 2 * 1024 * 1024
SHA_RE = re.compile(r"^[0-9a-f]{64}$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
REPOSITORY_RE = re.compile(r"^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)$")


class CanonicalToolingGap(ValueError):
    pass


def unique_pairs(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise CanonicalToolingGap(f"duplicate lock field: {key}")
        result[key] = value
    return result


def reject_constant(value: str) -> None:
    raise CanonicalToolingGap(f"non-finite JSON value is not allowed: {value}")


def read_lock(path: Path) -> tuple[dict[str, object], str]:
    if path.is_symlink() or not path.is_file():
        raise CanonicalToolingGap("workflow lock is not a regular file")
    raw = path.read_bytes()
    try:
        lock = json.loads(raw.decode("utf-8"), object_pairs_hook=unique_pairs, parse_constant=reject_constant)
    except (UnicodeDecodeError, json.JSONDecodeError, CanonicalToolingGap) as exc:
        raise CanonicalToolingGap(f"invalid workflow lock: {exc}") from exc
    if not isinstance(lock, dict):
        raise CanonicalToolingGap("workflow lock must be a JSON object")
    allowed = {"schema_version", "repository", "version", "commit", "document", "sha256", "execution_mode", "installed_at"}
    unknown = set(lock) - allowed
    if unknown:
        raise CanonicalToolingGap(f"unknown workflow lock fields: {sorted(unknown)}")
    required = {"schema_version", "repository", "commit", "document", "sha256"}
    missing = required - set(lock)
    if missing:
        raise CanonicalToolingGap(f"missing workflow lock fields: {sorted(missing)}")
    if lock["schema_version"] != 2:
        raise CanonicalToolingGap("workflow lock schema_version must be 2")
    repository = lock["repository"]
    commit = lock["commit"]
    document = lock["document"]
    digest = lock["sha256"]
    if not isinstance(repository, str) or REPOSITORY_RE.fullmatch(repository) is None:
        raise CanonicalToolingGap("workflow lock repository must be an HTTPS GitHub repository")
    if not isinstance(commit, str) or COMMIT_RE.fullmatch(commit) is None:
        raise CanonicalToolingGap("workflow lock commit must be a lowercase 40-character SHA")
    if not isinstance(document, str) or not document or "\\" in document:
        raise CanonicalToolingGap("workflow lock document must be a relative POSIX path")
    document_path = PurePosixPath(document)
    if document_path.is_absolute() or ".." in document_path.parts or "." in document_path.parts:
        raise CanonicalToolingGap("workflow lock document must not escape its root")
    if not isinstance(digest, str) or SHA_RE.fullmatch(digest) is None:
        raise CanonicalToolingGap("workflow lock sha256 must be a lowercase SHA-256 digest")
    return lock, hashlib.sha256(raw).hexdigest()


def retrieve(lock: dict[str, object], lock_sha256: str) -> dict[str, object]:
    repository = str(lock["repository"])
    owner_repo = REPOSITORY_RE.fullmatch(repository)
    assert owner_repo is not None
    commit = str(lock["commit"])
    document = str(lock["document"])
    url = f"https://raw.githubusercontent.com/{owner_repo.group(1)}/{commit}/{document}"
    request = Request(url, headers={"Accept": "text/plain", "User-Agent": "gpt-tunnel-gateway-workflow-loader"})
    try:
        with urlopen(request, timeout=30) as response:
            content = response.read(MAX_DOCUMENT_BYTES + 1)
    except HTTPError as exc:
        state = "not_found" if exc.code == 404 else "unavailable"
        raise CanonicalToolingGap(f"BLOCKED_CANONICAL_TOOLING_GAP: {state} retrieving pinned workflow") from exc
    except (URLError, OSError, TimeoutError) as exc:
        raise CanonicalToolingGap(f"BLOCKED_CANONICAL_TOOLING_GAP: unavailable retrieving pinned workflow") from exc
    if len(content) > MAX_DOCUMENT_BYTES:
        raise CanonicalToolingGap("BLOCKED_CANONICAL_TOOLING_GAP: pinned workflow exceeds retrieval bound")
    digest = hashlib.sha256(content).hexdigest()
    if digest != lock["sha256"]:
        raise CanonicalToolingGap("BLOCKED_CANONICAL_TOOLING_GAP: pinned workflow digest mismatch")
    return {
        "status": "READY",
        "repository": repository,
        "commit": commit,
        "document": document,
        "source_url": url,
        "bytes": len(content),
        "sha256": digest,
        "lock_sha256": lock_sha256,
        "provenance": "workflow lock exact repository/commit/document and content digest",
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--lock", default=".gpt-workflow.lock")
    args = parser.parse_args(argv)
    try:
        lock, lock_sha256 = read_lock(Path(args.lock).resolve())
        print(json.dumps(retrieve(lock, lock_sha256), sort_keys=True))
        return 0
    except (OSError, CanonicalToolingGap) as exc:
        print(json.dumps({"status": "BLOCKED_CANONICAL_TOOLING_GAP", "error": str(exc)}, sort_keys=True))
        return 4


if __name__ == "__main__":
    sys.exit(main())
