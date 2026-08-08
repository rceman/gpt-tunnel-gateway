import ast
import json
from pathlib import Path
from types import SimpleNamespace
import unittest
import urllib.request


SCRIPT = Path(__file__).resolve().parents[1] / "scripts" / "smoke_mcp.py"


def smoke_functions():
    tree = ast.parse(SCRIPT.read_text(encoding="utf-8"), filename=str(SCRIPT))
    selected = []
    wanted = {"ORDINARY_MCP_TIMEOUT_SECONDS", "PROJECT_STATUS_MCP_TIMEOUT_SECONDS"}
    for node in tree.body:
        if isinstance(node, ast.Assign) and any(
            isinstance(target, ast.Name) and target.id in wanted for target in node.targets
        ):
            selected.append(node)
        elif isinstance(node, ast.FunctionDef) and node.name in {"call", "project_status_call"}:
            selected.append(node)
    namespace = {"json": json, "urllib": urllib, "a": SimpleNamespace(url="http://example.invalid/mcp")}
    exec(compile(ast.Module(body=selected, type_ignores=[]), str(SCRIPT), "exec"), namespace)
    return namespace


class Response:
    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        return False

    def read(self):
        return b"{}"


class SmokeTimeoutTests(unittest.TestCase):
    def test_ordinary_calls_keep_five_second_timeout(self):
        namespace = smoke_functions()
        observed = []
        original = urllib.request.urlopen
        urllib.request.urlopen = lambda request, timeout: observed.append(timeout) or Response()
        try:
            namespace["call"]({"jsonrpc": "2.0"})
        finally:
            urllib.request.urlopen = original
        self.assertEqual(observed, [5])

    def test_project_status_uses_bounded_timeout_above_server_budget(self):
        namespace = smoke_functions()
        observed = []
        namespace["call"] = lambda payload, timeout=5: observed.append((payload, timeout)) or {"ok": True}
        result = namespace["project_status_call"]("example", 42)
        self.assertEqual(result, {"ok": True})
        self.assertEqual(observed[0][1], 10)
        self.assertEqual(observed[0][0]["params"]["name"], "project_status")
        self.assertGreater(observed[0][1], 8)


if __name__ == "__main__":
    unittest.main()
