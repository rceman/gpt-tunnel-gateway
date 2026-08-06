#!/usr/bin/env python3
"""Validate stdin and atomically write its derived canonical completion path."""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from completion_receipt import CompletionReceiptError, MAX_RECEIPT_BYTES, prepare_receipt


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository-root", default=".")
    parser.add_argument("--task-file", required=True)
    parser.add_argument("--run-id", required=True)
    args = parser.parse_args(argv)
    try:
        raw = sys.stdin.buffer.read(MAX_RECEIPT_BYTES + 1)
        destination, created = prepare_receipt(Path(args.repository_root), Path(args.task_file), args.run_id, raw)
        print(json.dumps({"status": "WRITTEN" if created else "ALREADY_PRESENT", "path": str(destination), "run_id": args.run_id}, sort_keys=True))
        return 0
    except (OSError, CompletionReceiptError) as exc:
        print(json.dumps({"status": "BLOCKED_CANONICAL_TOOLING_GAP", "error": str(exc)}, sort_keys=True))
        return 4


if __name__ == "__main__":
    sys.exit(main())
