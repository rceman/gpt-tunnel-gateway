#!/usr/bin/env python3
"""GTW-owned E2E Planner authority Procedure (GTW-TSK693, JRN25).

Bounded cross-project relay: the Planner supplies an approved exact Track
review snapshot for a clean-room target daemon reachable only on loopback.
The script validates the target Planner Session (exists, role=planner, exact
project), rereads the Track and requires review_pending plus the exact
revision/head/tree/submitted_by/submitted_at and pinned member revisions,
invokes track/accept through that Planner Session only on a full match,
rereads the accepted result, and writes a typed receipt.

Not a generic action runner: the only operation is track_accept, the only
authority is the supplied target Planner Session, and every supplied fact is
verified against live target state before any mutation. Any drift fails
closed without acceptance.
"""
import ipaddress
import json
import os
import sys
import urllib.error
import urllib.request

MAX_REQUEST_BYTES = 1 << 20
MAX_RESPONSE_BYTES = 1 << 20
REQUEST_TIMEOUT_SECONDS = 15


def fail(message: str) -> int:
    print(f"e2e: {message}", file=sys.stderr)
    return 1


def read_input() -> dict:
    path = os.environ.get("GTW_PROCEDURE_INPUT_FILE", "")
    if not path:
        raise RuntimeError("GTW_PROCEDURE_INPUT_FILE is not set")
    with open(path, "rb") as handle:
        data = handle.read(MAX_REQUEST_BYTES + 1)
    if len(data) > MAX_REQUEST_BYTES:
        raise RuntimeError("procedure input exceeds bounds")
    value = json.loads(data)
    if not isinstance(value, dict):
        raise RuntimeError("procedure input is not an object")
    # The executor writes a ProcedureInputEnvelope: {input, context} where
    # context carries project/session/operation/configuration_revision.
    context = value.get("context")
    if not isinstance(context, dict) or not context.get("session") or not context.get("project"):
        raise RuntimeError("procedure input envelope context is missing")
    inner = value.get("input")
    if not isinstance(inner, dict):
        raise RuntimeError("procedure input envelope input is missing")
    return inner


def write_output(value: dict) -> None:
    path = os.environ.get("GTW_PROCEDURE_OUTPUT_FILE", "")
    if not path:
        raise RuntimeError("GTW_PROCEDURE_OUTPUT_FILE is not set")
    encoded = json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    with open(path, "wb") as handle:
        handle.write(encoded)


def parse_listen_addr(value: str) -> tuple[str, int]:
    """Return (host, port) only for a loopback target; reject everything else."""
    host, sep, port_text = value.rpartition(":")
    if not sep or not host or not port_text.isdigit():
        raise RuntimeError("listen_addr must be host:port")
    port = int(port_text)
    if not 1 <= port <= 65535:
        raise RuntimeError("listen_addr port is out of range")
    candidate = host.strip("[]")
    try:
        parsed = ipaddress.ip_address(candidate)
        if not parsed.is_loopback:
            raise RuntimeError("listen_addr is not loopback")
    except ValueError:
        if candidate.lower() not in ("localhost",):
            raise RuntimeError("listen_addr is not a loopback address")
    return candidate, port


def mcp_call(base: str, session: str, action: str, tool_input: dict) -> dict:
    envelope = {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "tools/call",
        "params": {
            "name": "call",
            "arguments": {"session": session, "action": action, "input": tool_input},
        },
    }
    body = json.dumps(envelope).encode()
    if len(body) > MAX_REQUEST_BYTES:
        raise RuntimeError("MCP request exceeds bounds")
    request = urllib.request.Request(
        base,
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=REQUEST_TIMEOUT_SECONDS) as response:
            raw = response.read(MAX_RESPONSE_BYTES + 1)
    except urllib.error.URLError as err:
        raise RuntimeError(f"target daemon request failed: {err}") from err
    if len(raw) > MAX_RESPONSE_BYTES:
        raise RuntimeError("MCP response exceeds bounds")
    outer = json.loads(raw)
    if "error" in outer and outer["error"]:
        raise RuntimeError(f"target daemon rejected {action}: {outer['error'].get('message')}")
    result = outer.get("result") or {}
    content = result.get("structuredContent")
    if not isinstance(content, dict):
        texts = [c.get("text", "") for c in result.get("content", []) if isinstance(c, dict)]
        content = json.loads(texts[0]) if texts and texts[0] else {}
    if content.get("ok") is not True:
        detail = content.get("error")
        raise RuntimeError(f"{action} failed: {detail if isinstance(detail, dict) else detail or content}")
    return content["result"]


