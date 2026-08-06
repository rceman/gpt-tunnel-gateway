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
from unittest import mock


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


class PublicationHandler(BaseHTTPRequestHandler):
    run = {}
    jobs = {}
    runs_status = 200
    jobs_status = 200
    release_status = 404
    release_payload = {"message": "not found"}
    rate_limited = False

    def do_GET(self):
        if "/actions/runs/" in self.path and "/jobs" in self.path:
            status, payload = type(self).jobs_status, type(self).jobs
        elif "/actions/runs" in self.path:
            status, payload = type(self).runs_status, {"workflow_runs": [type(self).run]}
        elif "/releases/tags/" in self.path:
            status, payload = type(self).release_status, type(self).release_payload
        else:
            status, payload = 404, {"message": "not found"}
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        if type(self).rate_limited:
            self.send_header("X-RateLimit-Remaining", "0")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


class CanonicalAgentToolingTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), PublicationHandler)
        cls.server_thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.server_thread.start()
        cls.api_url = f"http://127.0.0.1:{cls.server.server_port}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server_thread.join(timeout=5)
        cls.server.server_close()

    def setUp(self):
        PublicationHandler.release_status = 404
        PublicationHandler.release_payload = {"message": "not found"}
        PublicationHandler.runs_status = 200
        PublicationHandler.jobs_status = 200
        PublicationHandler.rate_limited = False

    def git(self, repo, *args):
        return subprocess.run(["git", "-C", str(repo), *args], check=True, capture_output=True, text=True).stdout.strip()

    def make_release_repo(self, github_release=False):
        directory = tempfile.TemporaryDirectory()
        repo = Path(directory.name) / "repo"
        remote = Path(directory.name) / "remote.git"
        repo.mkdir()
        self.git(repo, "init", "-b", "main")
        self.git(repo, "config", "user.name", "Tooling Test")
        self.git(repo, "config", "user.email", "tooling@example.invalid")
        (repo / "VERSION").write_text("9.9.9\n", encoding="utf-8")
        topology = "github_release" if github_release else "tag_only"
        (repo / "release-config.json").write_text(json.dumps({"publication": {"topology": topology, "github_release": github_release, "assets": []}}), encoding="utf-8")
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

    def test_release_topology_is_loaded_from_exact_release_commit(self):
        directory, repo, commit = self.make_release_repo()
        self.addCleanup(directory.cleanup)
        (repo / "release-config.json").write_text(json.dumps({"publication": {"topology": "github_release", "github_release": True, "assets": ["mutable.zip"]}}), encoding="utf-8")
        PublicationHandler.run = {"id": 41, "head_sha": commit, "status": "completed", "conclusion": "success", "name": "Validate", "html_url": "https://github.com/owner/repo/actions/runs/41"}
        PublicationHandler.jobs = {"total_count": 1, "jobs": [{"id": 501, "name": "unit", "status": "completed", "conclusion": "success", "html_url": "https://github.com/owner/repo/actions/runs/41/jobs/501"}]}
        result = publication.verify(self.publication_args(repo, commit))
        self.assertFalse(result["release"]["declared"])
        self.assertEqual(result["release"]["state"], "not_found_expected")

    def test_release_transport_states_are_preserved_by_endpoint(self):
        directory, repo, commit = self.make_release_repo()
        self.addCleanup(directory.cleanup)
        PublicationHandler.run = {"id": 41, "head_sha": commit, "status": "completed", "conclusion": "success", "name": "Validate", "html_url": "https://github.com/owner/repo/actions/runs/41"}
        PublicationHandler.jobs = {"total_count": 1, "jobs": [{"id": 501, "name": "unit", "status": "completed", "conclusion": "success", "html_url": "https://github.com/owner/repo/actions/runs/41/jobs/501"}]}
        for status, expected in ((503, "ci_api_failure"), (401, "authentication_failure"), (403, "rate_limited")):
            PublicationHandler.runs_status = status; PublicationHandler.rate_limited = status == 403
            with self.assertRaises(publication.PublicationError) as raised:
                publication.verify(self.publication_args(repo, commit))
            self.assertEqual(raised.exception.state, expected)
        PublicationHandler.runs_status = 200; PublicationHandler.rate_limited = False
        for status, expected in ((503, "ci_api_failure"), (401, "authentication_failure"), (403, "rate_limited")):
            PublicationHandler.jobs_status = status; PublicationHandler.rate_limited = status == 403
            with self.assertRaises(publication.PublicationError) as raised:
                publication.verify(self.publication_args(repo, commit))
            self.assertEqual(raised.exception.state, expected)
        PublicationHandler.jobs_status = 200; PublicationHandler.rate_limited = False
        for status, expected in ((503, "api_failure"), (401, "authentication_failure"), (403, "rate_limited")):
            PublicationHandler.release_status = status; PublicationHandler.rate_limited = status == 403
            with self.assertRaises(publication.PublicationError) as raised:
                publication.verify(self.publication_args(repo, commit))
            self.assertEqual(raised.exception.state, expected)

    def test_release_unavailable_transport_is_preserved_for_runs_jobs_and_release(self):
        directory, repo, commit = self.make_release_repo()
        self.addCleanup(directory.cleanup)
        PublicationHandler.run = {"id": 41, "head_sha": commit, "status": "completed", "conclusion": "success", "name": "Validate", "html_url": "https://github.com/owner/repo/actions/runs/41"}
        PublicationHandler.jobs = {"total_count": 1, "jobs": [{"id": 501, "name": "unit", "status": "completed", "conclusion": "success", "html_url": "https://github.com/owner/repo/actions/runs/41/jobs/501"}]}
        run_payload = {"workflow_runs": [PublicationHandler.run]}
        jobs_payload = PublicationHandler.jobs
        def fail_runs(url, token):
            raise publication.GitHubAPIError("unavailable", None, url, "test unavailable")
        with mock.patch.object(publication, "fetch_json", side_effect=fail_runs):
            with self.assertRaises(publication.PublicationError) as raised:
                publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "unavailable")

        def fail_jobs(url, token):
            if "/jobs" in url:
                raise publication.GitHubAPIError("unavailable", None, url, "test unavailable")
            return run_payload
        with mock.patch.object(publication, "fetch_json", side_effect=fail_jobs):
            with self.assertRaises(publication.PublicationError) as raised:
                publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "unavailable")

        def fail_release(url, token):
            if "/releases/" in url:
                raise publication.GitHubAPIError("unavailable", None, url, "test unavailable")
            if "/jobs" in url:
                return jobs_payload
            return run_payload
        with mock.patch.object(publication, "fetch_json", side_effect=fail_release):
            with self.assertRaises(publication.PublicationError) as raised:
                publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "unavailable")

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

    def test_release_publication_emits_distinct_not_found_api_and_asset_states(self):
        directory, repo, commit = self.make_release_repo(github_release=True)
        self.addCleanup(directory.cleanup)
        PublicationHandler.run = {"id": 41, "head_sha": commit, "status": "completed", "conclusion": "success", "name": "Validate", "html_url": "https://github.com/owner/repo/actions/runs/41"}
        PublicationHandler.jobs = {"total_count": 1, "jobs": [{"id": 501, "name": "unit", "status": "completed", "conclusion": "success", "html_url": "https://github.com/owner/repo/actions/runs/41/jobs/501"}]}
        with self.assertRaises(publication.PublicationError) as raised:
            publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "release_not_found")
        PublicationHandler.runs_status = 404
        with self.assertRaises(publication.PublicationError) as raised:
            publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "ci_run_not_found")
        PublicationHandler.runs_status = 200; PublicationHandler.release_status = 200; PublicationHandler.release_payload = {"tag_name": "v9.9.9", "assets": [{"name": "unexpected.zip"}]}
        with self.assertRaises(publication.PublicationError) as raised:
            publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "asset_mismatch")
        PublicationHandler.release_status = 404; PublicationHandler.release_payload = {"message": "not found"}; PublicationHandler.jobs_status = 500
        with self.assertRaises(publication.PublicationError) as raised:
            publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "ci_api_failure")

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

    def test_canonical_tooling_prohibits_direct_or_regex_proof_bypasses(self):
        tool_paths = [
            SCRIPTS / "check-github-ci.py", SCRIPTS / "github_tooling.py", SCRIPTS / "verify-release-publication.py",
            SCRIPTS / "load-pinned-workflow.py",
        ]
        for path in tool_paths:
            text = path.read_text(encoding="utf-8")
            self.assertNotIn("curl", text.lower(), path.name)
            self.assertNotIn("beautifulsoup", text.lower(), path.name)
            self.assertNotRegex(text, r"(?i)(?:run|job)[_-]?id.{0,80}re\.(?:compile|search|match|findall|finditer)", path.name)

if __name__ == "__main__":
    unittest.main()
