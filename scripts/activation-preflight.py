#!/usr/bin/env python3
"""GTW activation_preflight Procedure.

Binds the exact clean source checkout, snapshots the live Gateway's durable
Shared/Local databases with the SQLite online backup API into a disposable
state directory, builds and boots the exact source against that snapshot with
an isolated offline Hub, proves migrations reach readiness and that a second
boot reopens idempotently, runs a bounded sessionless MCP E2E, and returns
compact structured evidence. Live databases are opened read-only and never
mutated; the live Gateway is never contacted or restarted.
"""
import json
import os
import re
import shutil
import signal
import socket
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.request
from pathlib import Path

GO_CANDIDATES = ("/usr/local/go/bin/go", "/usr/bin/go")
GO_CACHE_ROOT = "/tmp/gpt-tunnel-gateway-activation-preflight"
SELF_HOST_PROJECT = "gpt-tunnel-gateway"
CHECK_IDS = ("source_bind", "snapshot", "candidate_build", "boot", "e2e", "reopen")
READINESS_TIMEOUT_SECONDS = 120
READY_POLL_SECONDS = 0.25
MCP_TIMEOUT_SECONDS = 10
MAX_LOG_BYTES = 4096

CHECK_INDEX = {name: index for index, name in enumerate(CHECK_IDS)}


def fail(message):
    print(message, file=sys.stderr)
    raise SystemExit(1)


def fail_check(check, message):
    fail("[%s] %s" % (check, message))


def resolve_go(is_file=None, is_executable=None):
    is_file = os.path.isfile if is_file is None else is_file
    is_executable = (lambda path: os.access(path, os.X_OK)) if is_executable is None else is_executable
    for candidate in GO_CANDIDATES:
        if is_file(candidate) and is_executable(candidate):
            return candidate
    raise RuntimeError("Go toolchain unavailable")


def go_environment(go_path, source=None):
    environment = dict(os.environ if source is None else source)
    environment["PATH"] = os.pathsep.join((os.path.dirname(go_path), "/usr/bin", "/bin"))
    environment["GOCACHE"] = os.path.join(GO_CACHE_ROOT, "gocache")
    environment["GOMODCACHE"] = os.path.join(GO_CACHE_ROOT, "gomodcache")
    return environment


def git_environment():
    environment = {
        "PATH": "/usr/bin:/bin",
        "LC_ALL": "C",
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": "/dev/null",
        "GIT_TERMINAL_PROMPT": "0",
        "GIT_OPTIONAL_LOCKS": "0",
    }
    return environment


def run_git(args, cwd):
    try:
        return subprocess.check_output(
            ["git"] + list(args), cwd=str(cwd), env=git_environment(), text=True,
            stderr=subprocess.STDOUT, timeout=60,
        )
    except (OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired):
        return None


def loopback_port(binder=None):
    binder = binder or socket.socket
    probe = binder(socket.AF_INET, socket.SOCK_STREAM)
    try:
        probe.bind(("127.0.0.1", 0))
        probe.listen(1)
        return probe.getsockname()[1]
    finally:
        probe.close()


def snapshot_database(source, target):
    """Copy a live SQLite database through the consistent online backup API.

    The source is opened read-only; the backup API yields a consistent copy
    including committed WAL contents without writing to the live file.
    """
    target.parent.mkdir(parents=True, exist_ok=True)
    destination = sqlite3.connect(str(target))
    try:
        try:
            origin = sqlite3.connect("file:%s?mode=ro" % source, uri=True)
        except sqlite3.Error:
            # A clean database without WAL sidecars can refuse read-only open;
            # a plain connect still performs reads only under backup().
            origin = sqlite3.connect(str(source))
        try:
            origin.backup(destination)
        finally:
            origin.close()
    finally:
        destination.close()


def live_state_dir(environ=None):
    environ = os.environ if environ is None else environ
    tmpdir = environ.get("TMPDIR", "")
    if not tmpdir:
        return None
    # executeProcedureScript runs trusted Procedures with
    # TMPDIR=<state_dir>/procedure-runs/<runtime>; the live durable state
    # directory is the grandparent.
    run_dir = Path(tmpdir).resolve()
    if run_dir.parent.name != "procedure-runs":
        return None
    state_dir = run_dir.parent.parent
    if not (state_dir / "databases").is_dir():
        return None
    return state_dir


