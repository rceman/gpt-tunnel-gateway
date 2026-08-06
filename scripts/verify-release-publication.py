#!/usr/bin/env python3
"""Verify a tag-only or declared GitHub Release publication end to end."""
from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from pathlib import PurePosixPath

from github_tooling import GitHubAPIError, JobSetMismatch, complete_job_set, fetch_json, run_identity


SHA_RE = re.compile(r"^[0-9a-f]{40}$")
REPOSITORY_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
TAG_RE = re.compile(r"^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$")


class PublicationError(ValueError):
    def __init__(self, state: str, message: str) -> None:
        super().__init__(message)
        self.state = state


def unique_pairs(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise PublicationError("api_failure", f"duplicate JSON field: {key}")
        result[key] = value
    return result


def parse_object(raw: str, label: str) -> dict[str, object]:
    try:
        value = json.loads(raw, object_pairs_hook=unique_pairs)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise PublicationError("api_failure", f"invalid JSON: {label}") from exc
    if not isinstance(value, dict):
        raise PublicationError("api_failure", f"JSON object required: {label}")
    return value


def read_object(path: Path) -> dict[str, object]:
    if path.is_symlink() or not path.is_file():
        raise PublicationError("api_failure", f"not a regular file: {path}")
    try:
        raw = path.read_text(encoding="utf-8")
    except OSError as exc:
        raise PublicationError("api_failure", f"invalid JSON: {path}") from exc
    return parse_object(raw, str(path))


def git(repo: Path, *args: str) -> str:
    result = subprocess.run(["git", "-C", str(repo), *args], text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    if result.returncode != 0:
        raise PublicationError("git_ref_mismatch", result.stderr.strip() or "Git proof command failed")
    return result.stdout.strip()


def prove_git(repo: Path, remote: str, tag: str, commit: str) -> tuple[str, str]:
    try:
        if git(repo, "rev-parse", "--verify", f"{commit}^{{commit}}") != commit:
            raise PublicationError("release_commit_mismatch", "requested release commit does not resolve exactly")
        if git(repo, "cat-file", "-t", tag) != "tag":
            raise PublicationError("tag_mismatch", "release tag is not annotated")
        tag_object = git(repo, "rev-parse", f"{tag}^{{tag}}")
        peeled = git(repo, "rev-parse", f"{tag}^{{commit}}")
        if not SHA_RE.fullmatch(tag_object) or peeled != commit:
            raise PublicationError("tag_mismatch", "annotated tag object or peeled commit mismatch")
        remote_lines = git(repo, "ls-remote", remote, f"refs/tags/{tag}", f"refs/tags/{tag}^{{}}")
    except PublicationError:
        raise
    remote_refs = {}
    for line in remote_lines.splitlines():
        fields = line.split()
        if len(fields) == 2:
            remote_refs[fields[1]] = fields[0]
    if remote_refs.get(f"refs/tags/{tag}") != tag_object or remote_refs.get(f"refs/tags/{tag}^{{}}") != commit:
        raise PublicationError("tag_mismatch", "authoritative remote tag does not match local annotated tag")
    return tag_object, peeled


def publication_config(config: dict[str, object]) -> tuple[bool, list[str]]:
    publication = config.get("publication")
    if not isinstance(publication, dict):
        raise PublicationError("api_failure", "release-config.json lacks publication declaration")
    if publication.get("topology") not in {"tag_only", "github_release"}:
        raise PublicationError("api_failure", "publication.topology is invalid")
    github_release = publication.get("github_release")
    assets = publication.get("assets")
    if not isinstance(github_release, bool) or not isinstance(assets, list) or not all(isinstance(item, str) and item for item in assets) or len(set(assets)) != len(assets):
        raise PublicationError("api_failure", "publication declaration is invalid")
    if publication["topology"] == "tag_only" and github_release:
        raise PublicationError("api_failure", "tag_only publication cannot require a GitHub Release")
    if publication["topology"] == "github_release" and not github_release:
        raise PublicationError("api_failure", "github_release topology must require a GitHub Release")
    return github_release, assets


def api_error(exc: GitHubAPIError, context: str) -> PublicationError:
    state = exc.state
    if context == "ci" and state == "not_found":
        state = "ci_run_not_found"
    elif context == "ci" and state == "api_failure":
        state = "ci_api_failure"
    elif context == "jobs" and state == "not_found":
        state = "ci_job_set_mismatch"
    elif context == "jobs" and state == "api_failure":
        state = "ci_api_failure"
    if context == "release" and state == "not_found":
        state = "release_not_found"
    return PublicationError(state, str(exc))


def publication_config_from_commit(repo: Path, commit: str, config_path: str) -> tuple[bool, list[str]]:
    relative = PurePosixPath(config_path)
    if relative.is_absolute() or not config_path or ".." in relative.parts or "\\" in config_path:
        raise PublicationError("release_commit_mismatch", "publication config path must be a safe relative path")
    try:
        raw = git(repo, "show", f"{commit}:{config_path}")
    except PublicationError as exc:
        raise PublicationError("release_commit_mismatch", "publication config is unavailable in the exact release commit") from exc
    return publication_config(parse_object(raw, f"{commit}:{config_path}"))


def verify(args: argparse.Namespace) -> dict[str, object]:
    if not REPOSITORY_RE.fullmatch(args.repository) or not SHA_RE.fullmatch(args.commit) or not TAG_RE.fullmatch(args.tag):
        raise PublicationError("api_failure", "repository, commit, or tag format is invalid")
    repo = Path(args.repository_root).resolve()
    tag_object, peeled = prove_git(repo, args.remote, args.tag, args.commit)
    github_release_expected, expected_assets = publication_config_from_commit(repo, args.commit, args.config)
    token = os.environ.get("GITHUB_TOKEN")
    base = args.api_url.rstrip("/")
    runs_url = f"{base}/repos/{args.repository}/actions/runs?head_sha={args.commit}&per_page=100"
    try:
        runs_payload = fetch_json(runs_url, token)
    except GitHubAPIError as exc:
        raise api_error(exc, "ci") from exc
    if not isinstance(runs_payload, dict) or not isinstance(runs_payload.get("workflow_runs"), list):
        raise PublicationError("ci_api_failure", "workflow run response is malformed")
    candidates = [run for run in runs_payload["workflow_runs"] if isinstance(run, dict) and run.get("head_sha") == args.commit]
    if not candidates:
        raise PublicationError("ci_run_not_found", "no exact-SHA workflow run was found")
    candidates.sort(key=lambda item: (str(item.get("created_at", "")), int(item.get("id", 0)) if isinstance(item.get("id"), int) else 0), reverse=True)
    try:
        run = run_identity(candidates[0], args.commit)
    except ValueError as exc:
        raise PublicationError("ci_api_failure", str(exc)) from exc
    try:
        jobs_payload = fetch_json(f"{base}/repos/{args.repository}/actions/runs/{run['id']}/jobs?per_page=100", token)
        jobs = complete_job_set(jobs_payload)
    except GitHubAPIError as exc:
        raise api_error(exc, "jobs") from exc
    except JobSetMismatch as exc:
        raise PublicationError("ci_job_set_mismatch", str(exc)) from exc
    if run["status"] != "completed" or run["conclusion"] != "success" or any(job["status"] != "completed" or job["conclusion"] != "success" for job in jobs):
        raise PublicationError("ci_job_set_mismatch", "exact-SHA run or job set is not completely successful")

    release_url = f"{base}/repos/{args.repository}/releases/tags/{args.tag}"
    release: dict[str, object] = {"declared": github_release_expected, "found": False, "assets": [], "state": "not_found_expected"}
    try:
        release_payload = fetch_json(release_url, token)
    except GitHubAPIError as exc:
        if exc.state == "not_found" and not github_release_expected:
            release["state"] = "not_found_expected"
        else:
            raise api_error(exc, "release") from exc
    else:
        if not isinstance(release_payload, dict) or release_payload.get("tag_name") != args.tag or not isinstance(release_payload.get("assets"), list):
            raise PublicationError("api_failure", "GitHub Release response is malformed")
        assets = release_payload["assets"]
        raw_names = [asset.get("name") for asset in assets] if all(isinstance(asset, dict) for asset in assets) else []
        if len(raw_names) != len(assets) or any(not isinstance(name, str) or not name for name in raw_names):
            raise PublicationError("asset_mismatch", "GitHub Release contains malformed assets")
        names = sorted(raw_names)
        if not github_release_expected:
            raise PublicationError("asset_mismatch", "tag-only publication unexpectedly has a GitHub Release")
        if names != sorted(expected_assets):
            raise PublicationError("asset_mismatch", "GitHub Release assets do not match declaration")
        release = {"declared": True, "found": True, "assets": names, "state": "verified"}
    return {
        "status": "success",
        "state": "success",
        "repository": args.repository,
        "release_commit": args.commit,
        "tag": args.tag,
        "tag_object": tag_object,
        "peeled_commit": peeled,
        "ci_run": run,
        "jobs": jobs,
        "release": release,
        "provenance": {"remote": args.remote, "api_base": base, "config": args.config},
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--repository-root", default=".")
    parser.add_argument("--remote", default="origin")
    parser.add_argument("--config", default="release-config.json")
    parser.add_argument("--api-url", default="https://api.github.com")
    parser.add_argument("--format", choices=["json"], default="json")
    args = parser.parse_args(argv)
    try:
        print(json.dumps(verify(args), sort_keys=True))
        return 0
    except (OSError, PublicationError) as exc:
        state = exc.state if isinstance(exc, PublicationError) else "api_failure"
        print(json.dumps({"status": "BLOCKED_CANONICAL_TOOLING_GAP", "state": state, "error": str(exc)}, sort_keys=True))
        return 4


if __name__ == "__main__":
    sys.exit(main())
