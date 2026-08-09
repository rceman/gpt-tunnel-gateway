from __future__ import annotations

import json
import re
from pathlib import Path
from typing import Any, Iterable

from .configuration import parse_version_files
from .foundation import SEMVER_RE, ReleaseError, VersionFile, _unique_pairs


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
