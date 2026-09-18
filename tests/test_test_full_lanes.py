import importlib.util
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


full = load_script("test_full", "test-full.py")


MCP = "github.com/rceman/gpt-tunnel-gateway/internal/mcp"
SERVICE = "github.com/rceman/gpt-tunnel-gateway/internal/service"
DAEMON = "github.com/rceman/gpt-tunnel-gateway/cmd/gpt-tunnel-gatewayd"
MODEL = "github.com/rceman/gpt-tunnel-gateway/internal/model"

PACKAGES = [MCP, SERVICE, DAEMON, MODEL]
PACKAGE_TESTS = {
    MCP: ["TestAlpha", "TestBeta"],
    SERVICE: [
        "TestGamma",
        "TestOperatorConcurrentUnpinnedRecordsAllocateUniqueOrderedIDs",
        "TestTSK585TaskArchiveDone",
    ],
    DAEMON: ["TestBootstrapGTWIdentityMigrationDoesNotRequireHub"],
    MODEL: ["TestDelta"],
}


def plan_commands(argv):
    commands = []
    listed = []

    def fake_packages(root):
        return list(PACKAGES)

    def fake_tests(root, package):
        names = PACKAGE_TESTS.get(package, ["TestOther"])
        listed.append((package, list(names)))
        return list(names)

    def fake_execute(root, batch):
        commands.extend(batch)
        return []

    with patch.object(full, "go_packages", side_effect=fake_packages), patch.object(
        full, "package_tests", side_effect=fake_tests
    ), patch.object(full, "execute_concurrently", side_effect=fake_execute):
        rc = full.main(list(argv))
    return rc, commands


SPECIALIST_TOKENS = ("-race", "-tags", "livee2e", "liveperformance", "profile", "e2e")


def specialist_tokens(command):
    return [token for token in command if any(token == mark or mark in token for mark in SPECIALIST_TOKENS)]


class DeterministicFullLaneTests(unittest.TestCase):
    def test_full_lane_never_selects_race_e2e_perf_or_profile(self):
        rc, commands = plan_commands([])
        self.assertEqual(rc, 0)
        self.assertTrue(commands)
        for command in commands:
            self.assertEqual(command[:2], ["go", "test"])
            self.assertIn("-count=1", command)
            self.assertFalse(specialist_tokens(command), command)

    def test_full_lane_covers_every_discovered_test_exactly_once(self):
        rc, commands = plan_commands([])
        self.assertEqual(rc, 0)
        sharded_runs = [command for command in commands if "-run" in command]
        unsharded = [command for command in commands if "-run" not in command]
        self.assertEqual(len(unsharded), 1)
        for package in (DAEMON, MODEL):
            self.assertIn(package, unsharded[0])
        # Every sharded package test name appears inside some -run regex.
        run_regex = [command[command.index("-run") + 1] for command in sharded_runs]
        for package, names in PACKAGE_TESTS.items():
            if package in (DAEMON, MODEL):
                continue
            for name in names:
                self.assertTrue(
                    any(name in pattern for pattern in run_regex),
                    f"{name} is not covered by any sharded -run selection",
                )
        # No test name is selected by two different shards.
        selected = {}
        for pattern in run_regex:
            for name in pattern.strip("()^$").split("|"):
                selected[name] = selected.get(name, 0) + 1
        for name, count in selected.items():
            self.assertEqual(count, 1, f"{name} selected {count} times")

    def test_exclusive_service_tests_run_once_outside_the_concurrent_shards(self):
        rc, commands = plan_commands([])
        self.assertEqual(rc, 0)
        exclusive_commands = [
            command
            for command in commands
            if "-run" in command and "./internal/service" in command
        ]
        self.assertEqual(len(exclusive_commands), 1)
        exclusive_regex = exclusive_commands[0][exclusive_commands[0].index("-run") + 1]
        for name in full.EXCLUSIVE_SERVICE_TESTS & set(PACKAGE_TESTS[SERVICE]):
            self.assertIn(name, exclusive_regex)
        concurrent = [command for command in commands if command != exclusive_commands[0]]
        for name in full.EXCLUSIVE_SERVICE_TESTS:
            for pattern in [command[command.index("-run") + 1] for command in concurrent if "-run" in command]:
                self.assertNotIn(name, pattern)

    def test_unknown_argument_fails_closed(self):
        with self.assertRaises(SystemExit) as caught:
            plan_commands(["--bogus"])
        self.assertEqual(caught.exception.code, 2)


