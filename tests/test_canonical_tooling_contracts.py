import hashlib
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SCRIPTS = ROOT / "scripts"


def load_script(name, filename):
    spec = importlib.util.spec_from_file_location(name, SCRIPTS / filename)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


loader = load_script("pinned_workflow_loader", "load-pinned-workflow.py")


class CanonicalToolingContractTests(unittest.TestCase):
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
