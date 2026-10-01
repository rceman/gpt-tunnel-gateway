"""Unit tests for scripts/release-prod.py authority and publication ordering."""
import importlib.util
import json
import os
import sqlite3
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

MODULE_PATH = Path(__file__).with_name("release-prod.py")
SPEC = importlib.util.spec_from_file_location("release_prod", MODULE_PATH)
release_prod = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(release_prod)

COMMIT = "a" * 40
TREE = "b" * 40


def seed_shared(state_dir, track_payload=None, revision=8):
    databases = Path(state_dir) / "databases"
    databases.mkdir(parents=True)
    connection = sqlite3.connect(str(databases / "shared.db"))
    try:
        connection.execute(
            "CREATE TABLE shared_tracks(id TEXT PRIMARY KEY, revision INTEGER, payload BLOB)")
        if track_payload is not None:
            connection.execute(
                "INSERT INTO shared_tracks(id,revision,payload) VALUES(?,?,?)",
                (track_payload["id"], revision, json.dumps(track_payload)),
            )
        connection.commit()
    finally:
        connection.close()


def accepted_track(revision=8, head=COMMIT, tree=TREE, status="accepted"):
    return {
        "schema_version": 1,
        "id": "GTW-TRK2",
        "project_id": "gpt-tunnel-gateway",
        "revision": revision,
        "milestone": "GTW-MIL1",
        "title": "Example Track",
        "tasks": ["GTW-TSK1"],
        "status": status,
        "review": {
            "head": head,
            "tree": tree,
            "digest": "c" * 64,
            "track_revision": revision,
            "tasks": [{"key": "GTW-TSK1", "revision": 1, "revision_sha256": "d" * 64}],
            "submitted_at": "2026-09-30T00:00:00Z",
            "submitted_by": "HOM_GTW_L_test",
        },
        "created_by": "planner",
        "created_at": "2026-09-30T00:00:00Z",
        "updated_by": "planner",
        "updated_at": "2026-09-30T00:00:00Z",
    }


class AuthorityBindTests(unittest.TestCase):
    def payload(self, **overrides):
        value = {"track": "GTW-TRK2", "source_commit": COMMIT, "source_tree": TREE}
        value.update(overrides)
        return value

    def run_bind(self, state_dir, payload):
        with self.assertRaises(SystemExit):
            release_prod.authority_bind(Path(state_dir), payload)

    def test_accepted_track_binding_source_passes(self):
        with tempfile.TemporaryDirectory() as tmp:
            seed_shared(tmp, accepted_track())
            track = release_prod.authority_bind(Path(tmp), self.payload())
            self.assertEqual(track["id"], "GTW-TRK2")

    def test_missing_track_fails_before_side_effect(self):
        with tempfile.TemporaryDirectory() as tmp:
            seed_shared(tmp)
            self.run_bind(tmp, self.payload())

    def test_non_accepted_track_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            seed_shared(tmp, accepted_track(status="review_pending"))
            self.run_bind(tmp, self.payload())

    def test_mismatched_head_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            seed_shared(tmp, accepted_track(head="e" * 40))
            self.run_bind(tmp, self.payload())

    def test_mismatched_tree_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            seed_shared(tmp, accepted_track(tree="f" * 40))
            self.run_bind(tmp, self.payload())

    def test_stale_review_revision_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            track = accepted_track()
            track["review"]["track_revision"] = 7
            seed_shared(tmp, track)
            self.run_bind(tmp, self.payload())

    def test_row_revision_mismatch_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            seed_shared(tmp, accepted_track(), revision=9)
            self.run_bind(tmp, self.payload())


class MainInputTests(unittest.TestCase):
    def run_main(self, envelope, run_dir):
        input_path = Path(run_dir) / "input.json"
        output_path = Path(run_dir) / "output.json"
        input_path.write_text(json.dumps(envelope))
        environment = {
            "GTW_PROCEDURE_INPUT_FILE": str(input_path),
            "GTW_PROCEDURE_OUTPUT_FILE": str(output_path),
            "TMPDIR": str(run_dir),
        }
        with mock.patch.dict(os.environ, environment, clear=True):
            with self.assertRaises(SystemExit):
                release_prod.main()

    def test_short_fingerprint_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            run_dir = Path(tmp) / "procedure-runs" / "run"
            run_dir.mkdir(parents=True)
            (Path(tmp) / "databases").mkdir()
            envelope = {
                "input": {"track": "GTW-TRK2", "source_commit": COMMIT[:8], "source_tree": TREE},
                "context": {"project": "gpt-tunnel-gateway"},
            }
            self.run_main(envelope, run_dir)

    def test_wrong_project_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            run_dir = Path(tmp) / "procedure-runs" / "run"
            run_dir.mkdir(parents=True)
            (Path(tmp) / "databases").mkdir()
            envelope = {
                "input": {"track": "GTW-TRK2", "source_commit": COMMIT, "source_tree": TREE},
                "context": {"project": "other"},
            }
            self.run_main(envelope, run_dir)


class RemoteRepositoryTests(unittest.TestCase):
    def test_github_ssh_and_https_urls_parse(self):
        for url in ("git@github.com:owner/repo.git", "https://github.com/owner/repo.git",
                    "https://github.com/owner/repo"):
            with mock.patch.object(release_prod, "run_git", return_value=url + "\n"):
                self.assertEqual(release_prod.remote_repository(Path(".")), "owner/repo")

    def test_non_github_remote_fails_closed(self):
        with mock.patch.object(release_prod, "run_git", return_value="https://example.com/x/y.git\n"):
            with self.assertRaises(SystemExit):
                release_prod.remote_repository(Path("."))


if __name__ == "__main__":
    unittest.main()
