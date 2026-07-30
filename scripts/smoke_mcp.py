#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
from pathlib import Path
import urllib.request

p = argparse.ArgumentParser()
p.add_argument("--url", default="http://127.0.0.1:8875/mcp")
a = p.parse_args()


def call(payload: dict) -> dict:
    req = urllib.request.Request(
        a.url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=5) as response:
        return json.load(response)


expected_version = (Path(__file__).resolve().parents[1] / "VERSION").read_text(encoding="utf-8").strip()
init = call(
    {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "initialize",
        "params": {
            "protocolVersion": "2025-03-26",
            "capabilities": {},
            "clientInfo": {"name": "smoke", "version": "1"},
        },
    }
)
assert init["result"]["serverInfo"] == {
    "name": "gpt-tunnel-gatewayd",
    "version": expected_version,
}

tools_response = call({"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}})
tools = tools_response["result"]["tools"]
assert len(tools) == 36
assert [tool["name"] for tool in tools] == sorted(tool["name"] for tool in tools)
for tool in tools:
    assert tool["inputSchema"]["type"] == "object"
    assert tool["outputSchema"]["type"] == "object"
    annotations = tool["annotations"]
    assert set(annotations) == {
        "readOnlyHint",
        "destructiveHint",
        "idempotentHint",
        "openWorldHint",
    }
    assert all(isinstance(value, bool) for value in annotations.values())

ping = call(
    {
        "jsonrpc": "2.0",
        "id": 3,
        "method": "tools/call",
        "params": {
            "name": "system_ping",
            "arguments": {},
            "_meta": {"openai/locale": "en"},
        },
    }
)
assert ping["result"]["isError"] is False
assert ping["result"]["structuredContent"]["version"] == expected_version

unknown = call(
    {
        "jsonrpc": "2.0",
        "id": 4,
        "method": "tools/call",
        "params": {"name": "system_ping", "arguments": {}, "unexpected": True},
    }
)
assert unknown["error"]["code"] == -32602

invalid_meta = call(
    {
        "jsonrpc": "2.0",
        "id": 5,
        "method": "tools/call",
        "params": {"name": "system_ping", "arguments": {}, "_meta": None},
    }
)
assert invalid_meta["error"]["code"] == -32602

print("MCP_SMOKE_OK")
