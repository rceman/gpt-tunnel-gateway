#!/usr/bin/env python3
import json
import os
import re
import subprocess
import sys
import time

GO_CANDIDATES = ("/usr/local/go/bin/go", "/usr/bin/go")
GO_CACHE_ROOT = "/tmp/gpt-tunnel-gateway-task-verify"


def fail(message):
    print(message, file=sys.stderr)
    raise SystemExit(1)


def resolve_go(is_file=None, is_executable=None):
    is_file = os.path.isfile if is_file is None else is_file
    is_executable = (lambda path: os.access(path, os.X_OK)) if is_executable is None else is_executable
    for candidate in GO_CANDIDATES:
        if is_file(candidate) and is_executable(candidate):
            return candidate
    raise RuntimeError("Go toolchain unavailable")


def gate_environment(go_path, source=None):
    environment = dict(os.environ if source is None else source)
    environment["PATH"] = os.pathsep.join((os.path.dirname(go_path), "/usr/bin", "/bin"))
    environment["GOCACHE"] = os.path.join(GO_CACHE_ROOT, "gocache")
    environment["GOMODCACHE"] = os.path.join(GO_CACHE_ROOT, "gomodcache")
    return environment


def gate_checks(go_path):
    checks = [
        ("format", ["go", "run", "./cmd/gofmt-struct", "--check", "."]),
        ("static_check", ["python3", "scripts/static-check.py"]),
        ("full_test", ["./scripts/test-full.sh"]),
    ]
    checks[0][1][0] = go_path
    return checks


def self_test_go_resolution():
    local_go, system_go = GO_CANDIDATES
    cases = (
        ({local_go}, {local_go}, local_go),
        ({system_go}, {system_go}, system_go),
        ({local_go, system_go}, {local_go, system_go}, local_go),
        ({local_go, system_go}, {system_go}, system_go),
    )
    for files, executables, expected in cases:
        actual = resolve_go(
            is_file=lambda path, files=files: path in files,
            is_executable=lambda path, executables=executables: path in executables,
        )
        if actual != expected:
            raise AssertionError("Go candidate ordering did not resolve deterministically")
    try:
        resolve_go(is_file=lambda _: False, is_executable=lambda _: False)
    except RuntimeError as error:
        if str(error) != "Go toolchain unavailable":
            raise AssertionError("missing Go toolchain did not fail closed") from error
    else:
        raise AssertionError("missing Go toolchain did not fail closed")
    if any(gate_checks(path)[0][1][0] != path for path in (local_go, system_go)):
        raise AssertionError("format gate did not use the resolved Go executable")
    source_environment = {
        "PATH": "/inherited",
        "GOCACHE": "/inherited/gocache",
        "GOMODCACHE": "/inherited/gomodcache",
        "TEST_VALUE": "preserved",
    }
    for go_path in (local_go, system_go):
        environment = gate_environment(go_path, source_environment)
        expected_path = os.pathsep.join((os.path.dirname(go_path), "/usr/bin", "/bin"))
        expected_gocache = os.path.join(GO_CACHE_ROOT, "gocache")
        expected_gomodcache = os.path.join(GO_CACHE_ROOT, "gomodcache")
        if (
            environment.get("PATH") != expected_path
            or environment.get("GOCACHE") != expected_gocache
            or environment.get("GOMODCACHE") != expected_gomodcache
            or not os.path.isabs(environment.get("GOCACHE", ""))
            or not os.path.isabs(environment.get("GOMODCACHE", ""))
            or environment.get("TEST_VALUE") != "preserved"
            or "HOME" in environment
        ):
            raise AssertionError("Go gate environment is not deterministic without HOME")
    print("task-verify Go resolution and cache regression checks passed")


def main():
    if sys.argv[1:] == ["--self-test"]:
        self_test_go_resolution()
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
    if not isinstance(payload, dict) or not isinstance(context, dict) or context.get("hook") != "pre_task_verify":
        fail("Procedure context does not identify pre_task_verify")
    for field in ("candidate_head", "candidate_tree", "main_base"):
        if not isinstance(payload.get(field), str) or re.fullmatch(r"[0-9a-f]{8}", payload[field]) is None:
            fail("Task verification Git fingerprint is invalid")

    try:
        head = subprocess.check_output(["git", "rev-parse", "--verify", "HEAD"], text=True).strip()
        tree = subprocess.check_output(["git", "rev-parse", "--verify", "HEAD^{tree}"], text=True).strip()
        dirty = subprocess.check_output(["git", "status", "--porcelain", "--untracked-files=all"], text=True)
    except (OSError, subprocess.CalledProcessError):
        fail("Candidate repository identity could not be checked")
    if not head.startswith(payload["candidate_head"]) or not tree.startswith(payload["candidate_tree"]) or dirty:
        fail("Procedure did not run on the exact clean Task candidate")

    try:
        go_path = resolve_go()
    except RuntimeError as error:
        fail(str(error))
    child_environment = gate_environment(go_path)
    checks = gate_checks(go_path)
    results = []
    for name, argv in checks:
        started = time.monotonic_ns()
        try:
            process = subprocess.Popen(argv, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, env=child_environment)
        except OSError:
            code = 127
            tail = b""
        else:
            tail = bytearray()
            for chunk in iter(lambda: process.stdout.read(8192), b""):
                tail.extend(chunk)
                if len(tail) > 4096:
                    del tail[:-4096]
            returncode = process.wait()
            code = returncode if returncode >= 0 else min(255, 128 - returncode)
        duration = min(30 * 60 * 1000, max(0, (time.monotonic_ns() - started) // 1_000_000))
        results.append({"id": name, "exit_code": code, "duration_ms": duration})
        if code:
            sys.stdout.buffer.write(("\n[" + name + " exited " + str(code) + "]\n").encode("utf-8"))
            sys.stdout.buffer.write(tail)
            sys.stdout.buffer.write(b"\n")

    try:
        with open(output_path, "w", encoding="utf-8") as stream:
            json.dump({"gates": results}, stream, separators=(",", ":"), sort_keys=True)
    except OSError:
        fail("Task verification evidence could not be written")


if __name__ == "__main__":
    main()
