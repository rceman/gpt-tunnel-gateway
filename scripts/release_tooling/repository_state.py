from __future__ import annotations

from pathlib import Path

from .foundation import ReleaseError, StatusRecord, run_git


def status_records(repo: Path) -> list[StatusRecord]:
    result = run_git(repo, "status", "--porcelain=v1", "-z", "--untracked-files=all")
    tokens = result.stdout.split("\0")
    records: list[StatusRecord] = []
    index = 0
    while index < len(tokens):
        raw = tokens[index]
        index += 1
        if not raw:
            continue
        if len(raw) < 4:
            raise ReleaseError(f"unexpected git status record: {raw!r}")
        status = raw[:2]
        path = raw[3:]
        paths = [path]
        if "R" in status or "C" in status:
            if index >= len(tokens) or not tokens[index]:
                raise ReleaseError(f"rename/copy status is missing its second path: {raw!r}")
            paths.append(tokens[index])
            index += 1
        records.append(StatusRecord(status, tuple(paths)))
    return records


def status_paths(repo: Path) -> set[str]:
    paths: set[str] = set()
    for record in status_records(repo):
        paths.update(record.paths)
    return paths


def ensure_clean(repo: Path) -> None:
    paths = status_paths(repo)
    if paths:
        raise ReleaseError("working tree must be clean: " + ", ".join(sorted(paths)))


def current_head(repo: Path) -> str:
    return run_git(repo, "rev-parse", "HEAD").stdout.strip()


def tag_name(config: dict[str, Any], version: str) -> str:
    prefix = config.get("tag_prefix", "v")
    if not isinstance(prefix, str) or not prefix or any(char in prefix for char in " /\\\n\r"):
        raise ReleaseError("tag_prefix must be a non-empty ref-safe string")
    return prefix + version


def tag_exists(repo: Path, tag: str) -> bool:
    return run_git(repo, "rev-parse", "--verify", "--quiet", f"refs/tags/{tag}", check=False).returncode == 0


def lifecycle_message(config: dict[str, Any], version: str) -> str:
    message_template = config.get("release_commit_message", "chore(release): v{version}")
    if not isinstance(message_template, str) or "{version}" not in message_template:
        raise ReleaseError("release_commit_message must contain {version}")
    return message_template.format(version=version)
