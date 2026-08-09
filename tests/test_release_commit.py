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

class ReleaseCommitTests(ReleaseToolingTestCase):
    def test_prepare_application_failure_restores_all_original_bytes(self) -> None:
        repo = self.make_repo("2.1.0")
        config = self.config(repo)
        before = {path: (repo / path).read_bytes() for path in ("VERSION", "CHANGELOG.md")}
        original_replace = os.replace
        calls = 0

        def failing_replace(source, target):
            nonlocal calls
            calls += 1
            if calls == 2:
                raise OSError("injected replace failure")
            return original_replace(source, target)

        with mock.patch.object(self.release.os, "replace", side_effect=failing_replace):
            with self.assertRaises(self.release.ReleaseError):
                self.release.command_prepare(repo, config, "2.2.0", "2026-08-03")
        self.assertEqual(before, {path: (repo / path).read_bytes() for path in before})
        self.assertEqual(git(repo, "status", "--porcelain"), "")

    def test_release_commit_accepts_changelog_only_and_rejects_unrelated_or_empty(self) -> None:
        repo = self.make_repo("2.2.0")
        config = self.config(repo)
        self.release.command_prepare(repo, config, "2.2.0", "2026-08-03")
        self.release.command_commit(repo, config)
        with self.assertRaisesRegex(self.release.ReleaseError, "empty"):
            self.release.command_commit(repo, config)

        unrelated = self.make_repo("2.2.0")
        unrelated_config = self.config(unrelated)
        self.release.command_prepare(unrelated, unrelated_config, "2.2.0", "2026-08-03")
        (unrelated / "unrelated.txt").write_text("not a release file\n", encoding="utf-8")
        with self.assertRaises(self.release.ReleaseError):
            self.release.command_commit(unrelated, unrelated_config)

    def test_annotated_tag_identity_and_lightweight_rejection(self) -> None:
        repo = self.make_repo("2.2.0")
        config = self.config(repo)
        self.release.command_prepare(repo, config, "2.2.0", "2026-08-03")
        self.release.command_commit(repo, config)
        self.release.command_tag_ready(repo, config)
        self.release.command_tag(repo, config)
        self.release.command_verify_tag(repo, config, "v2.2.0")

        lightweight = self.make_repo("2.2.0")
        lightweight_config = self.config(lightweight)
        self.release.command_prepare(lightweight, lightweight_config, "2.2.0", "2026-08-03")
        self.release.command_commit(lightweight, lightweight_config)
        git(lightweight, "tag", "v2.2.0")
        with self.assertRaisesRegex(self.release.ReleaseError, "lightweight"):
            self.release.command_verify_tag(lightweight, lightweight_config, "v2.2.0")
