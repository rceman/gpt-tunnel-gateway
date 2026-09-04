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

class ReleasePrepareTests(ReleaseToolingTestCase):
    def test_prepare_propagates_regex_version_surfaces(self) -> None:
        repo = self.make_repo("2.1.0")
        surfaces = {
            "runtime.go": 'package runtime\n\nvar version = "2.1.0"\n',
            "protocol.go": 'package protocol\n\nvar response = map[string]string{"version": "2.1.0"}\n',
        }
        for path, contents in surfaces.items():
            (repo / path).write_text(contents, encoding="utf-8")
        config_path = repo / "release-config.json"
        config_data = json.loads(config_path.read_text(encoding="utf-8"))
        config_data["version_files"].extend(
            [
                {
                    "path": "runtime.go",
                    "kind": "regex",
                    "pattern": r"(?m)^var version = \"(?P<version>[0-9A-Za-z.+-]+)\"$",
                },
                {
                    "path": "protocol.go",
                    "kind": "regex",
                    "pattern": r'\"version\": \"(?P<version>[0-9A-Za-z.+-]+)\"',
                },
            ]
        )
        config_path.write_text(json.dumps(config_data, indent=2) + "\n", encoding="utf-8")
        git(repo, "add", ".")
        git(repo, "commit", "-qm", "configure runtime version surfaces")
        config = self.config(repo)

        self.release.command_check_source(repo, config)
        self.release.command_prepare(repo, config, "2.2.0", "2026-08-03")
        self.release.command_release_ready(repo, config)

        self.assertEqual((repo / "VERSION").read_text(encoding="utf-8"), "2.2.0\n")
        self.assertIn('var version = "2.2.0"', (repo / "runtime.go").read_text(encoding="utf-8"))
        self.assertIn('"version": "2.2.0"', (repo / "protocol.go").read_text(encoding="utf-8"))

    def test_regex_version_surface_fails_closed_on_missing_or_duplicate_match(self) -> None:
        for contents, message in (
            ('package runtime\nvar other = "2.1.0"\n', "not found"),
            ('var version = "2.1.0"\nvar version = "2.1.0"\n', "duplicated"),
        ):
            repo = self.make_repo("2.1.0")
            (repo / "runtime.go").write_text(contents, encoding="utf-8")
            config_path = repo / "release-config.json"
            config_data = json.loads(config_path.read_text(encoding="utf-8"))
            config_data["version_files"].append(
                {
                    "path": "runtime.go",
                    "kind": "regex",
                    "pattern": r"(?m)^var version = \"(?P<version>[0-9A-Za-z.+-]+)\"$",
                }
            )
            config_path.write_text(json.dumps(config_data, indent=2) + "\n", encoding="utf-8")
            git(repo, "add", ".")
            git(repo, "commit", "-qm", "configure runtime version surface")
            with self.assertRaisesRegex(self.release.ReleaseError, message):
                self.release.command_check(repo, self.config(repo))

    def test_source_state_is_distinct_from_release_ready(self) -> None:
        repo = self.make_repo()
        config = self.config(repo)
        self.release.command_check_source(repo, config)
        with self.assertRaises(self.release.ReleaseError):
            self.release.command_release_ready(repo, config)

    def test_prepare_lower_version_and_pre_set_target(self) -> None:
        repo = self.make_repo("2.1.0")
        config = self.config(repo)
        self.release.command_prepare(repo, config, "2.2.0", "2026-08-03")
        self.assertEqual((repo / "VERSION").read_text(), "2.2.0\n")
        self.assertIn("## 2.2.0 — 2026-08-03", (repo / "CHANGELOG.md").read_text())
        self.release.command_release_ready(repo, config)

        preset = self.make_repo("2.2.0")
        preset_config = self.config(preset)
        before = (preset / "VERSION").read_bytes()
        self.release.command_prepare(preset, preset_config, "2.2.0", "2026-08-03")
        self.assertEqual((preset / "VERSION").read_bytes(), before)
        self.assertEqual(git(preset, "status", "--porcelain"), "M CHANGELOG.md")
        self.release.command_commit(preset, preset_config)
        self.assertEqual(git(preset, "show", "-s", "--format=%s"), "chore(release): v2.2.0")

    def test_prepare_rejects_downgrade_and_repeated_promotion(self) -> None:
        repo = self.make_repo("2.2.0")
        config = self.config(repo)
        with self.assertRaisesRegex(self.release.ReleaseError, "downgrade"):
            self.release.command_prepare(repo, config, "2.1.9", "2026-08-03")
        self.release.command_prepare(repo, config, "2.2.0", "2026-08-03")
        before = {path: (repo / path).read_bytes() for path in ("VERSION", "CHANGELOG.md")}
        with self.assertRaises(self.release.ReleaseError):
            self.release.command_prepare(repo, config, "2.2.0", "2026-08-03")
        self.assertEqual(before, {path: (repo / path).read_bytes() for path in before})

    def test_prepare_rejects_empty_malformed_multiple_and_existing_target(self) -> None:
        for changelog in (
            "# Changelog\n\n## Unreleased\n\n## 2.0.0 — 2026-07-31\n",
            "# Changelog\n\n## Unreleased\n\n- note\n\n## Unreleased\n\n- duplicate\n",
            "# Changelog\n\n##Unreleased\n\n- malformed\n",
            "# Changelog\n\n## Unreleased\n\n- note\n\n## 2.2.0 — 2026-08-02\n",
        ):
            repo = self.make_repo("2.1.0")
            (repo / "CHANGELOG.md").write_text(changelog, encoding="utf-8")
            config = self.config(repo)
            before = (repo / "VERSION").read_bytes()
            with self.assertRaises(self.release.ReleaseError):
                self.release.command_prepare(repo, config, "2.2.0", "2026-08-03")
            self.assertEqual((repo / "VERSION").read_bytes(), before)

    def test_mismatched_configured_versions_fail_before_mutation(self) -> None:
        repo = self.make_repo("2.2.0")
        second_version = repo / "VERSION.secondary"
        second_version.write_text("2.2.1\n", encoding="utf-8")
        config_path = repo / "release-config.json"
        config_data = json.loads(config_path.read_text(encoding="utf-8"))
        config_data["version_files"] = [
            {"path": "VERSION", "kind": "plain"},
            {"path": "VERSION.secondary", "kind": "plain"},
        ]
        config_path.write_text(json.dumps(config_data, indent=2) + "\n", encoding="utf-8")
        git(repo, "add", ".")
        git(repo, "commit", "-qm", "configure multiple version files")
        config = self.config(repo)
        paths = ("VERSION", "VERSION.secondary", "CHANGELOG.md", "release-config.json")
        before = {path: (repo / path).read_bytes() for path in paths}
        head_before = git(repo, "rev-parse", "HEAD")

        for checker in (self.release.command_check, self.release.command_check_source):
            with self.assertRaisesRegex(self.release.ReleaseError, "version files disagree"):
                checker(repo, config)
            self.assertEqual(before, {path: (repo / path).read_bytes() for path in paths})
            self.assertEqual(git(repo, "rev-parse", "HEAD"), head_before)
            self.assertEqual(git(repo, "status", "--porcelain", "--untracked-files=all"), "")
