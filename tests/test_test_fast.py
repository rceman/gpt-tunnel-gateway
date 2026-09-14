import importlib.util
import os
import subprocess
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]


def load_script(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / "scripts" / filename)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


fast = load_script("test_fast", "test-fast.py")


class FastRunnerTests(unittest.TestCase):
    def test_default_base_prefers_origin_main_and_environment_overrides(self):
        calls = []

        def fake_run(argv, **kwargs):
            calls.append(argv)
            return SimpleNamespace(returncode=0)

        with patch.dict(os.environ, {}, clear=True), patch.object(fast.subprocess, "run", side_effect=fake_run):
            self.assertEqual(fast.default_base(ROOT), "origin/main")
        self.assertEqual(calls[0], ["git", "rev-parse", "--verify", "origin/main"])

        with patch.dict(os.environ, {"GPT_TEST_BASE": "task-base"}, clear=True), patch.object(fast.subprocess, "run") as mocked:
            self.assertEqual(fast.default_base(ROOT), "task-base")
        mocked.assert_not_called()

    def test_default_base_falls_back_to_head_without_a_resolvable_base(self):
        def missing(*args, **kwargs):
            return SimpleNamespace(returncode=1)

        with patch.dict(os.environ, {}, clear=True), patch.object(fast.subprocess, "run", side_effect=missing):
            self.assertEqual(fast.default_base(ROOT), "HEAD")

    def test_changed_files_uses_explicit_base_and_merges_working_and_staged_names(self):
        outputs = iter([
            "internal/service/example.go\n",
            "scripts/test-fast.py\n",
            "docs/TEST_GATE_SEPARATION.md\n",
        ])
        calls = []

        def fake_run(argv, **kwargs):
            calls.append(argv)
            return subprocess.CompletedProcess(argv, 0, stdout=next(outputs), stderr="")

        with patch.object(fast.subprocess, "run", side_effect=fake_run):
            changed = fast.changed_files(ROOT, "task-base")

        self.assertEqual(changed, ["docs/TEST_GATE_SEPARATION.md", "internal/service/example.go", "scripts/test-fast.py"])
        self.assertEqual(calls[0], ["git", "diff", "--name-only", "task-base...HEAD"])
        self.assertEqual(calls[1], ["git", "diff", "--name-only"])
        self.assertEqual(calls[2], ["git", "diff", "--cached", "--name-only"])


if __name__ == "__main__":
    unittest.main()
