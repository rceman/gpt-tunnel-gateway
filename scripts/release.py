#!/usr/bin/env python3
"""Small dependency-free release contract used by the pinned planner workflow."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path


SEMVER = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$")


class ReleaseError(RuntimeError):
    pass


def root_from_args(value: str | None) -> Path:
    root = Path(value).resolve() if value else Path(__file__).resolve().parents[1]
    if not (root / ".git").exists():
        raise ReleaseError(f"not a Git repository root: {root}")
    return root


def git(root: Path, *args: str, check: bool = True) -> str:
    result = subprocess.run(["git", "-C", str(root), *args], text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    if check and result.returncode != 0:
        raise ReleaseError(result.stderr.strip() or result.stdout.strip() or "Git command failed")
    return result.stdout.strip()


def config(root: Path, name: str) -> dict:
    try:
        value = json.loads((root / name).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ReleaseError(f"invalid release config: {exc}") from exc
    if value.get("schema_version") != 1 or not isinstance(value.get("version_files"), list):
        raise ReleaseError("release config schema is invalid")
    return value


def version(root: Path, cfg: dict) -> str:
    path = root / cfg["canonical_version_file"]
    value = path.read_text(encoding="utf-8").strip()
    if not SEMVER.fullmatch(value):
        raise ReleaseError(f"invalid semantic version: {value!r}")
    for entry in cfg["version_files"]:
        if entry.get("kind") != "plain":
            raise ReleaseError("this repository release contract supports plain version files only")
        other = (root / entry["path"]).read_text(encoding="utf-8").strip()
        if other != value:
            raise ReleaseError(f"version mismatch: {entry['path']}={other!r} != {value!r}")
    return value


def ensure_clean(root: Path) -> None:
    if git(root, "status", "--porcelain", "--untracked-files=all"):
        raise ReleaseError("worktree must be clean")


def check(root: Path, cfg: dict) -> None:
    current = version(root, cfg)
    changelog = root / cfg["changelog"]["path"]
    text = changelog.read_text(encoding="utf-8")
    if not re.search(rf"(?m)^## {re.escape(current)} — \d{{4}}-\d{{2}}-\d{{2}}$", text):
        raise ReleaseError(f"changelog has no release heading for {current}")
    print(f"PASS: version {current}")


def prepare(root: Path, cfg: dict, target: str) -> None:
    if not SEMVER.fullmatch(target):
        raise ReleaseError(f"invalid semantic version: {target!r}")
    ensure_clean(root)
    current = version(root, cfg)
    if current == target:
        raise ReleaseError(f"version is already {target}")
    (root / cfg["canonical_version_file"]).write_text(target + "\n", encoding="utf-8")
    changelog_cfg = cfg["changelog"]
    path = root / changelog_cfg["path"]
    text = path.read_text(encoding="utf-8")
    unreleased = changelog_cfg["unreleased_heading"]
    match = re.search(rf"(?m)^{re.escape(unreleased)}[ \t]*$", text)
    if not match:
        raise ReleaseError("changelog is missing the Unreleased heading")
    next_heading = re.search(r"(?m)^##\s+.+$", text[match.end():])
    section = text[match.end():match.end() + (next_heading.start() if next_heading else len(text))]
    if not section.strip():
        raise ReleaseError("Unreleased changelog section is empty")
    heading = changelog_cfg["release_heading"].format(version=target, date=datetime.now(timezone.utc).date().isoformat())
    path.write_text(text[:match.end()] + "\n\n" + heading + text[match.end():], encoding="utf-8")
    print(f"Prepared release {target}")


def commit(root: Path, cfg: dict) -> None:
    allowed = {cfg["canonical_version_file"], cfg["changelog"]["path"]}
    changed = {line[3:].strip().split(" -> ")[-1] for line in git(root, "status", "--porcelain").splitlines() if line}
    if changed != allowed:
        raise ReleaseError(f"release commit scope mismatch: {sorted(changed)}")
    git(root, "add", "--", *sorted(allowed))
    git(root, "commit", "-m", cfg["release_commit_message"].format(version=version(root, cfg)))


def tag(root: Path, cfg: dict) -> None:
    ensure_clean(root)
    name = cfg["tag_prefix"] + version(root, cfg)
    if git(root, "rev-parse", "--verify", "refs/tags/" + name, check=False):
        raise ReleaseError(f"tag already exists: {name}")
    git(root, "tag", "-a", name, "-m", name)
    print(f"Created annotated tag {name}")


def verify_tag(root: Path, cfg: dict, name: str) -> None:
    ensure_clean(root)
    if git(root, "cat-file", "-t", "refs/tags/" + name) != "tag":
        raise ReleaseError(f"tag is not annotated: {name}")
    if git(root, "rev-parse", name + "^{}") != git(root, "rev-parse", "HEAD"):
        raise ReleaseError(f"tag does not point to HEAD: {name}")
    if name != cfg["tag_prefix"] + version(root, cfg):
        raise ReleaseError("tag does not match VERSION")
    print(f"PASS: {name} matches HEAD")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo")
    parser.add_argument("--config", default="release-config.json")
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("check")
    prepare_parser = sub.add_parser("prepare")
    prepare_parser.add_argument("version")
    sub.add_parser("commit")
    sub.add_parser("tag")
    verify_parser = sub.add_parser("verify-tag")
    verify_parser.add_argument("tag")
    args = parser.parse_args(argv)
    try:
        root = root_from_args(args.repo)
        cfg = config(root, args.config)
        if args.command == "check":
            check(root, cfg)
        elif args.command == "prepare":
            prepare(root, cfg, args.version)
        elif args.command == "commit":
            commit(root, cfg)
        elif args.command == "tag":
            tag(root, cfg)
        else:
            verify_tag(root, cfg, args.tag)
    except (OSError, ReleaseError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
