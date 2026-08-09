#!/usr/bin/env python3
"""Compatibility entrypoint for the descriptive release-tooling package."""
from __future__ import annotations

import sys
import os
from pathlib import Path


_SCRIPTS_DIR = Path(__file__).resolve().parent
if str(_SCRIPTS_DIR) not in sys.path:
    sys.path.insert(0, str(_SCRIPTS_DIR))

from release_tooling import (  # noqa: E402
    LIFECYCLE_MODES,
    ReleaseError,
    SEMVER_RE,
    StatusRecord,
    VersionFile,
    allowed_release_paths,
    atomic_apply,
    build_parser,
    changelog_spec,
    changelog_text,
    changelog_text_from_text,
    check_changelog,
    check_forbidden_patterns,
    command_check,
    command_check_source,
    command_commit,
    command_prepare,
    command_release_ready,
    command_tag,
    command_tag_ready,
    command_verify_tag,
    compare_versions,
    configured_versions,
    current_head,
    ensure_clean,
    file_bytes,
    json_pointer_parts,
    lifecycle_message,
    load_config,
    main,
    normalized_relative_path,
    parse_version_files,
    prepare_changelog_bytes,
    read_json_pointer,
    read_version,
    release_commit_paths,
    release_heading_for,
    render_version,
    repository_root,
    run_git,
    semver_key,
    source_state,
    status_paths,
    status_records,
    tag_exists,
    tag_name,
    target_heading_matches,
    toml_version_location,
    unreleased_section,
    validate_date,
    validate_release_ready_state,
    validate_semver,
    validate_tag_ready_state,
    write_json_pointer,
)


if __name__ == "__main__":
    raise SystemExit(main())
