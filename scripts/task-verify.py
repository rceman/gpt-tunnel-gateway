#!/usr/bin/env python3
import json
import os
import re
import subprocess
import sys
import time


def fail(message):
    print(message, file=sys.stderr)
    raise SystemExit(1)


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

checks = [
    ("format", ["go", "run", "./cmd/gofmt-struct", "--check", "."]),
    ("static_check", ["python3", "scripts/static-check.py"]),
    ("full_test", ["./scripts/test-full.sh"]),
]
results = []
for name, argv in checks:
    started = time.monotonic_ns()
    try:
        process = subprocess.Popen(argv, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
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
