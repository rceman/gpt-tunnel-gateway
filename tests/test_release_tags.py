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

class ReleaseTagTests(ReleaseToolingTestCase):
    def test_existing_annotated_target_tag_blocks_source_prepare_and_tag_operations(self) -> None:
        source = self.make_repo("2.2.0")
        source_config = self.config(source)
        git(source, "tag", "-a", "v2.2.0", "-m", "v2.2.0")
        source_paths = ("VERSION", "CHANGELOG.md", "release-config.json")
        source_before = {path: (source / path).read_bytes() for path in source_paths}
        source_tag = git(source, "rev-parse", "refs/tags/v2.2.0")

        with self.assertRaisesRegex(self.release.ReleaseError, "target tag"):
            self.release.command_check_source(source, source_config)
        with self.assertRaisesRegex(self.release.ReleaseError, "target tag"):
            self.release.command_prepare(source, source_config, "2.2.0", "2026-08-03")
        self.assertEqual(source_before, {path: (source / path).read_bytes() for path in source_paths})
        self.assertEqual(git(source, "rev-parse", "refs/tags/v2.2.0"), source_tag)
        self.assertEqual(git(source, "status", "--porcelain", "--untracked-files=all"), "")

        ready = self.make_repo("2.1.0")
        ready_config = self.config(ready)
        self.release.command_prepare(ready, ready_config, "2.2.0", "2026-08-03")
        self.release.command_commit(ready, ready_config)
        git(ready, "tag", "-a", "v2.2.0", "-m", "v2.2.0")
        ready_tag = git(ready, "rev-parse", "refs/tags/v2.2.0")
        ready_commit = git(ready, "rev-parse", "HEAD")

        with self.assertRaisesRegex(self.release.ReleaseError, "tag already exists"):
            self.release.command_tag_ready(ready, ready_config)
        with self.assertRaisesRegex(self.release.ReleaseError, "tag already exists"):
            self.release.command_tag(ready, ready_config)
        self.assertEqual(git(ready, "rev-parse", "refs/tags/v2.2.0"), ready_tag)
        self.assertEqual(git(ready, "rev-parse", "HEAD"), ready_commit)
        self.assertEqual(git(ready, "status", "--porcelain", "--untracked-files=all"), "")

    def test_verify_tag_rejects_wrong_name_and_wrong_commit_identity(self) -> None:
        repo = self.make_repo("2.2.0")
        config = self.config(repo)
        self.release.command_prepare(repo, config, "2.2.0", "2026-08-03")
        self.release.command_commit(repo, config)

        with self.assertRaisesRegex(self.release.ReleaseError, "tag/version mismatch"):
            self.release.command_verify_tag(repo, config, "v2.2.1")

        parent = git(repo, "rev-parse", "HEAD^")
        git(repo, "tag", "-a", "v2.2.0", "-m", "v2.2.0", parent)
        with self.assertRaisesRegex(self.release.ReleaseError, "resolves to"):
            self.release.command_verify_tag(repo, config, "v2.2.0")
        self.assertEqual(git(repo, "rev-parse", "refs/tags/v2.2.0^{}"), parent)
