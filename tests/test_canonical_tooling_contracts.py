import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SCRIPTS = ROOT / "scripts"


class CanonicalToolingContractTests(unittest.TestCase):
    def test_external_workflow_bootstrap_is_retired(self):
        lock_name = "." + "gpt-workflow.lock"
        loader_name = "load-" + "pinned-workflow.py"
        planner_commit = "900d284a97dd745d079134b49e5654b" + "909e88c0a"
        self.assertFalse((ROOT / lock_name).exists())
        self.assertFalse((SCRIPTS / loader_name).exists())
        for path in ROOT.rglob("*"):
            if not path.is_file() or ".git" in path.parts:
                continue
            try:
                text = path.read_text(encoding="utf-8")
            except UnicodeDecodeError:
                continue
            self.assertNotIn(lock_name, text, str(path.relative_to(ROOT)))
            self.assertNotIn(loader_name, text, str(path.relative_to(ROOT)))
            self.assertNotIn(planner_commit, text, str(path.relative_to(ROOT)))

    def test_canonical_tooling_prohibits_direct_or_regex_proof_bypasses(self):
        tool_paths = [
            SCRIPTS / "check-github-ci.py",
            SCRIPTS / "github_tooling.py",
            SCRIPTS / "verify-release-publication.py",
        ]
        for path in tool_paths:
            text = path.read_text(encoding="utf-8")
            self.assertNotIn("curl", text.lower(), path.name)
            self.assertNotIn("beautifulsoup", text.lower(), path.name)
            self.assertNotRegex(text, r"(?i)(?:run|job)[_-]?id.{0,80}re\.(?:compile|search|match|findall|finditer)", path.name)


if __name__ == "__main__":
    unittest.main()
