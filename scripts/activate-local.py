#!/usr/bin/env python3
"""GTW activate_local Procedure.

Validates the accepted-Track/source authority for the exact clean checkout,
reuses the project-owned disposable activation preflight (snapshot -> build ->
boot -> readiness -> E2E -> reopen) before any live durable-state mutation,
then runs the canonical binary cutover through gpt-tunnelctl
install-and-restart-gateway — which owns the artifact+durable-state recovery
boundary, Tunnel preservation, rollback, and compatible-forward recovery.
Emits compact structured activation evidence. Live mutation is rejected when
authority is missing, stale, or mismatched.
"""
import hashlib
import json
import os
import pwd
import re
import sqlite3
import subprocess
import sys
import tempfile
import time
from pathlib import Path

SELF_HOST_PROJECT = "gpt-tunnel-gateway"
ARTIFACTS = ("gpt-tunnel", "gpt-tunnel-gatewayd", "gpt-tunnelctl")
CHECK_IDS = ("authority_bind", "source_bind", "preflight", "release_build", "cutover", "verify")
MAX_LOG_BYTES = 4096
FINGERPRINT_RE = re.compile(r"[0-9a-f]{40}")
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


def home_dir():
    return Path(pwd.getpwuid(os.getuid()).pw_dir)


def ctl_environment():
    environment = {
        "PATH": "/usr/bin:/bin",
        "LC_ALL": "C",
        "HOME": str(home_dir()),
    }
    return environment


def bounded(value):
    text = value if isinstance(value, str) else value.decode("utf-8", "replace")
    return text[-MAX_LOG_BYTES:]


