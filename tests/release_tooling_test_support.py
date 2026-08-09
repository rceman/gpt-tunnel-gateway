from __future__ import annotations

import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


ROOT = Path(__file__).resolve().parents[1]
RELEASE = ROOT / "scripts/release.py"
CONFORMANCE = ROOT / "scripts/validate-release-tool-conformance.py"


def load_release():
    spec = importlib.util.spec_from_file_location("release_lifecycle", RELEASE)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def git(repo: Path, *args: str, check: bool = True) -> str:
    result = subprocess.run(
        ["git", "-C", str(repo), *args],
        check=check,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    return result.stdout.strip()


class ReleaseToolingTestCase(unittest.TestCase):
    def setUp(self) -> None:
        self.release = load_release()

    def make_repo(self, version: str = "2.1.0", notes: str = "- Runtime policy work.\n") -> Path:
        root = Path(tempfile.mkdtemp())
        git(root, "init", "-q")
        git(root, "config", "user.name", "Lifecycle Test")
        git(root, "config", "user.email", "lifecycle@example.com")
        (root / "VERSION").write_text(version + "\n", encoding="utf-8")
        (root / "README.md").write_text("Use immutable tags.\n", encoding="utf-8")
        (root / "CHANGELOG.md").write_text(
            "# Changelog\n\n## Unreleased\n\n" + notes + "\n## 2.0.0 — 2026-07-31\n\n- Prior.\n",
            encoding="utf-8",
        )
        config = {
            "schema_version": 1,
            "canonical_version_file": "VERSION",
            "tag_prefix": "v",
            "release_commit_message": "chore(release): v{version}",
            "version_files": [{"path": "VERSION", "kind": "plain"}],
            "forbidden_version_patterns": [],
            "changelog": {
                "path": "CHANGELOG.md",
                "unreleased_heading": "## Unreleased",
                "release_heading": "## {version} — {date}",
            },
        }
        (root / "release-config.json").write_text(json.dumps(config, indent=2) + "\n", encoding="utf-8")
        git(root, "add", ".")
        git(root, "commit", "-qm", "base")
        return root

    def config(self, repo: Path) -> dict:
        return self.release.load_config(repo, "release-config.json")