def require_equal(field: str, actual, expected) -> None:
    if actual != expected:
        raise RuntimeError(f"Track review snapshot drift on {field}: expected {expected!r}, got {actual!r}")


def compact_fingerprint(value: str, field: str) -> str:
    if not isinstance(value, str) or len(value) != 8 or any(c not in "0123456789abcdef" for c in value):
        raise RuntimeError(f"{field} is not a compact 8-hex Git fingerprint")
    return value


def verify_snapshot(track_view: dict, want: dict, pending: bool) -> dict:
    """Require the stored review to match the approved snapshot exactly."""
    require_equal("key", track_view.get("key"), want["target_track"])
    revision = track_view.get("revision")
    if pending:
        require_equal("revision", revision, want["track_revision"])
    elif not isinstance(revision, int) or revision < want["track_revision"]:
        raise RuntimeError(
            f"Track review snapshot drift on revision: expected >= {want['track_revision']!r}, got {revision!r}"
        )
    review = track_view.get("review")
    if not isinstance(review, dict):
        raise RuntimeError("Track has no stored review snapshot")
    require_equal("review.head", review.get("head"), compact_fingerprint(want["head"], "head"))
    require_equal("review.tree", review.get("tree"), compact_fingerprint(want["tree"], "tree"))
    require_equal("review.track_revision", review.get("track_revision"), want["track_revision"])
    require_equal("review.submitted_by", review.get("submitted_by"), want["submitted_by"])
    require_equal("review.submitted_at", review.get("submitted_at"), want["submitted_at"])
    got_tasks = review.get("tasks")
    want_tasks = want["tasks"]
    if not isinstance(got_tasks, list) or len(got_tasks) != len(want_tasks):
        raise RuntimeError("Track review member snapshot drift on task list")
    for index, item in enumerate(want_tasks):
        got = got_tasks[index] if index < len(got_tasks) else {}
        require_equal(f"tasks[{index}].key", got.get("key"), item["key"])
        require_equal(f"tasks[{index}].revision", got.get("revision"), item["revision"])
    return review


def receipt(target_project: str, track: str, view: dict, want: dict) -> dict:
    # The stored review projects compact fingerprints; the receipt binds the
    # exact full fingerprints the Planner approved.
    return {
        "op": "track_accept",
        "target_project": target_project,
        "track": track,
        "revision": view["revision"],
        "head": want["head"],
        "tree": want["tree"],
        "status": view["status"],
    }


def main() -> int:
    try:
        value = read_input()
        if value.get("op") != "track_accept":
            return fail("unsupported operation; only track_accept is implemented")
        # Reject non-loopback targets before any network interaction.
        host, _port = parse_listen_addr(str(value["listen_addr"]))
        base = "http://%s:%d/mcp" % ("[%s]" % host if ":" in host else host, _port)
        session = str(value["planner_session"])
        target_project = str(value["target_project"])
        track = str(value["target_track"])
        compact_fingerprint(value["head"], "head")
        compact_fingerprint(value["tree"], "tree")

        info = mcp_call(base, session, "session/info", {})
        record = info.get("session", {})
        if record.get("role") != "planner":
            return fail("supplied target Session is not role=planner")
        bound_code = record.get("project_code", info.get("project_code"))
        if bound_code != target_project:
            return fail("supplied target Planner Session is not bound to the target project")

        view = mcp_call(base, session, "track/read", {"key": track})
        status = view.get("status")
        if status == "accepted":
            verify_snapshot(view, value, pending=False)
            write_output(receipt(target_project, track, view, value))
            return 0
        if status != "review_pending":
            return fail(f"Track is not awaiting acceptance: status={status!r}")
        verify_snapshot(view, value, pending=True)
        mcp_call(base, session, "track/accept", {"key": track})
        after = mcp_call(base, session, "track/read", {"key": track})
        if after.get("status") != "accepted":
            return fail(f"Track did not reach accepted state: status={after.get('status')!r}")
        verify_snapshot(after, value, pending=False)
        write_output(receipt(target_project, track, after, value))
        return 0
    except (RuntimeError, KeyError, TypeError, ValueError) as err:
        return fail(str(err))


if __name__ == "__main__":
    sys.exit(main())