def run_ctl(ctl, args, phase):
    try:
        completed = subprocess.run(
            [str(ctl)] + list(args), env=ctl_environment(), text=True,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=180,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        fail_check(phase, "control-plane command could not run: %s" % error)
    if completed.returncode != 0:
        diagnostics = bounded(completed.stderr or completed.stdout)
        fail_check(phase, "control-plane command failed: %s" % diagnostics)
    return completed.stdout


def ctl_json(ctl, args, phase):
    raw = run_ctl(ctl, args, phase)
    try:
        return json.loads(raw)
    except ValueError:
        fail_check(phase, "control-plane output is not structured")


def sha256(path):
    digest = hashlib.sha256()
    with open(path, "rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


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
        fail_check("authority_bind", "accepted Track does not bind the exact activation source")
    return track


def source_bind(payload):
    head = run_git(["rev-parse", "--verify", "HEAD"], ".")
    tree = run_git(["rev-parse", "--verify", "HEAD^{tree}"], ".")
    dirty = run_git(["status", "--porcelain", "--untracked-files=all"], ".")
    if head is None or tree is None or dirty is None:
        fail_check("source_bind", "candidate repository identity could not be checked")
    if head.strip() != payload["source_commit"] or tree.strip() != payload["source_tree"] or dirty:
        fail_check("source_bind", "activation source does not match the accepted authority or is dirty")
    return head.strip(), tree.strip()


def run_preflight(repo_root, run_dir, source_commit, source_tree):
    """Invoke the same project-owned disposable exact-source preflight before
    any live durable-state mutation."""
    input_path = Path(run_dir) / "preflight-input.json"
    output_path = Path(run_dir) / "preflight-output.json"
    envelope = {
        "input": {"source_commit": source_commit, "source_tree": source_tree},
        "context": {"project": SELF_HOST_PROJECT},
    }
    input_path.write_text(json.dumps(envelope, separators=(",", ":")), encoding="utf-8")
    environment = {
        "PATH": "/usr/bin:/bin",
        "LC_ALL": "C",
        "TMPDIR": str(run_dir),
        "GTW_PROCEDURE_INPUT_FILE": str(input_path),
        "GTW_PROCEDURE_OUTPUT_FILE": str(output_path),
    }
    try:
        completed = subprocess.run(
            ["python3", str(repo_root / "scripts" / "activation-preflight.py")],
            cwd=str(repo_root), env=environment, text=True,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=20 * 60,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        fail_check("preflight", "disposable preflight could not run: %s" % error)
    if completed.returncode != 0:
        fail_check("preflight", "disposable preflight failed: %s" % bounded(completed.stderr))
    try:
        evidence = json.loads(output_path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        fail_check("preflight", "disposable preflight returned no structured evidence")
    checks = evidence.get("checks")
    if not isinstance(checks, list) or any(
        not isinstance(check, dict) or check.get("exit_code") != 0 for check in checks
    ):
        fail_check("preflight", "disposable preflight evidence is incomplete")


def healthy_exact_runtime(ctl, artifact_hashes, source_commit):
    """The installed set plus running Gateway must already be the exact source
    so an unnecessary cutover is skipped and the Tunnel process is preserved."""
    current = ctl_json(ctl, ["runtime-status"], "verify")
    gateway = current.get("gateway", {})
    identity = current.get("runtime_identity", {})
    if (
        not current.get("gateway_ready")
        or not current.get("tunnel_ready")
        or not current.get("version_match")
        or not gateway.get("running")
        or not gateway.get("identity_valid")
        or not identity.get("artifact_set_coherent")
        or not identity.get("running_gateway_matches_install")
    ):
        return None
    if identity.get("source_sha") not in (None, source_commit) or not identity.get("exact_source_match"):
        return None
    executable = gateway.get("executable")
    if not isinstance(executable, str):
        return None
    try:
        if sha256(Path(executable)) != artifact_hashes["gpt-tunnel-gatewayd"]:
            return None
        installed = {name: sha256(home_dir() / ".local" / "bin" / name) for name in ARTIFACTS}
    except (FileNotFoundError, OSError):
        return None
    if installed != artifact_hashes:
        return None
    return current


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
        fail("activate_local only binds the GTW self-host project")
    for field in ("track", "source_commit", "source_tree"):
        if not isinstance(payload.get(field), str):
            fail("activate_local input is missing %s" % field)
    for field in ("source_commit", "source_tree"):
        if FINGERPRINT_RE.fullmatch(payload[field]) is None:
            fail("activate_local %s must be an exact 40-hex Git fingerprint" % field)

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
    source_bind(payload)
    record("source_bind", started)

    # preflight: disposable exact-source proof before live mutation.
    started = time.monotonic_ns()
    run_preflight(repo_root, run_dir, payload["source_commit"], payload["source_tree"])
    record("preflight", started)

    # release_build: canonical artifact set under the release builder.
    started = time.monotonic_ns()
    dist = Path(tempfile.mkdtemp(prefix="activate-local-", dir=str(run_dir))) / "dist"
    try:
        build = subprocess.run(
            ["bash", "scripts/build-release.sh", str(dist)], cwd=str(repo_root),
            env={"PATH": "/usr/bin:/bin", "LC_ALL": "C"}, text=True,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=1800,
        )
        if build.returncode != 0:
            fail_check("release_build", "release build failed: %s" % bounded(build.stdout))
        artifact_hashes = {name: sha256(dist / name) for name in ARTIFACTS}
        verify = subprocess.run(
            ["sha256sum", "-c", "SHA256SUMS"], cwd=str(dist), text=True,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=60,
        )
        if verify.returncode != 0:
            fail_check("release_build", "artifact checksum verification failed")
    except (OSError, subprocess.TimeoutExpired) as error:
        fail_check("release_build", "release build could not run: %s" % error)
    record("release_build", started)

    # cutover: the canonical ctl owns snapshot -> stop -> replace -> restart ->
    # verify with the combined artifact+durable-state recovery boundary; a
    # failure keeps its evidence and permits bounded truthful retry.
    started = time.monotonic_ns()
    ctl = dist / "gpt-tunnelctl"
    before = ctl_json(ctl, ["runtime-status"], "cutover")
    tunnel = before.get("tunnel", {})
    tunnel_pid = tunnel.get("pid")
    if not tunnel.get("running") or not tunnel.get("identity_valid") or not isinstance(tunnel_pid, int):
        fail_check("cutover", "Tunnel is not a valid running process")
    after = healthy_exact_runtime(ctl, artifact_hashes, payload["source_commit"])
    restarted = False
    if after is None:
        run_ctl(
            ctl,
            [
                "install-and-restart-gateway",
                "--gateway-bin", str(dist / "gpt-tunnel-gatewayd"),
                "--cli-bin", str(dist / "gpt-tunnel"),
                "--ctl-bin", str(dist / "gpt-tunnelctl"),
            ],
            "cutover",
        )
        after = ctl_json(ctl, ["runtime-status"], "verify")
        restarted = True
    record("cutover", started)

    # verify: exact running-source proof, preserved Tunnel identity, and
    # durable-state readability on the live Gateway.
    started = time.monotonic_ns()
    if after.get("tunnel", {}).get("pid") != tunnel_pid:
        fail_check("verify", "activation changed the Tunnel process")
    if not after.get("gateway_ready") or not after.get("tunnel_ready") or not after.get("version_match"):
        fail_check("verify", "Gateway/Tunnel readiness or version identity failed")
    state = ctl_json(ctl, ["state", "check"], "verify")
    if state.get("valid") is not True:
        fail_check("verify", "durable state check failed")
    doctor = run_ctl(ctl, ["doctor"], "verify").strip()
    if doctor != "doctor: ok":
        fail_check("verify", "Gateway doctor failed")
    record("verify", started)

    version = str((repo_root / "VERSION").read_text(encoding="utf-8").strip())
    with open(output_path, "w", encoding="utf-8") as stream:
        json.dump(
            {
                "track": track["id"],
                "source_commit": payload["source_commit"],
                "source_tree": payload["source_tree"],
                "gateway_version": version,
                "tunnel_pid": tunnel_pid,
                "restarted": restarted,
                "checks": [checks[name] for name in CHECK_IDS],
            },
            stream,
            separators=(",", ":"),
            sort_keys=True,
        )


if __name__ == "__main__":
    main()
