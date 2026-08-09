from __future__ import annotations

import json
from pathlib import Path, PurePosixPath
from typing import Any

from .foundation import ReleaseError, VersionFile, _unique_pairs


def repository_root(value: str | None) -> Path:
    root = Path(value).resolve() if value else Path(__file__).resolve().parents[2]
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
