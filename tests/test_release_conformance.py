from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from release_tooling_test_support import CONFORMANCE, RELEASE, ROOT, ReleaseToolingTestCase, git

class ReleaseConformanceTests(ReleaseToolingTestCase):
    def test_tool_conformance_and_exact_cli_names(self) -> None:
        result = subprocess.run(
            [
                sys.executable,
                str(CONFORMANCE),
                "--release-script",
                str(RELEASE),
                "--ci-script",
                str(ROOT / "scripts/check-github-ci.py"),
            ],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        help_result = subprocess.run([sys.executable, str(RELEASE), "--help"], text=True, capture_output=True)
        self.assertEqual(help_result.returncode, 0)
        self.assertIn("check-release-ready", help_result.stdout)
        self.assertIn("check-tag-ready", help_result.stdout)

    def test_gateway_source_state_uses_actual_release_config_without_mutation(self) -> None:
        repo = Path(tempfile.mkdtemp())
        for path in ("VERSION", "CHANGELOG.md", "release-config.json"):
            (repo / path).write_bytes((ROOT / path).read_bytes())
        (repo / "CHANGELOG.md").write_text("# Changelog\n\n## Unreleased\n\n- tooling source-state fixture.\n\n## 0.6.2 — 2026-08-06\n\n- Prior.\n", encoding="utf-8")
        target_version = (ROOT / "VERSION").read_text(encoding="utf-8").strip()
        git(repo, "init", "-q")
        git(repo, "config", "user.name", "Gateway source-state test")
        git(repo, "config", "user.email", "source@example.invalid")
        git(repo, "add", ".")
        git(repo, "commit", "-qm", "source state")
        config = self.release.load_config(repo, "release-config.json")
        before = {path: (repo / path).read_bytes() for path in ("VERSION", "CHANGELOG.md", "release-config.json")}
        self.release.command_check_source(repo, config)
        self.release.command_check(repo, config)
        self.assertEqual(before, {path: (repo / path).read_bytes() for path in before})
        self.assertEqual(git(repo, "status", "--porcelain", "--untracked-files=all"), "")
        self.assertEqual((repo / "VERSION").read_text(encoding="utf-8"), target_version + "\n")
        self.assertNotIn(f"## {target_version} — ", (repo / "CHANGELOG.md").read_text(encoding="utf-8"))

    def test_gateway_preset_target_prepare_preserves_version_bytes(self) -> None:
        repo = Path(tempfile.mkdtemp())
        for path in ("VERSION", "CHANGELOG.md", "release-config.json"):
            (repo / path).write_bytes((ROOT / path).read_bytes())
        (repo / "CHANGELOG.md").write_text("# Changelog\n\n## Unreleased\n\n- tooling release fixture.\n\n## 0.6.2 — 2026-08-06\n\n- Prior.\n", encoding="utf-8")
        target_version = (ROOT / "VERSION").read_text(encoding="utf-8").strip()
        git(repo, "init", "-q")
        git(repo, "config", "user.name", "Gateway release test")
        git(repo, "config", "user.email", "release@example.invalid")
        git(repo, "add", ".")
        git(repo, "commit", "-qm", "source state")
        config = self.release.load_config(repo, "release-config.json")
        version_before = (repo / "VERSION").read_bytes()
        self.release.command_prepare(repo, config, target_version, "2026-08-07")
        self.assertEqual((repo / "VERSION").read_bytes(), version_before)
        self.assertEqual(git(repo, "status", "--porcelain"), "M CHANGELOG.md")
        self.assertIn(f"## {target_version} — 2026-08-07", (repo / "CHANGELOG.md").read_text(encoding="utf-8"))

    def test_conformance_rejects_mutated_release_or_ci_script(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            altered_release = Path(directory) / "release.py"
            altered_release.write_bytes(RELEASE.read_bytes() + b"\n")
            result = subprocess.run(
                [sys.executable, str(CONFORMANCE), "--release-script", str(altered_release), "--ci-script", str(ROOT / "scripts/check-github-ci.py")],
                capture_output=True,
                text=True,
            )
            self.assertNotEqual(result.returncode, 0)

            altered_ci = Path(directory) / "check-github-ci.py"
            altered_ci.write_bytes((ROOT / "scripts/check-github-ci.py").read_bytes() + b"\n")
            result = subprocess.run(
                [sys.executable, str(CONFORMANCE), "--release-script", str(RELEASE), "--ci-script", str(altered_ci)],
                capture_output=True,
                text=True,
            )
            self.assertNotEqual(result.returncode, 0)

    def test_adopted_tools_are_executable(self) -> None:
        for path in (RELEASE, ROOT / "scripts/check-github-ci.py", CONFORMANCE):
            self.assertTrue(path.is_file())
            self.assertTrue(path.stat().st_mode & 0o111, path)
