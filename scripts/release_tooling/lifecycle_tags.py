from __future__ import annotations

from pathlib import Path
from typing import Any

from .foundation import ReleaseError, run_git
from .lifecycle_checks import allowed_release_paths, release_commit_paths, validate_release_ready_state
from .repository_state import current_head, ensure_clean, lifecycle_message, tag_exists, tag_name
from .version_files import configured_versions


def validate_tag_ready_state(repo: Path, config: dict[str, Any]) -> tuple[str, str]:
    ensure_clean(repo)
    version, _ = validate_release_ready_state(repo, config)
    tag = tag_name(config, version)
    if tag_exists(repo, tag):
        raise ReleaseError(f"tag already exists: {tag}")
    subject = run_git(repo, "show", "-s", "--format=%s", "HEAD").stdout.strip()
    if subject != lifecycle_message(config, version):
        raise ReleaseError("HEAD is not the configured release commit")
    changed = release_commit_paths(repo)
    if not changed or not changed <= allowed_release_paths(config):
        raise ReleaseError("HEAD release commit does not contain only configured release paths")
    return version, tag


def command_tag_ready(repo: Path, config: dict[str, Any]) -> None:
    version, tag = validate_tag_ready_state(repo, config)
    print(f"PASS: tag-ready {tag} at {current_head(repo)} for v{version}")


def command_tag(repo: Path, config: dict[str, Any]) -> None:
    version, tag = validate_tag_ready_state(repo, config)
    run_git(repo, "tag", "-a", tag, "-m", tag)
    print(f"Created annotated tag {tag} at {current_head(repo)} for v{version}")
    print(f"Push explicitly with: git push origin {tag}")


def command_verify_tag(repo: Path, config: dict[str, Any], tag: str) -> None:
    ensure_clean(repo)
    version, _ = configured_versions(repo, config)
    expected = tag_name(config, version)
    if tag != expected:
        raise ReleaseError(f"tag/version mismatch: tag={tag}, expected={expected}")
    if not tag_exists(repo, tag):
        raise ReleaseError(f"tag does not exist: {tag}")
    object_type = run_git(repo, "cat-file", "-t", f"refs/tags/{tag}").stdout.strip()
    if object_type != "tag":
        raise ReleaseError(f"tag {tag} is lightweight; an annotated tag is required")
    tag_commit = run_git(repo, "rev-parse", f"refs/tags/{tag}^{{commit}}").stdout.strip()
    head = current_head(repo)
    if tag_commit != head:
        raise ReleaseError(f"tag {tag} resolves to {tag_commit}, but HEAD is {head}")
    validate_release_ready_state(repo, config)
    print(f"PASS: annotated {tag} matches repository version and HEAD {head}")
