#!/usr/bin/env python3
from __future__ import annotations
import argparse
import json
import urllib.request

p = argparse.ArgumentParser()
p.add_argument("--url", default="http://127.0.0.1:8875/mcp")
a = p.parse_args()

def call(payload: dict) -> dict:
    req = urllib.request.Request(a.url, data=json.dumps(payload).encode(), headers={"Content-Type":"application/json"})
    with urllib.request.urlopen(req, timeout=5) as response:
        return json.load(response)

init = call({"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}})
assert init["result"]["serverInfo"]["name"] == "gpt-tunnel-gatewayd"
tools = call({"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}})
assert len(tools["result"]["tools"]) >= 20
ping = call({"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"system_ping","arguments":{}}})
assert isinstance(ping["result"]["structuredContent"], dict)
print("MCP_SMOKE_OK")
