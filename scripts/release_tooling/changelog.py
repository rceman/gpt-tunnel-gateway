from __future__ import annotations

import re
from datetime import date
from pathlib import Path
from typing import Any

from .configuration import normalized_relative_path
from .foundation import ReleaseError


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
