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


def repository_root(value: str | None) -> Path:
    root = Path(value).resolve() if value else Path(__file__).resolve().parents[1]
    if not (root / ".git").exists():
        raise ReleaseError(f"not a Git repository root: {root}")
    return root


def load_config(repo: Path, config_name: str) -> dict[str, Any]:
    path = Path(config_name)
    if path.is_absolute():
        config_path = path
    else:
        config_path = repo / path
    try:
        data = json.loads(config_path.read_text(encoding="utf-8"), object_pairs_hook=_unique_pairs)
    except FileNotFoundError as exc:
        raise ReleaseError(f"missing release config: {config_name}") from exc
    except (UnicodeError, json.JSONDecodeError) as exc:
        raise ReleaseError(f"invalid release config JSON: {exc}") from exc
    if not isinstance(data, dict):
        raise ReleaseError("release config must be an object")
    if data.get("schema_version") != 1:
        raise ReleaseError("release config schema_version must be 1")
    if not isinstance(data.get("version_files"), list) or not data["version_files"]:
        raise ReleaseError("release config version_files must be a non-empty list")
    return data


def normalized_relative_path(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value or "\\" in value:
        raise ReleaseError(f"{label} must be a normalized relative POSIX path")
    path = PurePosixPath(value)
    if path.is_absolute() or any(part in {"", ".", ".."} for part in path.parts):
        raise ReleaseError(f"{label} must be a normalized relative POSIX path")
    if str(path) != value:
        raise ReleaseError(f"{label} must be a normalized relative POSIX path")
    return value


def parse_version_files(config: dict[str, Any]) -> list[VersionFile]:
    result: list[VersionFile] = []
    for index, raw in enumerate(config["version_files"], 1):
        if not isinstance(raw, dict):
            raise ReleaseError(f"version_files[{index}] must be an object")
        path = normalized_relative_path(raw.get("path"), f"version_files[{index}].path")
        kind = raw.get("kind")
        if kind not in {"plain", "json", "toml"}:
            raise ReleaseError(f"unsupported version file kind for {path}: {kind!r}")
        optional = raw.get("optional", False)
        if not isinstance(optional, bool):
            raise ReleaseError(f"version_files[{index}].optional must be boolean")
        pointer = raw.get("pointer")
        table = raw.get("table")
        key = raw.get("key")
        if kind == "json" and (not isinstance(pointer, str) or not pointer.startswith("/")):
            raise ReleaseError(f"JSON version file {path} requires an absolute pointer")
        if kind == "toml" and (not isinstance(table, str) or not table or not isinstance(key, str) or not key):
            raise ReleaseError(f"TOML version file {path} requires table and key")
        result.append(VersionFile(path, kind, optional, pointer, table, key))
    paths = [entry.path for entry in result]
    if len(paths) != len(set(paths)):
        raise ReleaseError("release config contains duplicate version file paths")
    canonical = config.get("canonical_version_file")
    if canonical not in paths:
        raise ReleaseError("canonical_version_file must name a configured version file")
    return result


def validate_semver(version: str) -> str:
    if not isinstance(version, str) or not SEMVER_RE.fullmatch(version):
        raise ReleaseError(f"invalid semantic version: {version!r}")
    return version


def semver_key(version: str) -> tuple[Any, ...]:
    match = SEMVER_RE.fullmatch(validate_semver(version))
    assert match is not None
    core = tuple(int(match.group(index)) for index in range(1, 4))
    prerelease = match.group(4)
    if prerelease is None:
        return (*core, 1, ())
    parts: list[tuple[int, Any]] = []
    for item in prerelease.split("."):
        if item.isdigit():
            parts.append((0, int(item)))
        else:
            parts.append((1, item))
    return (*core, 0, tuple(parts))


def compare_versions(left: str, right: str) -> int:
    left_key = semver_key(left)
    right_key = semver_key(right)
    return (left_key > right_key) - (left_key < right_key)


def json_pointer_parts(pointer: str | None, path: str) -> list[str]:
    if not pointer or not pointer.startswith("/"):
        raise ReleaseError(f"JSON version file {path} requires an absolute pointer")
    return [part.replace("~1", "/").replace("~0", "~") for part in pointer[1:].split("/")]


def read_json_pointer(data: Any, parts: Iterable[str], path: str) -> Any:
    current = data
    for part in parts:
        if not isinstance(current, dict) or part not in current:
            raise ReleaseError(f"JSON pointer not found in {path}: /{'/'.join(parts)}")
        current = current[part]
    return current


def write_json_pointer(data: Any, parts: list[str], value: str, path: str) -> None:
    current = data
    for part in parts[:-1]:
        if not isinstance(current, dict) or part not in current:
            raise ReleaseError(f"JSON pointer not found in {path}: /{'/'.join(parts)}")
        current = current[part]
    if not parts or not isinstance(current, dict) or parts[-1] not in current:
        raise ReleaseError(f"JSON pointer not found in {path}: /{'/'.join(parts)}")
    current[parts[-1]] = value


def toml_version_location(text: str, entry: VersionFile) -> tuple[list[str], int, re.Match[str]]:
    assert entry.table and entry.key
    table_pattern = re.compile(r"^\s*\[([^\]]+)\]\s*(?:#.*)?$")
    key_pattern = re.compile(
        rf'^(\s*{re.escape(entry.key)}\s*=\s*")([^"]+)("[^\r\n]*)(\r?\n)?$'
    )
    lines = text.splitlines(keepends=True)
    current_table: str | None = None
    matches: list[tuple[int, re.Match[str]]] = []
    for index, line in enumerate(lines):
        stripped = line.rstrip("\r\n")
        table_match = table_pattern.fullmatch(stripped)
        if table_match:
            current_table = table_match.group(1).strip()
            continue
        if current_table == entry.table:
            key_match = key_pattern.fullmatch(line)
            if key_match:
                matches.append((index, key_match))
    if len(matches) != 1:
        raise ReleaseError(
            f"TOML version key {'not found' if not matches else 'is duplicated'} in {entry.path}"
        )
    index, match = matches[0]
    return lines, index, match


def file_bytes(repo: Path, entry: VersionFile) -> bytes | None:
    path = repo / entry.path
    if path.is_symlink():
        raise ReleaseError(f"configured version file must not be a symlink: {entry.path}")
    if not path.exists():
        if entry.optional:
            return None
        raise ReleaseError(f"missing configured version file: {entry.path}")
    if not path.is_file():
        raise ReleaseError(f"configured version path is not a regular file: {entry.path}")
    try:
        return path.read_bytes()
    except OSError as exc:
        raise ReleaseError(f"cannot read configured version file {entry.path}: {exc}") from exc


def read_version(repo: Path, entry: VersionFile) -> str | None:
    raw = file_bytes(repo, entry)
    if raw is None:
        return None
    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise ReleaseError(f"configured version file is not UTF-8: {entry.path}") from exc
    if entry.kind == "plain":
        lines = text.splitlines()
        if len(lines) != 1 or not lines[0].strip():
            raise ReleaseError(f"plain version file must contain exactly one non-empty line: {entry.path}")
        return lines[0].strip()
    if entry.kind == "json":
        try:
            data = json.loads(text, object_pairs_hook=_unique_pairs)
        except (json.JSONDecodeError, ReleaseError) as exc:
            raise ReleaseError(f"invalid JSON in {entry.path}: {exc}") from exc
        value = read_json_pointer(data, json_pointer_parts(entry.pointer, entry.path), entry.path)
        if not isinstance(value, str):
            raise ReleaseError(f"version at {entry.path}{entry.pointer} must be a string")
        return value
    _, _, match = toml_version_location(text, entry)
    return match.group(2)


def render_version(original: bytes, entry: VersionFile, version: str) -> bytes:
    try:
        text = original.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise ReleaseError(f"configured version file is not UTF-8: {entry.path}") from exc
    if entry.kind == "plain":
        return (version + "\n").encode("utf-8")
    if entry.kind == "json":
        try:
            data = json.loads(text, object_pairs_hook=_unique_pairs)
        except (json.JSONDecodeError, ReleaseError) as exc:
            raise ReleaseError(f"invalid JSON in {entry.path}: {exc}") from exc
        write_json_pointer(data, json_pointer_parts(entry.pointer, entry.path), version, entry.path)
        return (json.dumps(data, indent=2, ensure_ascii=False) + "\n").encode("utf-8")
    lines, index, match = toml_version_location(text, entry)
    newline = match.group(4) or ""
    lines[index] = match.group(1) + version + match.group(3) + newline
    return "".join(lines).encode("utf-8")


def configured_versions(repo: Path, config: dict[str, Any]) -> tuple[str, list[tuple[VersionFile, str]]]:
    entries = parse_version_files(config)
    values: list[tuple[VersionFile, str]] = []
    for entry in entries:
        value = read_version(repo, entry)
        if value is not None:
            values.append((entry, validate_semver(value)))
    canonical_path = str(config["canonical_version_file"])
    canonical = next((value for entry, value in values if entry.path == canonical_path), None)
    if canonical is None:
        raise ReleaseError("canonical version file must be present")
    mismatches = [(entry.path, value) for entry, value in values if value != canonical]
    if mismatches:
        detail = ", ".join(f"{path}={value}" for path, value in mismatches)
        raise ReleaseError(f"version files disagree with {canonical_path}={canonical}: {detail}")
    return canonical, values


def check_forbidden_patterns(repo: Path, config: dict[str, Any]) -> None:
    for index, item in enumerate(config.get("forbidden_version_patterns", []), 1):
        if not isinstance(item, dict):
            raise ReleaseError(f"forbidden_version_patterns[{index}] must be an object")
        path_value = normalized_relative_path(item.get("path"), f"forbidden_version_patterns[{index}].path")
        regex_value = item.get("regex")
        optional = item.get("optional", False)
        if not isinstance(regex_value, str) or not regex_value or not isinstance(optional, bool):
            raise ReleaseError(f"forbidden_version_patterns[{index}] requires regex and boolean optional")
        path = repo / path_value
        if not path.exists() and optional:
            continue
        if not path.is_file():
            raise ReleaseError(f"forbidden-pattern file is missing: {path_value}")
        try:
            pattern = re.compile(regex_value, re.MULTILINE)
            text = path.read_text(encoding="utf-8")
        except (re.error, UnicodeError, OSError) as exc:
            raise ReleaseError(f"cannot inspect forbidden-pattern file {path_value}: {exc}") from exc
        match = pattern.search(text)
        if match:
            excerpt = match.group(0).replace("\n", "\\n")
            raise ReleaseError(f"forbidden version literal in {path_value}: {excerpt!r}")


def changelog_spec(config: dict[str, Any]) -> tuple[str, str, str]:
    raw = config.get("changelog")
    if not isinstance(raw, dict):
        raise ReleaseError("release config changelog must be an object")
    path = normalized_relative_path(raw.get("path"), "changelog.path")
    unreleased = raw.get("unreleased_heading", "## Unreleased")
    release_template = raw.get("release_heading", "## {version} — {date}")
    if not isinstance(unreleased, str) or not unreleased or not isinstance(release_template, str):
        raise ReleaseError("changelog headings must be non-empty strings")
    if "{version}" not in release_template or "{date}" not in release_template:
        raise ReleaseError("changelog.release_heading must contain {version} and {date}")
    return path, unreleased, release_template


def changelog_text(repo: Path, config: dict[str, Any]) -> tuple[str, str, str, list[re.Match[str]]]:
    path, unreleased, release_template = changelog_spec(config)
    file_path = repo / path
    if file_path.is_symlink() or not file_path.is_file():
        raise ReleaseError(f"configured changelog is missing or not regular: {path}")
    try:
        text = file_path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as exc:
        raise ReleaseError(f"cannot read configured changelog {path}: {exc}") from exc
    matches = list(re.finditer(rf"(?m)^{re.escape(unreleased)}[ \t]*(?:\r?\n|$)", text))
    if len(matches) != 1:
        raise ReleaseError(f"changelog must contain exactly one {unreleased!r} heading")
    return path, unreleased, release_template, matches


def unreleased_section(text: str, match: re.Match[str]) -> tuple[int, int, str]:
    start = match.end()
    next_heading = re.search(r"(?m)^##[ \t]+[^\r\n]*$", text[start:])
    end = start + next_heading.start() if next_heading else len(text)
    return start, end, text[start:end]


def target_heading_matches(text: str, version: str) -> list[str]:
    pattern = re.compile(rf"(?m)^##[ \t]+{re.escape(version)}(?:[ \t]+[^\r\n]*)?[ \t]*$")
    return [match.group(0).rstrip() for match in pattern.finditer(text)]


def release_heading_for(template: str, version: str, release_date: str) -> str:
    try:
        heading = template.format(version=version, date=release_date)
    except (KeyError, ValueError) as exc:
        raise ReleaseError(f"invalid release heading template: {exc}") from exc
    if "\n" in heading or not heading.startswith("## "):
        raise ReleaseError("release heading must be one level-2 Markdown heading")
    return heading


def validate_date(value: str) -> str:
    try:
        parsed = date.fromisoformat(value)
    except ValueError as exc:
        raise ReleaseError(f"invalid release date: {value!r}") from exc
    if parsed.isoformat() != value:
        raise ReleaseError(f"release date must use YYYY-MM-DD: {value!r}")
    return value


def check_changelog(repo: Path, config: dict[str, Any]) -> None:
    _, _, _, _ = changelog_text(repo, config)


def prepare_changelog_bytes(text: str, config: dict[str, Any], version: str, release_date: str) -> bytes:
    path, _, template, matches = changelog_text_from_text(text, config)
    del path
    release_heading = release_heading_for(template, version, release_date)
    if target_heading_matches(text, version):
        raise ReleaseError(f"changelog already contains a target heading for {version}")
    start, end, section = unreleased_section(text, matches[0])
    if not section.strip():
        raise ReleaseError("changelog Unreleased section is empty")
    notes = section.strip("\r\n")
    insertion = f"\n{release_heading}\n\n{notes}\n"
    suffix = text[end:]
    if suffix and not suffix.startswith("\n"):
        insertion += "\n"
    updated = text[:start] + insertion + suffix
    return updated.encode("utf-8")


def changelog_text_from_text(text: str, config: dict[str, Any]) -> tuple[str, str, str, list[re.Match[str]]]:
    path, unreleased, template = changelog_spec(config)
    matches = list(re.finditer(rf"(?m)^{re.escape(unreleased)}[ \t]*(?:\r?\n|$)", text))
    if len(matches) != 1:
        raise ReleaseError(f"changelog must contain exactly one {unreleased!r} heading")
    return path, unreleased, template, matches


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


def atomic_apply(repo: Path, changes: dict[str, bytes]) -> None:
    originals: dict[Path, bytes] = {}
    modes: dict[Path, int] = {}
    temporary: list[Path] = []
    try:
        for relative in changes:
            path = repo / relative
            if path.is_symlink() or not path.is_file():
                raise ReleaseError(f"release target is not a regular file: {relative}")
            originals[path] = path.read_bytes()
            modes[path] = stat.S_IMODE(path.stat().st_mode)
        for relative, content in changes.items():
            path = repo / relative
            with tempfile.NamedTemporaryFile("wb", dir=path.parent, prefix=f".{path.name}.release-", delete=False) as handle:
                handle.write(content)
                handle.flush()
                os.fsync(handle.fileno())
                temp_path = Path(handle.name)
            temp_path.chmod(modes[path])
            temporary.append(temp_path)
            os.replace(temp_path, path)
            temporary.remove(temp_path)
    except Exception as exc:
        for path, original in originals.items():
            try:
                path.write_bytes(original)
                path.chmod(modes[path])
            except OSError:
                pass
        if isinstance(exc, ReleaseError):
            raise
        raise ReleaseError(f"release application failed; original bytes restored: {exc}") from exc
    finally:
        for path in temporary:
            try:
                path.unlink()
            except FileNotFoundError:
                pass


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


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Canonical two-mode release lifecycle automation.")
    parser.add_argument("--repo", help="Repository root. Defaults to the parent of scripts/.")
    parser.add_argument("--config", default="release-config.json", help="Config path relative to repository root.")
    subparsers = parser.add_subparsers(dest="command", required=True)
    subparsers.add_parser("check", help="Verify configured version files and changelog structure.")
    subparsers.add_parser("check-source", help="Verify implementation_unreleased source state.")
    subparsers.add_parser("check-release-ready", help="Verify release-publication-ready state.")
    subparsers.add_parser("check-tag-ready", help="Verify annotated-tag-ready state.")
    prepare = subparsers.add_parser("prepare", help="Prepare a release from Unreleased notes.")
    prepare.add_argument("version")
    prepare.add_argument("--date", help="UTC release date YYYY-MM-DD; defaults to today.")
    subparsers.add_parser("commit", help="Commit the actual non-empty release-file subset.")
    subparsers.add_parser("tag", help="Create an annotated tag after tag-ready validation.")
    verify = subparsers.add_parser("verify-tag", help="Verify an annotated tag matches VERSION and HEAD.")
    verify.add_argument("tag")
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        repo = repository_root(args.repo)
        config = load_config(repo, args.config)
        if args.command == "check":
            command_check(repo, config)
        elif args.command == "check-source":
            command_check_source(repo, config)
        elif args.command == "check-release-ready":
            command_release_ready(repo, config)
        elif args.command == "check-tag-ready":
            command_tag_ready(repo, config)
        elif args.command == "prepare":
            command_prepare(repo, config, args.version, args.date)
        elif args.command == "commit":
            command_commit(repo, config)
        elif args.command == "tag":
            command_tag(repo, config)
        elif args.command == "verify-tag":
            command_verify_tag(repo, config, args.tag)
        else:
            parser.error(f"unsupported command: {args.command}")
    except ReleaseError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
