from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
FORMAT_SCRIPT = ROOT / "scripts/check-go-format.sh"


class CIFormatGateTests(unittest.TestCase):
    def git(self, repo: Path, *args: str) -> None:
        subprocess.run(["git", "-C", str(repo), *args], check=True, capture_output=True, text=True)

    def test_shallow_missing_parent_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            repo = Path(directory)
            self.git(repo, "init", "-q")
            self.git(repo, "config", "user.name", "CI format test")
            self.git(repo, "config", "user.email", "ci-format@example.invalid")
            (repo / "README.md").write_text("format fixture\n", encoding="utf-8")
            self.git(repo, "add", "README.md")
            self.git(repo, "commit", "-qm", "base")

            result = subprocess.run(
                ["bash", str(FORMAT_SCRIPT), "HEAD^"],
                cwd=repo,
                capture_output=True,
                text=True,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("unavailable comparison base: HEAD^", result.stderr)

    def test_workflow_fetches_comparison_parent(self) -> None:
        workflow = (ROOT / ".github/workflows/ci.yml").read_text(encoding="utf-8")
        self.assertIn(
            "      - uses: actions/checkout@v4\n        with:\n          fetch-depth: 2\n",
            workflow,
        )


if __name__ == "__main__":
    unittest.main()
