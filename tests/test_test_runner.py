from __future__ import annotations

import importlib.util
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("canonical_test_runner", ROOT / "scripts" / "test.py")
assert SPEC is not None and SPEC.loader is not None
RUNNER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RUNNER)


class CanonicalTestRunnerTests(unittest.TestCase):
    def test_full_command_is_fresh_and_uses_go_schedulers(self) -> None:
        args = RUNNER.build_parser().parse_args(["full", "--jobs", "3", "--parallel", "2"])
        self.assertEqual(
            RUNNER.command(args),
            ["go", "test", "-count=1", "-p=3", "-parallel=2", "./..."],
        )

    def test_service_command_keeps_scope_without_regex_sharding(self) -> None:
        args = RUNNER.build_parser().parse_args(["service", "--jobs", "2", "--race"])
        self.assertEqual(
            RUNNER.command(args),
            ["go", "test", "-count=1", "-p=2", "-parallel=2", "-race", "./internal/service"],
        )


if __name__ == "__main__":
    unittest.main()
