from __future__ import annotations

import re
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from .changelog import (
    changelog_spec,
    changelog_text,
    check_changelog,
    prepare_changelog_bytes,
    release_heading_for,
    target_heading_matches,
    unreleased_section,
    validate_date,
)
from .configuration import parse_version_files
from .foundation import ReleaseError, VersionFile, run_git
from .repository_state import (
    current_head,
    ensure_clean,
    lifecycle_message,
    status_records,
    tag_exists,
    tag_name,
)
from .transaction import atomic_apply
from .version_files import (
    check_forbidden_patterns,
    configured_versions,
    compare_versions,
    file_bytes,
    render_version,
    validate_semver,
)


def command_check(repo: Path, config: dict[str, Any]) -> None:
    version, values = configured_versions(repo, config)
    check_forbidden_patterns(repo, config)
    check_changelog(repo, config)
    print(f"PASS: version {version}")
    for entry, value in values:
        print(f"  {entry.path}: {value}")


def source_state(repo: Path, config: dict[str, Any]) -> tuple[str, list[tuple[VersionFile, str]]]:
    ensure_clean(repo)
    version, values = configured_versions(repo, config)
    check_forbidden_patterns(repo, config)
    path, _, _, matches = changelog_text(repo, config)
    text = (repo / path).read_text(encoding="utf-8")
    _, _, section = unreleased_section(text, matches[0])
    if not section.strip():
        raise ReleaseError("source state requires non-empty changelog Unreleased section")
    if target_heading_matches(text, version):
        raise ReleaseError(f"source state must not contain a dated target heading for {version}")
    tag = tag_name(config, version)
    if tag_exists(repo, tag):
        raise ReleaseError(f"source state must not contain target tag: {tag}")
    return version, values


def command_check_source(repo: Path, config: dict[str, Any]) -> None:
    version, values = source_state(repo, config)
    print(f"PASS: source state version {version}; lifecycle=implementation_unreleased")
    for entry, value in values:
        print(f"  {entry.path}: {value}")


def command_prepare(repo: Path, config: dict[str, Any], version: str, release_date: str | None = None) -> None:
    ensure_clean(repo)
    target = validate_semver(version)
    current, values = configured_versions(repo, config)
    comparison = compare_versions(target, current)
    if comparison < 0:
        raise ReleaseError(f"release downgrade is forbidden: current={current}, target={target}")
    check_forbidden_patterns(repo, config)
    changelog_path_value, _, _, changelog_matches = changelog_text(repo, config)
    changelog_path_obj = repo / changelog_path_value
    original_changelog = changelog_path_obj.read_bytes()
    try:
        changelog_text_value = original_changelog.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise ReleaseError(f"configured changelog is not UTF-8: {changelog_path_value}") from exc
    selected_date = validate_date(release_date or datetime.now(timezone.utc).date().isoformat())
    release_heading_for(changelog_spec(config)[2], target, selected_date)
    if target_heading_matches(changelog_text_value, target):
        raise ReleaseError(f"changelog already contains a target heading for {target}")
    _, _, section = unreleased_section(changelog_text_value, changelog_matches[0])
    if not section.strip():
        raise ReleaseError("changelog Unreleased section is empty")
    release_tag = tag_name(config, target)
    if tag_exists(repo, release_tag):
        raise ReleaseError(f"target tag already exists: {release_tag}")

    changes: dict[str, bytes] = {}
    if comparison > 0:
        for entry, current_value in values:
            del current_value
            original = file_bytes(repo, entry)
            assert original is not None
            updated = render_version(original, entry, target)
            if updated != original:
                changes[entry.path] = updated
    updated_changelog = prepare_changelog_bytes(changelog_text_value, config, target, selected_date)
    if updated_changelog != original_changelog:
        changes[changelog_path_value] = updated_changelog
    if not changes:
        raise ReleaseError("prepare changed no files")
    atomic_apply(repo, changes)
    print(f"Prepared release lifecycle for v{target}:")
    for path in sorted(changes):
        print(f"  {path}")


def allowed_release_paths(config: dict[str, Any]) -> set[str]:
    entries = parse_version_files(config)
    allowed = {entry.path for entry in entries}
    changelog = changelog_spec(config)[0]
    allowed.add(changelog)
    return allowed


def validate_release_ready_state(repo: Path, config: dict[str, Any]) -> tuple[str, set[str]]:
    version, _ = configured_versions(repo, config)
    check_forbidden_patterns(repo, config)
    path, _, template, matches = changelog_text(repo, config)
    text = (repo / path).read_text(encoding="utf-8")
    start, end, section = unreleased_section(text, matches[0])
    del start, end
    if section.strip():
        raise ReleaseError("release-ready state requires an empty Unreleased section")
    target_headings = target_heading_matches(text, version)
    expected_prefix = release_heading_for(template, version, "2000-01-01").rsplit("2000-01-01", 1)[0]
    if len(target_headings) != 1:
        raise ReleaseError(f"release-ready state requires exactly one dated heading for {version}")
    heading_date_match = re.search(r"(\d{4}-\d{2}-\d{2})[ \t]*$", target_headings[0])
    if heading_date_match is None:
        raise ReleaseError(f"release heading for {version} must contain an ISO date")
    validate_date(heading_date_match.group(1))
    if expected_prefix and not target_headings[0].startswith(expected_prefix):
        raise ReleaseError(f"release heading does not match configured template for {version}")

    records = status_records(repo)
    allowed = allowed_release_paths(config)
    changed = set()
    for record in records:
        if record.status == "??" or "U" in record.status or "R" in record.status or "C" in record.status:
            raise ReleaseError("release-ready worktree contains unsupported status: " + record.status)
        changed.update(record.paths)
    unexpected = sorted(changed - allowed)
    if unexpected:
        raise ReleaseError("release-ready worktree contains unrelated paths: " + ", ".join(unexpected))
    return version, changed


def command_release_ready(repo: Path, config: dict[str, Any]) -> None:
    version, changed = validate_release_ready_state(repo, config)
    print(f"PASS: release-ready v{version}; changed={len(changed)}")


def command_commit(repo: Path, config: dict[str, Any]) -> None:
    version, changed = validate_release_ready_state(repo, config)
    if not changed:
        raise ReleaseError("release commit would be empty")
    run_git(repo, "add", "--", *sorted(changed))
    message = lifecycle_message(config, version)
    run_git(repo, "commit", "-m", message)
    print(f"Created release commit {current_head(repo)} for v{version}")


def release_commit_paths(repo: Path) -> set[str]:
    output = run_git(repo, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD").stdout
    return {line for line in output.splitlines() if line}
