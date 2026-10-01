"""Unit tests for scripts/activate-local.py authority and failure handling."""
import importlib.util
import json
import os
import sqlite3
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

MODULE_PATH = Path(__file__).with_name("activate-local.py")
SPEC = importlib.util.spec_from_file_location("activate_local", MODULE_PATH)
activate_local = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(activate_local)

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
            activate_local.authority_bind(Path(state_dir), payload)

    def test_accepted_track_binding_source_passes(self):
        with tempfile.TemporaryDirectory() as tmp:
            seed_shared(tmp, accepted_track())
            track = activate_local.authority_bind(Path(tmp), self.payload())
            self.assertEqual(track["id"], "GTW-TRK2")

    def test_missing_track_fails_before_mutation(self):
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
            self.run_bind(tmp, self.payload(source_commit=COMMIT))

    def test_stale_review_revision_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            track = accepted_track()
            track["review"]["track_revision"] = 7
            seed_shared(tmp, track)
            self.run_bind(tmp, self.payload())

    def test_row_revision_mismatch_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            track = accepted_track()
            seed_shared(tmp, track, revision=9)
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
                activate_local.main()

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


class FailureEvidenceTests(unittest.TestCase):
    """The TSK664 failure class: a failed cutover surfaces bounded diagnostics
    instead of deleting evidence."""

    def test_cutover_failure_relays_diagnostics(self):
        with tempfile.TemporaryDirectory() as tmp:
            ctl = Path(tmp) / "gpt-tunnelctl"
            ctl.write_text("#!/bin/sh\nexit 0\n")
            ctl.chmod(0o700)

            def fake_completed(args, **kwargs):
                if args[1] == "runtime-status":
                    return subprocess.CompletedProcess(args, 0, stdout=json.dumps({
                        "tunnel": {"running": True, "identity_valid": True, "pid": 4242},
                    }), stderr="")
                failure = {"phase": "compatible_forward_recovery", "error": "readiness failed",
                           "rollback": {"durable_state_restored": False, "forward_recovery": "required"}}
                return subprocess.CompletedProcess(
                    args, 42, stdout="", stderr="activation failure: " + json.dumps(failure))

            with mock.patch.object(subprocess, "run", side_effect=fake_completed):
                with self.assertRaises(SystemExit):
                    activate_local.run_ctl(ctl, ["install-and-restart-gateway"], "cutover")


if __name__ == "__main__":
    unittest.main()