class RaceLaneTests(unittest.TestCase):
    def test_race_lane_runs_the_same_corpus_under_race_only(self):
        rc, race_commands = plan_commands(["--race"])
        self.assertEqual(rc, 0)
        self.assertTrue(race_commands)
        for command in race_commands:
            self.assertEqual(command[:2], ["go", "test"])
            self.assertIn("-race", command)
            self.assertIn("-count=1", command)
            self.assertFalse(
                [token for token in command if "-tags" in token or "livee2e" in token or "liveperformance" in token],
                command,
            )
        _, full_commands = plan_commands([])
        without_race = [[token for token in command if token != "-race"] for command in race_commands]
        self.assertEqual(without_race, full_commands)

    def test_race_lane_is_explicitly_invocable_and_rejects_selection_args(self):
        script = (ROOT / "scripts" / "test-race.sh")
        self.assertTrue(script.is_file())
        text = script.read_text()
        self.assertIn("python3 scripts/test-full.py --race", text)
        self.assertIn("does not accept test-selection arguments", text)
        result = subprocess.run(
            ["bash", "scripts/test-race.sh", "extra"], cwd=ROOT,
            capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("does not accept", result.stderr)

    def test_full_script_remains_argument_free_and_delegates_to_the_runner(self):
        text = (ROOT / "scripts" / "test-full.sh").read_text()
        self.assertIn("does not accept test-selection arguments", text)
        self.assertIn("exec python3 scripts/test-full.py", text)


class SpecialistLaneTests(unittest.TestCase):
    def test_e2e_lane_is_explicit_tagged_and_independently_runnable(self):
        script = ROOT / "scripts" / "test-e2e.sh"
        self.assertTrue(script.is_file())
        text = script.read_text()
        self.assertIn("-tags=livee2e", text)
        self.assertIn("-run '^TestCandidate'", text)
        self.assertIn("./cmd/gpt-tunnel-gatewayd", text)
        self.assertIn("-count=1", text)
        result = subprocess.run(
            ["bash", "scripts/test-e2e.sh"], cwd=ROOT,
            capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr[-2000:])
        self.assertIn("ok", result.stdout)

    def test_candidate_e2e_files_are_excluded_from_the_default_corpus(self):
        tagged = sorted((ROOT / "cmd" / "gpt-tunnel-gatewayd").glob("runtime_restart_candidate_e2e*.go"))
        self.assertGreaterEqual(len(tagged), 4)
        for path in tagged:
            self.assertTrue(
                path.read_text().startswith("//go:build livee2e\n\npackage main"),
                f"{path.name} is not behind the livee2e tag",
            )

    def test_perf_and_profile_lanes_remain_separately_runnable(self):
        perf = (ROOT / "scripts" / "test-performance.py").read_text()
        self.assertIn("-tags=liveperformance", perf)
        self.assertIn('"go"', perf)
        self.assertIn('"test"', perf)
        profile = (ROOT / "scripts" / "test-profile.py").read_text()
        self.assertIn('"go", "test", "./..."', profile)
        self.assertIn('"-json"', profile)
        self.assertNotIn("-race", profile)
        integration = ROOT / "scripts" / "test-integration-activate.sh"
        self.assertTrue(integration.is_file())

    def test_perf_files_stay_out_of_the_default_corpus(self):
        for relative in (
            "internal/service/local_code_inspection_perf_test.go",
            "internal/mcp/public_code_performance_test.go",
        ):
            self.assertTrue(
                (ROOT / relative).read_text().startswith("//go:build liveperformance\n\n"),
                f"{relative} lost its liveperformance tag",
            )


if __name__ == "__main__":
    unittest.main()
