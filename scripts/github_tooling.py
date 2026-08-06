"""Shared, bounded GitHub Actions transport and job-proof validation.

This module is intentionally repository-owned so CI and release publication
verification use the same authenticated API path and fail-closed error types.
It never scrapes HTML and never exposes response bodies in errors.
"""
from __future__ import annotations

import json
from dataclasses import dataclass
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


class JobSetMismatch(ValueError):
    """The API response cannot prove the complete associated job set."""


MAX_RESPONSE_BYTES = 4 * 1024 * 1024


def _unique_pairs(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON field: {key}")
        result[key] = value
    return result


def _reject_constant(value: str) -> None:
    raise ValueError(f"non-finite JSON value: {value}")


@dataclass
class GitHubAPIError(RuntimeError):
    state: str
    status_code: int | None
    url: str
    detail: str

    def __str__(self) -> str:
        suffix = f" HTTP {self.status_code}" if self.status_code is not None else ""
        return f"{self.state}{suffix} for {self.url}: {self.detail}"


def fetch_json(url: str, token: str | None) -> object:
    headers = {
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        "Cache-Control": "no-cache",
        "Pragma": "no-cache",
        "User-Agent": "gpt-tunnel-gateway-canonical-tooling",
    }
    if token:
        headers["Authorization"] = f"Bearer {token}"
    try:
        with urlopen(Request(url, headers=headers), timeout=30) as response:
            raw = response.read(MAX_RESPONSE_BYTES + 1)
            if len(raw) > MAX_RESPONSE_BYTES:
                raise GitHubAPIError("api_failure", None, url, "GitHub API response exceeds bounded size")
            return json.loads(raw.decode("utf-8"), object_pairs_hook=_unique_pairs, parse_constant=_reject_constant)
    except HTTPError as exc:
        remaining = exc.headers.get("X-RateLimit-Remaining", "")
        if exc.code == 429 or (exc.code == 403 and remaining == "0"):
            state = "rate_limited"
        elif exc.code in {401, 403}:
            state = "authentication_failure"
        elif exc.code == 404:
            state = "not_found"
        else:
            state = "api_failure"
        exc.close()
        raise GitHubAPIError(state, exc.code, url, "GitHub API request failed") from exc
    except (URLError, OSError, TimeoutError) as exc:
        raise GitHubAPIError("unavailable", None, url, "GitHub API request unavailable") from exc
    except (json.JSONDecodeError, ValueError) as exc:
        raise GitHubAPIError("api_failure", None, url, "GitHub API returned invalid JSON") from exc


def complete_job_set(payload: object) -> list[dict[str, object]]:
    """Return normalized jobs only when the response proves completeness."""
    if not isinstance(payload, dict):
        raise JobSetMismatch("jobs response is not an object")
    total = payload.get("total_count")
    jobs = payload.get("jobs")
    if isinstance(total, bool) or not isinstance(total, int) or total < 1:
        raise JobSetMismatch("jobs response has no positive total_count")
    if not isinstance(jobs, list) or total != len(jobs):
        raise JobSetMismatch("jobs response is incomplete")
    normalized: list[dict[str, object]] = []
    seen: set[int] = set()
    for item in jobs:
        if not isinstance(item, dict):
            raise JobSetMismatch("jobs response contains a non-object job")
        job_id = item.get("id")
        name = item.get("name")
        url = item.get("html_url")
        status = item.get("status")
        conclusion = item.get("conclusion")
        if (
            isinstance(job_id, bool)
            or not isinstance(job_id, int)
            or job_id < 1
            or job_id in seen
            or not isinstance(name, str)
            or not name.strip()
            or not isinstance(url, str)
            or not url.startswith("https://")
            or not isinstance(status, str)
            or not status.strip()
            or conclusion is not None and not isinstance(conclusion, str)
        ):
            raise JobSetMismatch("jobs response contains an invalid or duplicate job identity")
        seen.add(job_id)
        normalized.append(
            {
                "id": job_id,
                "name": name,
                "url": url,
                "status": status,
                "conclusion": conclusion,
            }
        )
    return sorted(normalized, key=lambda item: int(item["id"]))


def run_identity(run: object, expected_sha: str) -> dict[str, object]:
    if not isinstance(run, dict):
        raise ValueError("workflow run is not an object")
    if run.get("head_sha") != expected_sha:
        raise ValueError("workflow run head SHA does not match requested SHA")
    run_id = run.get("id")
    if isinstance(run_id, bool) or not isinstance(run_id, int) or run_id < 1:
        raise ValueError("workflow run has an invalid ID")
    url = run.get("html_url")
    if not isinstance(url, str) or not url.startswith("https://"):
        raise ValueError("workflow run has an invalid URL")
    name = run.get("name") or run.get("path")
    if not isinstance(name, str) or not name.strip():
        raise ValueError("workflow run has no workflow identity")
    return {
        "id": run_id,
        "url": url,
        "workflow": name,
        "workflow_path": run.get("path"),
        "event": run.get("event"),
        "branch": run.get("head_branch"),
        "status": run.get("status"),
        "conclusion": run.get("conclusion"),
        "head_sha": run.get("head_sha"),
    }