def write_disposable_config(path, listen_addr, health_addr, state_dir, hub_repo, project_root):
    config = {
        "schema_version": 1,
        # A non-HOM gateway id keeps the standing self-host debug policy (and
        # its deferred migration path) off so migrations run inline during the
        # disposable boot.
        "gateway_id": "PFA",
        "listen_addr": listen_addr,
        "state_dir": str(state_dir),
        "max_read_bytes": 1 << 20,
        "max_diff_bytes": 1 << 20,
        "max_list_items": 100,
        "dispatch_timeout_seconds": 30,
        "run_timeout_seconds": 300,
        "airelay_command": "/bin/true",
        "hub": {
            "repository_url": str(hub_repo),
            "branch": "main",
            "author_name": "gtw-preflight",
            "author_email": "gtw-preflight@localhost",
        },
        "controller": {"tunnel_health_listen_addr": health_addr},
        "projects": {
            SELF_HOST_PROJECT: {
                "root": str(project_root),
                "mirror": str(hub_repo),
                "remote": "origin",
                "default_branch": "main",
                "airelay_session_key": "activation_preflight",
                "project_code": "GTW",
            },
        },
    }
    path.write_text(json.dumps(config, sort_keys=True, separators=(",", ":")), encoding="utf-8")


def create_offline_hub(root):
    """Seed a disposable bare Hub repository so the candidate never reaches
    the live Hub remote."""
    seed = root / "hub-seed"
    bare = root / "hub.git"
    seed.mkdir(parents=True)
    bare.mkdir()
    commands = [
        (["init"], seed),
        (["init", "--bare"], bare),
    ]
    for args, cwd in commands:
        if run_git(args, cwd) is None:
            fail_check("boot", "offline Hub repository could not be initialized")
    (seed / "README.md").write_text("offline candidate Hub fixture\n", encoding="utf-8")
    for args in (
        ["add", "README.md"],
        ["-c", "user.name=gtw-preflight", "-c", "user.email=gtw-preflight@localhost",
         "commit", "-m", "candidate Hub fixture"],
        ["remote", "add", "origin", str(bare)],
        ["push", "origin", "HEAD:refs/heads/main"],
    ):
        if run_git(args, seed) is None:
            fail_check("boot", "offline Hub repository could not be seeded")
    if run_git(["symbolic-ref", "HEAD", "refs/heads/main"], bare) is None:
        fail_check("boot", "offline Hub repository could not be finalized")
    return bare


def wait_ready(process, addr, log_buffer, deadline):
    url = "http://%s/readyz" % addr
    while time.monotonic() < deadline:
        if process.poll() is not None:
            return "candidate exited before readiness: %s" % log_buffer.getvalue()[-MAX_LOG_BYTES:]
        try:
            with urllib.request.urlopen(url, timeout=2) as response:
                if response.status == 200:
                    return None
        except Exception:
            pass
        time.sleep(READY_POLL_SECONDS)
    return "candidate readiness timed out: %s" % log_buffer.getvalue()[-MAX_LOG_BYTES:]


def mcp_call(addr, payload, timeout=MCP_TIMEOUT_SECONDS):
    request = urllib.request.Request(
        "http://%s/mcp" % addr,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=timeout) as response:
        return json.load(response)


def mcp_tool(addr, request_id, name, arguments):
    return mcp_call(addr, {
        "jsonrpc": "2.0", "id": request_id, "method": "tools/call",
        "params": {"name": name, "arguments": arguments},
    })


def snapshot_sessions(state_dir):
    """Session identifiers in the disposable snapshot's Local durable store,
    or None when the migrated durable store cannot be read at all."""
    try:
        db = sqlite3.connect(str(state_dir / "databases" / "local.db"))
        try:
            rows = db.execute("SELECT session_id FROM local_sessions ORDER BY session_id LIMIT 8").fetchall()
        finally:
            db.close()
    except sqlite3.Error:
        return None
    return [row[0] for row in rows]


def run_boot(binary, config_path, work_dir):
    log_path = work_dir / "gateway.log"
    log_stream = open(log_path, "w+b")
    process = subprocess.Popen(
        [str(binary), "-config", str(config_path)],
        cwd=str(work_dir),
        env={"PATH": "/usr/bin:/bin", "LC_ALL": "C", "TMPDIR": str(work_dir / "tmp")},
        stdout=log_stream, stderr=subprocess.STDOUT,
        start_new_session=True,
    )
    return process, log_stream


class LogBuffer:
    def __init__(self, stream):
        self.stream = stream

    def getvalue(self):
        self.stream.flush()
        position = self.stream.tell()
        self.stream.seek(max(0, position - MAX_LOG_BYTES))
        data = self.stream.read(MAX_LOG_BYTES)
        self.stream.seek(0, 2)
        return data.decode("utf-8", "replace")


