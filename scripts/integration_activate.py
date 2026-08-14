#!/usr/bin/env python3
"""Repository-owned, Gateway-only integration activation hook."""

from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


ARTIFACTS = ("gpt-tunnel", "gpt-tunnel-gatewayd", "gpt-tunnelctl")


def run(argv: list[str], root: Path, *, text: bool = True) -> str:
    completed = subprocess.run(argv, cwd=root, check=True, text=text, capture_output=True)
    return completed.stdout


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def status(ctl: str, root: Path) -> dict:
    return json.loads(run([ctl, "status"], root))


def activate(phase: str) -> None:
    root = Path.cwd()
    source_sha = run(["git", "rev-parse", "HEAD"], root).strip()
    if len(source_sha) != 40 or any(char not in "0123456789abcdef" for char in source_sha):
        raise RuntimeError("source HEAD is not an exact Git commit")
    if run(["git", "status", "--porcelain"], root).strip():
        raise RuntimeError("integration activation requires a clean source worktree")

    ctl = shutil.which("gpt-tunnelctl")
    if not ctl:
        raise RuntimeError("gpt-tunnelctl is unavailable")
    before = status(ctl, root)
    tunnel = before.get("tunnel", {})
    tunnel_pid = tunnel.get("pid")
    if not tunnel.get("running") or not tunnel.get("identity_valid") or not isinstance(tunnel_pid, int):
        raise RuntimeError("Tunnel is not a valid running process")

    with tempfile.TemporaryDirectory(prefix="gpt-tunnel-integration-") as directory:
        dist = Path(directory) / "dist"
        run(["bash", "scripts/build-release.sh", str(dist)], root)
        artifact_hashes = {name: sha256(dist / name) for name in ARTIFACTS}
        run(["sha256sum", "-c", "SHA256SUMS"], dist)
        run(
            [
                ctl,
                "install",
                "--gateway-bin",
                str(dist / "gpt-tunnel-gatewayd"),
                "--cli-bin",
                str(dist / "gpt-tunnel"),
                "--ctl-bin",
                str(dist / "gpt-tunnelctl"),
            ],
            root,
        )
        installed = {name: sha256(Path.home() / ".local" / "bin" / name) for name in ARTIFACTS}
        if installed != artifact_hashes:
            raise RuntimeError("installed control binaries are not a coherent artifact set")

        run([ctl, "restart-gateway"], root)
        after = status(ctl, root)
        if after.get("tunnel", {}).get("pid") != tunnel_pid:
            raise RuntimeError("Gateway activation changed the Tunnel process")
        if not after.get("gateway_ready") or not after.get("tunnel_ready") or not after.get("version_match"):
            raise RuntimeError("Gateway/Tunnel readiness or version identity failed")
        state = json.loads(run([ctl, "state", "check"], root))
        if state.get("valid") is not True:
            raise RuntimeError("durable state check failed")
        doctor = run([ctl, "doctor"], root).strip()
        if doctor != "doctor: ok":
            raise RuntimeError("Gateway doctor failed")

    if run(["git", "rev-parse", "HEAD"], root).strip() != source_sha or run(["git", "status", "--porcelain"], root).strip():
        raise RuntimeError("activation changed the source worktree")
    print(json.dumps({"phase": phase, "source_sha": source_sha, "tunnel_pid": tunnel_pid, "status": after}, sort_keys=True))


if __name__ == "__main__":
    import sys

    if len(sys.argv) != 2 or sys.argv[1] not in {"pre", "post"}:
        raise SystemExit("usage: integration_activate.py {pre|post}")
    activate(sys.argv[1])
