#!/usr/bin/env python3
from __future__ import annotations
import json
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
errors: list[str] = []

required = [
    "README.md", "VERSION", "CHANGELOG.md", "AGENTS.md", ".gpt-workflow.lock",
    "cmd/gpt-tunnel/main.go", "cmd/gpt-tunnel-gatewayd/main.go", "cmd/gpt-tunnelctl/main.go",
    "docs/ARCHITECTURE.md", "docs/BEHAVIOR_CONTRACT.md", "docs/HUB_LAYOUT.md",
]
for name in required:
    if not (ROOT / name).is_file():
        errors.append(f"missing required file: {name}")

for path in (ROOT / "schemas").glob("*.json"):
    try:
        json.loads(path.read_text(encoding="utf-8"))
    except Exception as exc:
        errors.append(f"invalid JSON schema {path.relative_to(ROOT)}: {exc}")

for path in ROOT.rglob("*.go"):
    text = path.read_text(encoding="utf-8")
    if re.search(r'exec\.Command(?:Context)?\([^\n]*["\'](?:sh|bash)["\']', text):
        errors.append(f"shell execution forbidden: {path.relative_to(ROOT)}")
    if "codex exec" in text or "--ephemeral" in text:
        errors.append(f"direct Codex spawning forbidden: {path.relative_to(ROOT)}")
    if "force-push" in text or '"--force"' in text and "worktree" not in text:
        errors.append(f"review force operation: {path.relative_to(ROOT)}")

for path in ROOT.rglob("*"):
    if path.is_file() and any(token in path.name.lower() for token in ("tunnel.env", "api_key", "private_key")):
        errors.append(f"secret-like file committed: {path.relative_to(ROOT)}")

for token in ("allow_parallel_protocol", "CheckHubCompatibility", "protocol/v2", "protocol/v3"):
    for path in ROOT.rglob("*"):
        if path.is_file() and ".git" not in path.parts and ".gpt-review" not in path.parts and path != pathlib.Path(__file__):
            try:
                text = path.read_text(encoding="utf-8")
            except UnicodeDecodeError:
                continue
            if token in text:
                errors.append(f"unauthorized compatibility/configurable protocol token {token!r}: {path.relative_to(ROOT)}")

if "gpt-tunnel/v1" not in (ROOT / "internal/hub/hub.go").read_text(encoding="utf-8"):
    errors.append("canonical hub root gpt-tunnel/v1 is not declared")

if errors:
    print("STATIC_CHECK_FAILED")
    for error in errors:
        print(f"- {error}")
    sys.exit(1)
print("STATIC_CHECK_OK")
