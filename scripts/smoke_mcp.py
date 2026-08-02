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
assert [tool["name"] for tool in tools] == sorted(tool["name"] for tool in tools)
required_tools = {
    "system_ping",
    "gateway_capabilities",
    "project_list",
    "project_read",
    "plan_read",
    "agent_send",
    "agent_tail",
    "agent_status",
}
assert required_tools.issubset({tool["name"] for tool in tools})
tool_by_name = {tool["name"]: tool for tool in tools}
assert "session_key" not in tool_by_name["agent_send"]["inputSchema"]["properties"]
assert tool_by_name["agent_tail"]["inputSchema"]["properties"]["lines"]["minimum"] == 1
assert tool_by_name["agent_tail"]["inputSchema"]["properties"]["skip"]["minimum"] == 0
assert tool_by_name["agent_status"]["outputSchema"]["properties"]["state"]["enum"] == ["waiting", "running", "idle", "error"]
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

capabilities = call(
    {
        "jsonrpc": "2.0",
        "id": 6,
        "method": "tools/call",
        "params": {"name": "gateway_capabilities", "arguments": {}},
    }
)
cap = capabilities["result"]["structuredContent"]
assert cap["hub_protocol_root"] == "gpt-tunnel/v1"

projects = call(
    {
        "jsonrpc": "2.0",
        "id": 7,
        "method": "tools/call",
        "params": {"name": "project_list", "arguments": {}},
    }
)["result"]["structuredContent"]["projects"]
for index, project in enumerate(projects, 8):
    project_id = project["id"]
    read = call(
        {
            "jsonrpc": "2.0",
            "id": index,
            "method": "tools/call",
            "params": {"name": "project_read", "arguments": {"project_id": project_id}},
        }
    )
    assert read["result"]["structuredContent"]["id"] == project_id
    plan = call(
        {
            "jsonrpc": "2.0",
            "id": index + 100,
            "method": "tools/call",
            "params": {"name": "plan_read", "arguments": {"project_id": project_id}},
        }
    )["result"]["structuredContent"]
    assert "body" not in plan
    status = call(
        {
            "jsonrpc": "2.0",
            "id": index + 200,
            "method": "tools/call",
            "params": {"name": "agent_status", "arguments": {"project_id": project_id}},
        }
    )
    assert status["result"]["isError"] is False
    assert status["result"]["structuredContent"]["project_id"] == project_id

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

unknown_agent_argument = call(
    {
        "jsonrpc": "2.0",
        "id": 9,
        "method": "tools/call",
        "params": {
            "name": "agent_send",
            "arguments": {"project_id": projects[0]["id"], "message": "smoke", "session_key": "forbidden"},
        },
    }
)
assert unknown_agent_argument["error"]["code"] == -32602

print(f"MCP_SMOKE_OK tools={len(tools)} projects={len(projects)}")