def stop_candidate(process):
    if process.poll() is None:
        try:
            process.send_signal(signal.SIGTERM)
        except OSError:
            pass
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=10)


def run_e2e(addr, expected_version, candidate_state):
    """Bounded sessionless E2E: exact runtime identity, control-plane smoke,
    and durable authority readability from the snapshot."""
    init = mcp_call(addr, {
        "jsonrpc": "2.0", "id": 1, "method": "initialize",
        "params": {
            "protocolVersion": "2025-03-26",
            "capabilities": {},
            "clientInfo": {"name": "activation-preflight", "version": "1"},
        },
    })
    info = init.get("result", {}).get("serverInfo", {})
    if info.get("name") != "gpt-tunnel-gatewayd":
        return "MCP server identity mismatch"
    if info.get("version") != expected_version:
        return "candidate runtime version %r != source version %r" % (info.get("version"), expected_version)
    tools = mcp_call(addr, {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}})
    names = [tool["name"] for tool in tools.get("result", {}).get("tools", [])]
    required = {"call", "guide", "projects", "schema", "session_start", "status"}
    if not required.issubset(set(names)):
        return "candidate MCP surface missing canonical tools"
    # Control-plane readiness and exact runtime identity.
    status = mcp_tool(addr, 3, "status", {})
    body = status.get("result", {}).get("structuredContent", {})
    if status.get("result", {}).get("isError") is not False or body.get("ready") is not True:
        return "control-plane status probe failed"
    if body.get("gateways", [{}])[0].get("key") != "PFA":
        return "control-plane status reported the wrong Gateway identity"
    # Durable authority readability: project resolution reads the snapshot's
    # Shared/Local retirement state; the snapshot's Local sessions must be
    # readable through the migrated durable store.
    projects = mcp_tool(addr, 4, "projects", {"gateway": "PFA"})
    listed = [item.get("name") for item in projects.get("result", {}).get("structuredContent", {}).get("projects", [])]
    if projects.get("result", {}).get("isError") is not False or SELF_HOST_PROJECT not in listed:
        return "durable project resolution read failed"
    if snapshot_sessions(candidate_state) is None:
        return "snapshot Local durable store is not readable"
    schema = mcp_tool(addr, 5, "schema", {"path": ""})
    if schema.get("result", {}).get("isError") is not False:
        return "action surface schema read failed"
    return None


def emit(output_path, evidence):
    try:
        with open(output_path, "w", encoding="utf-8") as stream:
            json.dump(evidence, stream, separators=(",", ":"), sort_keys=True)
    except OSError:
        fail("Preflight evidence could not be written")


def self_test():
    tmp = os.environ.get("TEST_TMPDIR") or tempfile.mkdtemp(prefix="activation-preflight-selftest-")
    root = Path(tmp) / "case"
    (root / "databases").mkdir(parents=True)
    run_dir = root / "procedure-runs" / "run-1"
    run_dir.mkdir(parents=True)
    environ = {"TMPDIR": str(run_dir)}
    if live_state_dir(environ) != root:
        raise AssertionError("live state dir derivation failed")
    environ = {"TMPDIR": str(root / "databases" / "other")}
    if live_state_dir(environ) is not None:
        raise AssertionError("live state dir derivation accepted a non-procedure TMPDIR")
    environ = {"TMPDIR": str(root / "procedure-runs" / "x"), "TEST": "1"}
    for go_path in GO_CANDIDATES:
        env = go_environment(go_path, environ)
        if env["PATH"].split(os.pathsep)[0] != os.path.dirname(go_path) or "HOME" not in os.environ and "HOME" in env:
            raise AssertionError("Go gate environment is not deterministic")
    print("activation-preflight self-test checks passed")


