"""Canonical schema validation and atomic placement for agent completion receipts."""
from __future__ import annotations

import json
import os
import re
import tempfile
from pathlib import Path


MAX_SAFE_INTEGER = 9007199254740991
MAX_RECEIPT_BYTES = 1 << 20
TASK_RE = re.compile(r"^(?P<code>[A-Z]{3})-TSK(?P<number>[1-9][0-9]*)$")
RUN_RE = re.compile(r"^(?P<task>[A-Z]{3}-TSK[1-9][0-9]*)-RUN(?P<number>[1-9][0-9]*)$")
SHA_RE = re.compile(r"^[0-9a-f]{64}$")


class CompletionReceiptError(ValueError):
    pass


def unique_pairs(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise CompletionReceiptError(f"duplicate JSON field: {key}")
        result[key] = value
    return result


def reject_constant(value: str) -> None:
    raise CompletionReceiptError(f"non-finite JSON value is not allowed: {value}")


def load_json_bytes(raw: bytes, label: str) -> dict[str, object]:
    if len(raw) > MAX_RECEIPT_BYTES:
        raise CompletionReceiptError(f"{label} exceeds {MAX_RECEIPT_BYTES} bytes")
    try:
        value = json.loads(raw.decode("utf-8"), object_pairs_hook=unique_pairs, parse_constant=reject_constant)
    except (UnicodeDecodeError, json.JSONDecodeError, CompletionReceiptError) as exc:
        raise CompletionReceiptError(f"invalid {label}: {exc}") from exc
    if not isinstance(value, dict):
        raise CompletionReceiptError(f"{label} must be a JSON object")
    return value


def compact_task_id(value: object) -> str:
    if not isinstance(value, str):
        raise CompletionReceiptError("task id must be a string")
    match = TASK_RE.fullmatch(value)
    if match is None or int(match.group("number")) > MAX_SAFE_INTEGER:
        raise CompletionReceiptError("task id is not a canonical compact identifier")
    return value


def run_identity(value: object) -> tuple[str, int]:
    if not isinstance(value, str):
        raise CompletionReceiptError("run id must be a string")
    match = RUN_RE.fullmatch(value)
    if match is None or int(match.group("number")) > MAX_SAFE_INTEGER:
        raise CompletionReceiptError("run id is not a canonical compact identifier")
    return match.group("task"), int(match.group("number"))


def load_task(path: Path) -> dict[str, object]:
    if path.is_symlink() or not path.is_file():
        raise CompletionReceiptError("task file must be a regular non-symlink file")
    task = load_json_bytes(path.read_bytes(), "task file")
    required = {"schema_version", "id", "sha256", "acceptance_criteria", "required_gates"}
    unknown = set(task) - {
        "schema_version", "id", "sha256", "project_id", "title", "objective", "branch", "base_revision",
        "acceptance_criteria", "constraints", "required_gates", "status", "supersedes", "created_by", "created_at",
    }
    if unknown:
        raise CompletionReceiptError(f"unknown task fields: {sorted(unknown)}")
    if not required.issubset(task):
        raise CompletionReceiptError("task file is missing required receipt fields")
    if task["schema_version"] != 1:
        raise CompletionReceiptError("task schema_version must be 1")
    task_id = compact_task_id(task["id"])
    if not isinstance(task["sha256"], str) or SHA_RE.fullmatch(task["sha256"]) is None:
        raise CompletionReceiptError("task sha256 must be a lowercase SHA-256 digest")
    for field in ("acceptance_criteria", "required_gates"):
        if not isinstance(task[field], list) or not all(isinstance(item, str) for item in task[field]):
            raise CompletionReceiptError(f"task {field} must be a string array")
    task["id"] = task_id
    return task


def _bounded_text(value: object, field: str, maximum: int) -> None:
    if not isinstance(value, str) or not value.strip() or len(value.encode("utf-8")) > maximum:
        raise CompletionReceiptError(f"{field} must be non-empty and at most {maximum} UTF-8 bytes")


def validate_completion(receipt: dict[str, object], task: dict[str, object], run_id: str) -> None:
    allowed = {"schema_version", "run_id", "task_sha256", "status", "summary", "gate_results", "acceptance_coverage", "deviations", "remaining_risks"}
    unknown = set(receipt) - allowed
    if unknown or set(receipt) != allowed:
        missing = allowed - set(receipt)
        raise CompletionReceiptError(f"completion fields mismatch; missing={sorted(missing)} unknown={sorted(unknown)}")
    if receipt["schema_version"] != 1 or receipt["run_id"] != run_id:
        raise CompletionReceiptError("completion identity mismatch")
    if receipt["task_sha256"] != task["sha256"]:
        raise CompletionReceiptError("completion task hash mismatch")
    if receipt["status"] not in {"succeeded", "failed", "needs_gpt_revision"}:
        raise CompletionReceiptError("invalid completion status")
    _bounded_text(receipt["summary"], "summary", 4096)
    gates = receipt["gate_results"]
    acceptance = receipt["acceptance_coverage"]
    for field, value, maximum in (("gate_results", gates, 128), ("acceptance_coverage", acceptance, 128), ("deviations", receipt["deviations"], 64), ("remaining_risks", receipt["remaining_risks"], 64)):
        if not isinstance(value, list) or len(value) > maximum:
            raise CompletionReceiptError(f"{field} must be a bounded array")
    assert isinstance(gates, list) and isinstance(acceptance, list)
    for index, gate in enumerate(gates, 1):
        if not isinstance(gate, dict) or set(gate) != {"id", "exit_code"} or gate["id"] != f"G{index}" or not isinstance(gate["exit_code"], int) or isinstance(gate["exit_code"], bool):
            raise CompletionReceiptError("gate_results must be the exact positional gate sequence")
    for index, value in enumerate(acceptance, 1):
        if not isinstance(value, str) or value != f"AC{index}":
            raise CompletionReceiptError("acceptance_coverage must be ordered and positional")
    if receipt["status"] == "succeeded":
        if len(gates) != len(task["required_gates"]) or len(acceptance) != len(task["acceptance_criteria"]):
            raise CompletionReceiptError("successful completion must cover every gate and criterion")
        if any(gate["exit_code"] != 0 for gate in gates):
            raise CompletionReceiptError("successful completion contains a failed gate")
    else:
        if len(gates) > len(task["required_gates"]) or len(acceptance) > len(task["acceptance_criteria"]):
            raise CompletionReceiptError("completion exceeds task bounds")
    for field in ("deviations", "remaining_risks"):
        for value in receipt[field]:
            _bounded_text(value, field, 2048)


def receipt_path(repository_root: Path, task_id: str, run_number: int) -> Path:
    return repository_root / ".gpt" / "run" / task_id / f"run-{run_number}" / "completion.json"


def write_atomic(path: Path, data: bytes) -> bool:
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(path.parent, 0o700)
    if path.exists() or path.is_symlink():
        if path.is_symlink() or not path.is_file():
            raise CompletionReceiptError("canonical completion path is not a regular file")
        if path.read_bytes() == data:
            return False
        raise CompletionReceiptError("canonical completion already exists with different content")
    fd, temporary = tempfile.mkstemp(prefix=".completion-", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        directory_fd = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
        return True
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def prepare_receipt(repository_root: Path, task_path: Path, run_id: str, raw_receipt: bytes) -> tuple[Path, bool]:
    task = load_task(task_path)
    task_from_run, run_number = run_identity(run_id)
    if task_from_run != task["id"]:
        raise CompletionReceiptError("run id does not belong to task file")
    receipt = load_json_bytes(raw_receipt, "completion receipt")
    validate_completion(receipt, task, run_id)
    destination = receipt_path(repository_root.resolve(), str(task["id"]), run_number)
    data = (json.dumps(receipt, indent=2, ensure_ascii=False) + "\n").encode("utf-8")
    return destination, write_atomic(destination, data)
