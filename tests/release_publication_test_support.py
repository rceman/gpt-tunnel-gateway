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


class PublicationTestCase(unittest.TestCase):
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
