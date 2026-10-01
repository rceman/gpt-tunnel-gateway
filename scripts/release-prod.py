#!/usr/bin/env python3
"""GTW release_prod Procedure.

Validates the accepted-Track/source authority for the exact clean checkout
before any external side effect, then drives the canonical release lifecycle:
tag-ready validation, annotated tag creation at the exact source, remote tag
publication, and typed GitHub publication verification. Emits compact
structured provenance. Source advance after acceptance requires a new
accept cycle because the accepted review head/tree is bound exactly.
"""
import json
import os
import re
import sqlite3
import subprocess
import sys
import time
from pathlib import Path

SELF_HOST_PROJECT = "gpt-tunnel-gateway"
CHECK_IDS = ("authority_bind", "source_bind", "tag_ready", "tag", "publish", "verify")
MAX_LOG_BYTES = 4096
FINGERPRINT_RE = re.compile(r"[0-9a-f]{40}")
TAG_RE = re.compile(r"^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$")
MAX_GATE_MS = 30 * 60 * 1000


def fail(message):
    print(message, file=sys.stderr)
    raise SystemExit(1)


def fail_check(check, message):
    fail("[%s] %s" % (check, message))


def git_environment():
    return {
        "PATH": "/usr/bin:/bin",
        "LC_ALL": "C",
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": "/dev/null",
        "GIT_TERMINAL_PROMPT": "0",
        "GIT_OPTIONAL_LOCKS": "0",
    }


def run_git(args, cwd):
    try:
        return subprocess.check_output(
            ["git"] + list(args), cwd=str(cwd), env=git_environment(), text=True,
            stderr=subprocess.STDOUT, timeout=60,
        )
    except (OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired):
        return None


def run_git_strict(args, cwd, phase):
    result = run_git(args, cwd)
    if result is None:
        fail_check(phase, "Git command %s failed" % args[0])
    return result.strip()


def bounded(value):
    text = value if isinstance(value, str) else value.decode("utf-8", "replace")
    return text[-MAX_LOG_BYTES:]


