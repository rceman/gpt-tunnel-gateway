import hashlib
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from types import SimpleNamespace


ROOT = Path(__file__).resolve().parents[1]
SCRIPTS = ROOT / "scripts"
sys.path.insert(0, str(SCRIPTS))


def load_script(name, filename):
    spec = importlib.util.spec_from_file_location(name, SCRIPTS / filename)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


loader = load_script("pinned_workflow_loader", "load-pinned-workflow.py")
publication = load_script("release_publication_verifier", "verify-release-publication.py")
receipt = load_script("completion_receipt", "completion_receipt.py")


class PublicationHandler(BaseHTTPRequestHandler):
    run = {}
    jobs = {}
    release_status = 404

    def do_GET(self):
        if "/actions/runs/" in self.path and "/jobs" in self.path:
            status, payload = 200, type(self).jobs
        elif "/actions/runs" in self.path:
            status, payload = 200, {"workflow_runs": [type(self).run]}
        elif "/releases/tags/" in self.path:
            status, payload = type(self).release_status, {"message": "not found"}
        else:
            status, payload = 404, {"message": "not found"}
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


class CanonicalAgentToolingTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), PublicationHandler)
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()
        cls.api_url = f"http://127.0.0.1:{cls.server.server_port}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()

    def setUp(self):
        PublicationHandler.release_status = 404

    def git(self, repo, *args):
        return subprocess.run(["git", "-C", str(repo), *args], check=True, capture_output=True, text=True).stdout.strip()

    def make_release_repo(self):
        directory = tempfile.TemporaryDirectory()
        repo = Path(directory.name) / "repo"
        remote = Path(directory.name) / "remote.git"
        repo.mkdir()
        self.git(repo, "init", "-b", "main")
        self.git(repo, "config", "user.name", "Tooling Test")
        self.git(repo, "config", "user.email", "tooling@example.invalid")
        (repo / "VERSION").write_text("9.9.9\n", encoding="utf-8")
        (repo / "release-config.json").write_text(json.dumps({"publication": {"topology": "tag_only", "github_release": False, "assets": []}}), encoding="utf-8")
        self.git(repo, "add", "VERSION", "release-config.json")
        self.git(repo, "commit", "-m", "fixture")
        commit = self.git(repo, "rev-parse", "HEAD")
        self.git(repo, "tag", "-a", "v9.9.9", "-m", "v9.9.9")
        self.git(repo, "init", "--bare", str(remote))
        self.git(repo, "remote", "add", "origin", str(remote))
        self.git(repo, "push", "origin", "refs/tags/v9.9.9")
        return directory, repo, commit

    def publication_args(self, repo, commit, api_url=None):
        return SimpleNamespace(
            repository="owner/repo",
            commit=commit,
            tag="v9.9.9",
            repository_root=str(repo),
            remote="origin",
            config="release-config.json",
            api_url=api_url or self.api_url,
        )

    def test_release_publication_success_proves_tag_ci_jobs_and_tag_only(self):
        directory, repo, commit = self.make_release_repo()
        self.addCleanup(directory.cleanup)
        PublicationHandler.run = {
            "id": 41,
            "head_sha": commit,
            "status": "completed",
            "conclusion": "success",
            "name": "Validate",
            "path": ".github/workflows/ci.yml",
            "event": "push",
            "head_branch": "main",
            "html_url": "https://github.com/owner/repo/actions/runs/41",
        }
        PublicationHandler.jobs = {"total_count": 1, "jobs": [{
            "id": 501,
            "name": "unit",
            "status": "completed",
            "conclusion": "success",
            "html_url": "https://github.com/owner/repo/actions/runs/41/jobs/501",
        }]}
        result = publication.verify(self.publication_args(repo, commit))
        self.assertEqual(result["state"], "success")
        self.assertEqual(result["peeled_commit"], commit)
        self.assertEqual(result["jobs"][0]["id"], 501)
        self.assertEqual(result["release"]["state"], "not_found_expected")

    def test_release_publication_rejects_incomplete_job_proof(self):
        directory, repo, commit = self.make_release_repo()
        self.addCleanup(directory.cleanup)
        PublicationHandler.run = {
            "id": 41,
            "head_sha": commit,
            "status": "completed",
            "conclusion": "success",
            "name": "Validate",
            "html_url": "https://github.com/owner/repo/actions/runs/41",
        }
        PublicationHandler.jobs = {"total_count": 2, "jobs": [{"id": 501, "name": "unit", "status": "completed", "conclusion": "success", "html_url": "https://github.com/owner/repo/actions/runs/41/jobs/501"}]}
        with self.assertRaises(publication.PublicationError) as raised:
            publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "ci_job_set_mismatch")

    def test_release_publication_preserves_typed_auth_failure(self):
        directory, repo, commit = self.make_release_repo()
        self.addCleanup(directory.cleanup)
        with self.assertRaises(publication.PublicationError) as raised:
            publication.verify(self.publication_args(repo, commit, "http://127.0.0.1:9"))
        self.assertEqual(raised.exception.state, "unavailable")

    def test_pinned_workflow_loader_checks_digest_and_bound(self):
        content = b"workflow\n"
        lock = {
            "schema_version": 2,
            "repository": "https://github.com/owner/repo",
            "commit": "a" * 40,
            "document": "WORKFLOW.md",
            "sha256": hashlib.sha256(content).hexdigest(),
        }

        class Response:
            def __enter__(self): return self
            def __exit__(self, *args): return False
            def read(self, limit): return content

        original = loader.urlopen
        loader.urlopen = lambda request, timeout: Response()
        self.addCleanup(lambda: setattr(loader, "urlopen", original))
        result = loader.retrieve(lock, "b" * 64)
        self.assertEqual(result["status"], "READY")
        self.assertEqual(result["sha256"], lock["sha256"])
        lock["sha256"] = "c" * 64
        with self.assertRaises(loader.CanonicalToolingGap) as raised:
            loader.retrieve(lock, "b" * 64)
        self.assertIn("BLOCKED_CANONICAL_TOOLING_GAP", str(raised.exception))

    def test_pinned_workflow_loader_rejects_escape_and_noncanonical_commit(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "lock.json"
            path.write_text(json.dumps({"schema_version": 2, "repository": "https://github.com/owner/repo", "commit": "A" * 40, "document": "../WORKFLOW.md", "sha256": "a" * 64}), encoding="utf-8")
            with self.assertRaises(loader.CanonicalToolingGap):
                loader.read_lock(path)

    def task_fixture(self, root):
        task_path = root / "task.json"
        task_path.write_text(json.dumps({
            "schema_version": 1,
            "id": "GTW-TSK1",
            "sha256": "a" * 64,
            "acceptance_criteria": ["AC1", "AC2"],
            "required_gates": ["G1", "G2"],
        }), encoding="utf-8")
        return task_path

    def valid_receipt(self):
        return {
            "schema_version": 1,
            "run_id": "GTW-TSK1-RUN1",
            "task_sha256": "a" * 64,
            "status": "succeeded",
            "summary": "canonical tooling passed",
            "gate_results": [{"id": "G1", "exit_code": 0}, {"id": "G2", "exit_code": 0}],
            "acceptance_coverage": ["AC1", "AC2"],
            "deviations": [],
            "remaining_risks": [],
        }

    def test_completion_receipt_derives_atomic_path_and_is_idempotent(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            task_path = self.task_fixture(root)
            raw = json.dumps(self.valid_receipt()).encode()
            destination, created = receipt.prepare_receipt(root, task_path, "GTW-TSK1-RUN1", raw)
            self.assertTrue(created)
            self.assertEqual(destination, root / ".gpt/run/GTW-TSK1/run-1/completion.json")
            self.assertEqual(destination.stat().st_mode & 0o777, 0o600)
            _, created_again = receipt.prepare_receipt(root, task_path, "GTW-TSK1-RUN1", raw)
            self.assertFalse(created_again)
            changed = self.valid_receipt()
            changed["summary"] = "different"
            with self.assertRaises(receipt.CompletionReceiptError):
                receipt.prepare_receipt(root, task_path, "GTW-TSK1-RUN1", json.dumps(changed).encode())

    def test_completion_receipt_rejects_handcrafted_destination_and_invalid_order(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            task_path = self.task_fixture(root)
            bad = self.valid_receipt()
            bad["gate_results"] = [{"id": "G2", "exit_code": 0}]
            with self.assertRaises(receipt.CompletionReceiptError):
                receipt.prepare_receipt(root, task_path, "GTW-TSK1-RUN1", json.dumps(bad).encode())
            result = subprocess.run([sys.executable, str(SCRIPTS / "write-completion-receipt.py"), "--repository-root", str(root), "--task-file", str(task_path), "--run-id", "GTW-TSK1-RUN1", "--output", str(root / "manual.json")], input=json.dumps(self.valid_receipt()), text=True, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((root / "manual.json").exists())


if __name__ == "__main__":
    unittest.main()