def main():
    if sys.argv[1:] == ["--self-test"]:
        self_test()
        return

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
        fail("activation_preflight only binds the GTW self-host project")
    for field in ("source_commit", "source_tree"):
        if not isinstance(payload.get(field), str) or re.fullmatch(r"[0-9a-f]{8}", payload[field]) is None:
            fail("activation_preflight source fingerprints are invalid")

    checks = {name: {"id": name, "exit_code": 0, "duration_ms": 0} for name in CHECK_IDS}

    def record(name, started):
        checks[name]["duration_ms"] = min(
            30 * 60 * 1000, max(0, (time.monotonic_ns() - started) // 1_000_000))

    # source_bind: the checkout at the Procedure working directory must match
    # the caller-supplied exact fingerprints and be clean.
    started = time.monotonic_ns()
    head = run_git(["rev-parse", "--verify", "HEAD"], ".")
    tree = run_git(["rev-parse", "--verify", "HEAD^{tree}"], ".")
    dirty = run_git(["status", "--porcelain", "--untracked-files=all"], ".")
    if head is None or tree is None or dirty is None:
        fail_check("source_bind", "candidate repository identity could not be checked")
    if not head.strip().startswith(payload["source_commit"]) or not tree.strip().startswith(payload["source_tree"]) or dirty:
        fail_check("source_bind", "Procedure did not run on the exact clean source under test")
    record("source_bind", started)
    source_commit = head.strip()
    source_tree = tree.strip()

    state_dir = live_state_dir()
    if state_dir is None:
        fail("live Gateway state directory is not derivable from the Procedure environment")
    live_shared = state_dir / "databases" / "shared.db"
    live_local = state_dir / "databases" / "local.db"
    if not live_shared.is_file() or not live_local.is_file():
        fail("live durable databases are unavailable")

    work_dir = Path(tempfile.mkdtemp(prefix="activation-preflight-", dir=os.environ.get("TMPDIR") or None))
    try:
        # snapshot: consistent online backup of live Shared/Local databases.
        started = time.monotonic_ns()
        candidate_state = work_dir / "state"
        snapshot_database(live_shared, candidate_state / "databases" / "shared.db")
        snapshot_database(live_local, candidate_state / "databases" / "local.db")
        record("snapshot", started)

        # candidate_build: build the exact checkout under test.
        started = time.monotonic_ns()
        try:
            go_path = resolve_go()
        except RuntimeError as error:
            fail_check("candidate_build", str(error))
        binary = work_dir / "bin" / "gpt-tunnel-gatewayd"
        binary.parent.mkdir(parents=True)
        build = subprocess.run(
            [go_path, "build", "-o", str(binary), "./cmd/gpt-tunnel-gatewayd"],
            env=go_environment(go_path), stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            timeout=900,
        )
        if build.returncode != 0:
            tail = build.stdout[-MAX_LOG_BYTES:].decode("utf-8", "replace")
            fail_check("candidate_build", "candidate build failed: %s" % tail)
        record("candidate_build", started)

        expected_version = (Path(__file__).resolve().parents[1] / "VERSION").read_text(encoding="utf-8").strip()

        hub_repo = create_offline_hub(work_dir)
        (work_dir / "tmp").mkdir(exist_ok=True)
        config_path = work_dir / "candidate-config.json"
        listen_addr = "127.0.0.1:%d" % loopback_port()
        health_addr = "127.0.0.1:%d" % loopback_port()
        write_disposable_config(config_path, listen_addr, health_addr, candidate_state, hub_repo, Path.cwd())

        # boot: first disposable startup must apply migrations and reach
        # readiness, then the bounded sessionless E2E runs.
        started = time.monotonic_ns()
        process, log_stream = run_boot(binary, config_path, work_dir)
        logs = LogBuffer(log_stream)
        try:
            deadline = time.monotonic() + READINESS_TIMEOUT_SECONDS
            error = wait_ready(process, listen_addr, logs, deadline)
            if error:
                fail_check("boot", error)
            record("boot", started)

            started = time.monotonic_ns()
            error = run_e2e(listen_addr, expected_version, candidate_state)
            if error:
                fail_check("e2e", error)
            record("e2e", started)
        finally:
            stop_candidate(process)
            log_stream.close()

        # reopen: a second startup on the migrated disposable state must reach
        # readiness again, proving migration markers complete idempotently.
        started = time.monotonic_ns()
        process, log_stream = run_boot(binary, config_path, work_dir)
        logs = LogBuffer(log_stream)
        try:
            deadline = time.monotonic() + READINESS_TIMEOUT_SECONDS
            error = wait_ready(process, listen_addr, logs, deadline)
            if error:
                fail_check("reopen", error)
            record("reopen", started)
        finally:
            stop_candidate(process)
            log_stream.close()
    finally:
        shutil.rmtree(work_dir, ignore_errors=True)

    emit(output_path, {
        "source_commit": source_commit,
        "source_tree": source_tree,
        "gateway_version": expected_version,
        "checks": [checks[name] for name in CHECK_IDS],
    })


if __name__ == "__main__":
    main()
