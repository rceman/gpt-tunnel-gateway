#!/usr/bin/env bash
set -euo pipefail

export PYTHONDONTWRITEBYTECODE=1
exec python3 -m unittest scripts/integration_activate_test.py "$@"