def run_script(script, args, phase, timeout=600):
    try:
        completed = subprocess.run(
            ["python3", str(script)] + list(args), env={"PATH": "/usr/bin:/bin", "LC_ALL": "C"},
            text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        fail_check(phase, "canonical release tooling could not run: %s" % error)
    return completed


def live_state_dir(run_dir):
    """executeProcedureScript runs trusted Procedures with
    TMPDIR=<state_dir>/procedure-runs/<runtime>; the live durable state
    directory is the grandparent."""
    run_dir = Path(run_dir).resolve()
    if run_dir.parent.name != "procedure-runs":
        return None
    state_dir = run_dir.parent.parent
    if not (state_dir / "databases").is_dir():
        return None
    return state_dir


def load_track_authority(state_dir, track_id):
    """Read the durable Shared Track and return its accepted review authority."""
    shared = state_dir / "databases" / "shared.db"
    if not shared.is_file():
        return None
    connection = sqlite3.connect("file:%s?mode=ro" % shared, uri=True)
    try:
        row = connection.execute(
            "SELECT revision, payload FROM shared_tracks WHERE id=?", (track_id,)
        ).fetchone()
    finally:
        connection.close()
    if row is None:
        return None
    revision, payload = row
    try:
        track = json.loads(payload)
    except (TypeError, ValueError):
        return None
    if track.get("id") != track_id or track.get("revision") != revision:
        return None
    return track


def authority_bind(state_dir, payload):
    """The accepted Track must durably bind the exact source the caller names."""
    track_id = payload["track"]
    if not re.fullmatch(r"GTW-TRK[1-9][0-9]*", track_id):
        fail_check("authority_bind", "Track authority must name a GTW Track")
    track = load_track_authority(state_dir, track_id)
    if track is None:
        fail_check("authority_bind", "accepted Track authority is missing")
    review = track.get("review")
    if (
        track.get("status") != "accepted"
        or not isinstance(review, dict)
        or review.get("head") != payload["source_commit"]
        or review.get("tree") != payload["source_tree"]
        or review.get("track_revision") != track.get("revision")
    ):
        fail_check("authority_bind", "accepted Track does not bind the exact release source")
    return track


def source_bind(payload):
    head = run_git(["rev-parse", "--verify", "HEAD"], ".")
    tree = run_git(["rev-parse", "--verify", "HEAD^{tree}"], ".")
    dirty = run_git(["status", "--porcelain", "--untracked-files=all"], ".")
    if head is None or tree is None or dirty is None:
        fail_check("source_bind", "candidate repository identity could not be checked")
    if head.strip() != payload["source_commit"] or tree.strip() != payload["source_tree"] or dirty:
        fail_check("source_bind", "release source does not match the accepted authority or is dirty")
    return head.strip(), tree.strip()


def remote_repository(repo_root):
    """Derive the canonical owner/repository identity from the configured
    origin remote; anything unparseable fails closed."""
    url = run_git(["remote", "get-url", "origin"], repo_root)
    if url is None:
        fail_check("verify", "canonical remote URL is unavailable")
    match = re.search(r"github\.com[:/]([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+?)(?:\.git)?\s*$", url.strip())
    if match is None:
        fail_check("verify", "canonical remote does not resolve to a GitHub repository")
    return match.group(1)


def main():
    input_path = os.environ.get("GTW_PROCEDURE_INPUT_FILE", "")
    output_path = os.environ.get("GTW_PROCEDURE_OUTPUT_FILE", "")
    if not input_path or not output_path:
        fail("Procedure input/output paths are unavailable")
    try:
        with open(input_path, "r", encoding="utf-8") as stream:
            envelope = json.load(stream)
    except (OSError, ValueError):
        fail("Procedure input is invalid")
    payload = envelope.get("input")
    context = envelope.get("context")
    if not isinstance(payload, dict) or not isinstance(context, dict):
        fail("Procedure input envelope is invalid")
    if context.get("project") != SELF_HOST_PROJECT:
        fail("release_prod only binds the GTW self-host project")
    for field in ("track", "source_commit", "source_tree"):
        if not isinstance(payload.get(field), str):
            fail("release_prod input is missing %s" % field)
    for field in ("source_commit", "source_tree"):
        if FINGERPRINT_RE.fullmatch(payload[field]) is None:
            fail("release_prod %s must be an exact 40-hex Git fingerprint" % field)

    run_dir = Path(os.environ.get("TMPDIR", "")).resolve()
    state_dir = live_state_dir(run_dir)
    if state_dir is None:
        fail("live Gateway state directory is not derivable from the Procedure environment")
    repo_root = Path.cwd()

    checks = {name: {"id": name, "exit_code": 0, "duration_ms": 0} for name in CHECK_IDS}

    def record(name, started):
        checks[name]["duration_ms"] = min(
            MAX_GATE_MS, max(0, (time.monotonic_ns() - started) // 1_000_000))

    # authority_bind: the durable accepted Track must bind this exact source.
    started = time.monotonic_ns()
    track = authority_bind(state_dir, payload)
    record("authority_bind", started)

    # source_bind: the Procedure checkout must be that exact clean source.
    started = time.monotonic_ns()
    source_commit, source_tree = source_bind(payload)
    record("source_bind", started)

    # tag_ready: the canonical release lifecycle must accept this state.
    started = time.monotonic_ns()
    release_py = repo_root / "scripts" / "release.py"
    ready = run_script(release_py, ["check-tag-ready"], "tag_ready")
    version = (repo_root / "VERSION").read_text(encoding="utf-8").strip()
    tag = "v" + version
    if TAG_RE.fullmatch(tag) is None:
        fail_check("tag_ready", "VERSION does not resolve to a valid release tag")
    tag_commit = run_git(["rev-parse", "--verify", "--quiet", tag + "^{commit}"], repo_root)
    if ready.returncode != 0:
        # Bounded truthful retry: an existing tag is usable only when it is
        # this exact annotated release tag peeling to the exact source commit.
        peeled_tag = run_git(["cat-file", "-t", tag], repo_root)
        if tag_commit is None or tag_commit.strip() != source_commit or peeled_tag != "tag":
            fail_check("tag_ready", "canonical tag-ready validation failed: %s" % bounded(ready.stdout))
    record("tag_ready", started)

    # tag: create the annotated tag at the exact source when absent.
    started = time.monotonic_ns()
    if tag_commit is None:
        created = run_script(release_py, ["tag"], "tag")
        if created.returncode != 0:
            fail_check("tag", "annotated tag creation failed: %s" % bounded(created.stdout))
        tag_commit = run_git(["rev-parse", "--verify", tag + "^{commit}"], repo_root)
    verified = run_script(release_py, ["verify-tag", tag], "tag")
    if verified.returncode != 0:
        fail_check("tag", "annotated tag verification failed: %s" % bounded(verified.stdout))
    tag_object = run_git_strict(["rev-parse", "--verify", tag + "^{tag}"], repo_root, "tag")
    record("tag", started)

    # publish: the one external side effect — push the exact tag to the
    # configured remote — happens only after every admission check passed.
    started = time.monotonic_ns()
    remote_refs = run_git(["ls-remote", "origin", "refs/tags/%s" % tag], repo_root)
    if remote_refs is None:
        fail_check("publish", "canonical remote could not be enumerated")
    published_refs = {line.split()[1]: line.split()[0] for line in remote_refs.splitlines() if len(line.split()) == 2}
    if published_refs.get("refs/tags/%s" % tag) != tag_object:
        pushed = subprocess.run(
            ["git", "push", "origin", "refs/tags/%s" % tag], cwd=str(repo_root),
            env=git_environment(), text=True, stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT, timeout=120,
        )
        if pushed.returncode != 0:
            fail_check("publish", "release tag push failed: %s" % bounded(pushed.stdout))
        remote_refs = run_git(["ls-remote", "origin", "refs/tags/%s" % tag], repo_root)
        published_refs = {line.split()[1]: line.split()[0] for line in (remote_refs or "").splitlines() if len(line.split()) == 2}
        if published_refs.get("refs/tags/%s" % tag) != tag_object:
            fail_check("publish", "pushed release tag does not match the local annotated tag")
    record("publish", started)

    # verify: typed publication proof — remote tag identity, exact-SHA CI
    # completion, and declared release/asset topology.
    started = time.monotonic_ns()
    repository = remote_repository(repo_root)
    proof = run_script(
        repo_root / "scripts" / "verify-release-publication.py",
        ["--repository", repository, "--commit", source_commit, "--tag", tag,
         "--repository-root", str(repo_root), "--remote", "origin"],
        "verify", timeout=300,
    )
    if proof.returncode != 0:
        fail_check("verify", "release publication proof failed: %s" % bounded(proof.stdout))
    try:
        result = json.loads(proof.stdout)
    except ValueError:
        fail_check("verify", "release publication proof is not structured")
    if result.get("status") != "success" or result.get("release_commit") != source_commit or result.get("tag") != tag or result.get("tag_object") != tag_object:
        fail_check("verify", "release publication proof does not bind the exact source and tag")
    record("verify", started)

    with open(output_path, "w", encoding="utf-8") as stream:
        json.dump(
            {
                "track": track["id"],
                "source_commit": source_commit,
                "source_tree": source_tree,
                "version": version,
                "tag": tag,
                "tag_object": tag_object,
                "published": True,
                "checks": [checks[name] for name in CHECK_IDS],
            },
            stream,
            separators=(",", ":"),
            sort_keys=True,
        )


if __name__ == "__main__":
    main()
