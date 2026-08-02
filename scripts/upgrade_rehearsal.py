#!/usr/bin/env python3
"""Validate the sanitized previous-version upgrade rehearsal matrix.

The executable rehearsal is intentionally data-only: it proves that the
production-like v0.2.2 incompatibilities are represented without touching a
runtime, hub, repository, or secret.
"""
from __future__ import annotations

import json
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
FIXTURE = ROOT / "testdata" / "upgrades" / "v0.2.2"
REQUIRED_CASES = 25


def main() -> int:
    matrix = json.loads((FIXTURE / "matrix.json").read_text(encoding="utf-8"))
    assert matrix["source_version"] == "0.2.2"
    assert len(matrix["cases"]) == REQUIRED_CASES
    for project in matrix["legacy_plans"]:
        plan = json.loads((FIXTURE / "plans" / project / "current.json").read_text(encoding="utf-8"))
        assert "body" in plan
    assert (FIXTURE / "runs" / "history-only-run.json").is_file()
    assert (FIXTURE / "tasks" / "dispatched-with-run.json").is_file()
    assert (FIXTURE / "tasks" / "terminal.json").is_file()
    history_only = matrix["history_only_dispatched_state"]
    assert history_only["task_state"] == "dispatched"
    assert history_only["run_shape"] == "HistoricalRunV1"
    assert history_only["repaired_task_state"] == "cancelled"
    for relative in ("plan.json", "task.json", "task.state.json", "run.json"):
        assert (FIXTURE / "state-repair" / relative).is_file()
    print(f"UPGRADE_REHEARSAL_MATRIX_OK cases={len(matrix['cases'])} source={matrix['source_version']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
