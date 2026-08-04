#!/usr/bin/env python3
"""Capability-aware, exact-SHA GitHub Actions status check."""
from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


SHA_RE = re.compile(r"^[0-9a-f]{40}$")
REPOSITORY_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
STATES = {
    "success", "pending", "failed", "cancelled", "timed_out", "action_required",
    "neutral", "skipped", "no_run", "unavailable", "not_applicable", "invalid_response",
}
EXIT = {
    "success": 0,
    "not_applicable": 0,
    "pending": 2,
    "failed": 3,
    "cancelled": 3,
    "timed_out": 3,
    "action_required": 3,
    "neutral": 3,
    "skipped": 3,
    "no_run": 0,
    "unavailable": 0,
    "invalid_response": 5,
}


def result(args: argparse.Namespace, state: str, message: str, *, quiet: bool = False, **values: object) -> dict[str, object]:
    blocking = (
        state in {"failed", "cancelled", "timed_out", "action_required", "neutral", "skipped", "invalid_response"}
        or (state in {"no_run", "unavailable"} and args.policy == "required")
        or state == "pending"
    )
    data: dict[str, object] = {
        "schema_version": 1,
        "repository": args.repository,
        "sha": args.sha,
        "policy": args.policy,
        "state": state,
        "blocking": blocking,
        "source": "policy" if state == "not_applicable" else "github-actions-rest",
        "run_id": None,
        "job_id": None,
        "run_url": None,
        "job_url": None,
        "workflow": None,
        "event": None,
        "status": None,
        "conclusion": None,
        "checked_sha": None,
        "message": message,
    }
    data.update(values)
    if not quiet:
        if args.format == "json":
            print(json.dumps(data, sort_keys=True))
        else:
            print(f"{state}: {message}")
    return data


def fetch(url: str, token: str | None) -> object:
    headers = {
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        "Cache-Control": "no-cache",
        "Pragma": "no-cache",
        "User-Agent": "gpt-review-planner-ci-check",
    }
    if token:
        headers["Authorization"] = f"Bearer {token}"
    with urlopen(Request(url, headers=headers), timeout=30) as response:
        return json.loads(response.read().decode("utf-8"))


