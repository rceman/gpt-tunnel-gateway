#!/usr/bin/env python3
"""Run every repository Go test exactly once with bounded package sharding."""

from __future__ import annotations

import os
import pathlib
import re
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed


SHARDED_PACKAGES = {
    "/internal/mcp": 8,
    "/internal/service": 16,
}
EXCLUSIVE_SERVICE_TESTS = {
    "TestOperatorConcurrentUnpinnedRecordsAllocateUniqueOrderedIDs",
    "TestOperatorConcurrentUnpinnedCorrectionsAllocateUniqueOrderedIDs",
    "TestTSK585TaskArchiveDone",
    "TestTSK585TaskHistoryMerged",
    "TestTSK585TaskReviewBases",
}
PROFILED_TEST_SECONDS = {
    "TestLocalCodeScanSafetyFailsClosedWithoutPagination": 10.28,
    "TestTSK579CodeWorktreePagesLargeManagedInventoryAndExactSelector": 7.87,
    "TestTSK571RuntimeIdentityFailsClosedOverHTTP": 6.15,
    "TestTrainV2CorrectionStartRequiresExactRejectedReviewAndQueuedTask": 5.43,
    "TestTaskAuthoringQueuedTrainItemCanBeUpdatedUntilAttemptStarts": 4.52,
    "TestTrainV2AdvanceStartsNextItemAndIsIdempotent": 4.04,
    "TestTaskFinalizeOwnsCheckpointByTaskIdentity": 3.90,
    "TestPublicCodeSearchContextLinesE2E": 3.53,
    "TestTSK610TaskCompleteReviewAuthority": 3.46,
    "TestTaskWorkStartsAndResumesByTaskIdentity": 3.43,
    "TestTaskWorkRejectsTrainRuntimeSessionMismatchWithoutLaunch": 3.19,
    "TestTSK585TaskHistoryServicePageBoundary": 3.18,
    "TestTSK585TaskBrowseOmitsDone": 3.09,
    "TestTSK585HistoricalSessionAuthority": 3.05,
    "TestPublicCodeActionsE2EPerformanceAndPagination": 3.00,
}


def run(root: pathlib.Path, command: list[str]) -> subprocess.CompletedProcess[str]:
    environment = dict(os.environ)
    if "-run" in command:
        environment["GOMAXPROCS"] = environment.get("GPT_FULL_SHARD_GOMAXPROCS", "1")
    else:
        environment["GOMAXPROCS"] = environment.get("GPT_FULL_GOMAXPROCS", "8")
    return subprocess.run(command, cwd=root, capture_output=True, text=True, env=environment)


def go_packages(root: pathlib.Path) -> list[str]:
    result = subprocess.run(["go", "list", "./..."], cwd=root, check=True, capture_output=True, text=True)
    return [line for line in result.stdout.splitlines() if line]


def package_tests(root: pathlib.Path, package: str) -> list[str]:
    result = subprocess.run(["go", "test", package, "-list", "."], cwd=root, check=True, capture_output=True, text=True)
    names = []
    for line in result.stdout.splitlines():
        if re.fullmatch(r"(?:Test|Example|Fuzz)[A-Za-z0-9_]*(?:/[^\s]+)?", line):
            names.append(line)
    if not names:
        raise RuntimeError(f"no test names discovered for {package}")
    return names


def regex_for(names: list[str]) -> str:
    return "^(" + "|".join(re.escape(name) for name in names) + ")$"


def shard_names(names: list[str], shard_count: int) -> list[list[str]]:
    shards = [[] for _ in range(min(shard_count, len(names)))]
    weights = [0.0] * len(shards)
    for name in sorted(names, key=lambda value: PROFILED_TEST_SECONDS.get(value, 1.0), reverse=True):
        index = min(range(len(shards)), key=weights.__getitem__)
        shards[index].append(name)
        weights[index] += PROFILED_TEST_SECONDS.get(name, 1.0)
    return shards


def execute(root: pathlib.Path, command: list[str]) -> tuple[list[str], int, str, str]:
    result = run(root, command)
    return command, result.returncode, result.stdout, result.stderr


def execute_concurrently(root: pathlib.Path, commands: list[list[str]]) -> list[tuple[list[str], int, str, str]]:
    failures: list[tuple[list[str], int, str, str]] = []
    with ThreadPoolExecutor(max_workers=len(commands)) as executor:
        futures = [executor.submit(execute, root, command) for command in commands]
        for future in as_completed(futures):
            command, returncode, stdout, stderr = future.result()
            if stdout:
                print(stdout, end="")
            if stderr:
                print(stderr, end="", file=sys.stderr)
            if returncode != 0:
                failures.append((command, returncode, stdout, stderr))
    return failures


def report_failures(failures: list[tuple[list[str], int, str, str]]) -> None:
    for command, returncode, _, _ in failures:
        print(f"test-full shard failed ({returncode}): {' '.join(command)}", file=sys.stderr)


def main() -> int:
    if len(sys.argv) != 1:
        print("test-full.py does not accept test-selection arguments", file=sys.stderr)
        return 2
    root = pathlib.Path.cwd().resolve()
    packages = go_packages(root)
    commands: list[list[str]] = []
    exclusive: list[str] = []
    unsharded = []
    for package in packages:
        shard_count = next((count for suffix, count in SHARDED_PACKAGES.items() if package.endswith(suffix)), None)
        if shard_count is None:
            unsharded.append(package)
            continue
        names = package_tests(root, package)
        if package.endswith("/internal/service"):
            exclusive = [name for name in names if name in EXCLUSIVE_SERVICE_TESTS]
            names = [name for name in names if name not in EXCLUSIVE_SERVICE_TESTS]
        shards = shard_names(names, shard_count)
        commands.extend(
            ["go", "test", package, "-count=1", "-run", regex_for(shard_names)]
            for shard_names in shards
        )
    if unsharded:
        commands.append(["go", "test", *unsharded, "-count=1"])

    failures = execute_concurrently(root, commands)
    if failures:
        report_failures(failures)
        return 1
    if exclusive:
        exclusive_command = ["go", "test", "./internal/service", "-count=1", "-run", regex_for(exclusive)]
        failures = execute_concurrently(root, [exclusive_command])
        if failures:
            report_failures(failures)
            return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