def resolve_head_sha() -> str:
    completed = subprocess.run(
        ["git", "rev-parse", "--verify", "HEAD^{commit}"],
        cwd=Path.cwd(),
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if completed.returncode != 0:
        raise ValueError(completed.stderr.strip() or "git could not resolve HEAD^{commit}")
    sha = completed.stdout.strip()
    if not SHA_RE.fullmatch(sha):
        raise ValueError("git returned a non-canonical commit SHA")
    return sha


def classify(run: dict[str, object]) -> str:
    if run.get("status") != "completed":
        return "pending"
    conclusion = run.get("conclusion")
    if conclusion == "success":
        return "success"
    if conclusion == "cancelled":
        return "cancelled"
    if conclusion == "timed_out":
        return "timed_out"
    if conclusion == "action_required":
        return "action_required"
    if conclusion in {"neutral", "skipped"}:
        return str(conclusion)
    return "failed"


def run_sort_key(item: dict[str, object]) -> tuple[str, int]:
    raw_id = item.get("id")
    numeric_id = raw_id if isinstance(raw_id, int) and not isinstance(raw_id, bool) else 0
    return str(item.get("created_at", "")), numeric_id


def invalid(args: argparse.Namespace, message: str) -> int:
    result(args, "invalid_response", message)
    return 5


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--sha")
    parser.add_argument("--sha-from-git")
    parser.add_argument("--policy", choices=["auto", "required", "optional", "disabled"], default="auto")
    parser.add_argument("--format", choices=["text", "json"], default="text")
    parser.add_argument("--wait", action="store_true")
    parser.add_argument("--timeout", type=int, default=1800)
    parser.add_argument("--interval", type=int, default=15)
    parser.add_argument("--api-url", default="https://api.github.com")
    parser.add_argument("--workflow")
    parser.add_argument("--event", default="push")
    args = parser.parse_args(argv)
    args.sha = args.sha or ""

    if bool(args.sha) == bool(args.sha_from_git):
        return invalid(args, "exactly one of --sha or --sha-from-git is required")
    if args.sha_from_git is not None:
        if args.sha_from_git != "HEAD":
            return invalid(args, "--sha-from-git accepts only HEAD")
        try:
            args.sha = resolve_head_sha()
        except (OSError, ValueError) as exc:
            return invalid(args, f"could not resolve HEAD^{{commit}}: {exc}")
    if not REPOSITORY_RE.fullmatch(args.repository) or not SHA_RE.fullmatch(args.sha):
        return invalid(args, "repository or SHA format is invalid")
    if args.timeout <= 0 or args.interval < 1:
        return invalid(args, "timeout must be positive and interval at least 1")
    if args.policy == "disabled":
        result(args, "not_applicable", "remote CI is disabled by policy")
        return 0

    token = os.environ.get("GITHUB_TOKEN")
    base = args.api_url.rstrip("/")
    runs_url = f"{base}/repos/{args.repository}/actions/runs?head_sha={args.sha}&per_page=100"
    deadline = time.monotonic() + args.timeout
    while True:
        try:
            payload = fetch(runs_url, token)
        except HTTPError as exc:
            state = "unavailable" if exc.code in {401, 403, 404} else "invalid_response"
            result(args, state, f"GitHub Actions query returned HTTP {exc.code}")
            if state == "unavailable":
                return 4 if args.policy == "required" else 0
            return 5
        except (URLError, OSError, json.JSONDecodeError) as exc:
            result(args, "unavailable", f"GitHub Actions query unavailable: {exc}")
            return 4 if args.policy == "required" else 0
        if not isinstance(payload, dict) or not isinstance(payload.get("workflow_runs"), list):
            return invalid(args, "GitHub Actions response has invalid workflow_runs")
        candidates = [
            run for run in payload["workflow_runs"]
            if isinstance(run, dict)
            and run.get("head_sha") == args.sha
            and (not args.workflow or args.workflow in str(run.get("name")) or args.workflow in str(run.get("path")))
            and (not args.event or not run.get("event") or run.get("event") == args.event)
        ]
        if not candidates:
            if args.wait and time.monotonic() < deadline:
                time.sleep(min(args.interval, max(0, deadline - time.monotonic())))
                continue
            if args.wait:
                result(args, "timed_out", "timed out waiting for a matching exact-SHA workflow run")
                return 6
            result(args, "no_run", "no matching exact-SHA workflow run was found")
            return 0 if args.policy in {"auto", "optional"} else 4

        run = sorted(candidates, key=run_sort_key, reverse=True)[0]
        state = classify(run)
        values: dict[str, object] = {
            "run_id": run.get("id"),
            "run_url": run.get("html_url"),
            "workflow": run.get("name") or run.get("path"),
            "event": run.get("event"),
            "status": run.get("status"),
            "conclusion": run.get("conclusion"),
            "checked_sha": run.get("head_sha"),
        }
        try:
            jobs = fetch(f"{base}/repos/{args.repository}/actions/runs/{run['id']}/jobs?per_page=100", token)
        except (HTTPError, URLError, OSError, json.JSONDecodeError, KeyError) as exc:
            message = f"exact-SHA run {run.get('id')} resolved as {state}; job metadata unavailable: {exc}"
            result(args, state, message, quiet=state == "pending" and args.wait, **values)
            if state == "pending" and args.wait and time.monotonic() < deadline:
                time.sleep(min(args.interval, max(0, deadline - time.monotonic())))
                continue
            if state == "pending" and args.wait:
                return 6
            return EXIT[state]
        if not isinstance(jobs, dict) or not isinstance(jobs.get("jobs"), list):
            message = f"exact-SHA run {run.get('id')} resolved as {state}; job metadata unavailable: malformed jobs response"
            result(args, state, message, quiet=state == "pending" and args.wait, **values)
            if state == "pending" and args.wait and time.monotonic() < deadline:
                time.sleep(min(args.interval, max(0, deadline - time.monotonic())))
                continue
            if state == "pending" and args.wait:
                return 6
            return EXIT[state]
        job = (jobs.get("jobs") or [None])[0]
        if isinstance(job, dict):
            values.update(job_id=job.get("id"), job_url=job.get("html_url"))
        result(args, state, f"exact-SHA run {run.get('id')} is {state}", quiet=state == "pending" and args.wait, **values)
        if state == "pending" and args.wait and time.monotonic() < deadline:
            time.sleep(min(args.interval, max(0, deadline - time.monotonic())))
            continue
        if state == "pending" and args.wait:
            return 6
        return EXIT.get(state, 5)


if __name__ == "__main__":
    sys.exit(main())
