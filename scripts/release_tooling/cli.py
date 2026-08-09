from __future__ import annotations

import argparse
import sys

from .configuration import load_config, repository_root
from .foundation import ReleaseError
from .lifecycle_checks import (
    command_check,
    command_check_source,
    command_commit,
    command_prepare,
    command_release_ready,
)
from .lifecycle_tags import command_tag, command_tag_ready, command_verify_tag


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Canonical two-mode release lifecycle automation.")
    parser.add_argument("--repo", help="Repository root. Defaults to the parent of scripts/.")
    parser.add_argument("--config", default="release-config.json", help="Config path relative to repository root.")
    subparsers = parser.add_subparsers(dest="command", required=True)
    subparsers.add_parser("check", help="Verify configured version files and changelog structure.")
    subparsers.add_parser("check-source", help="Verify implementation_unreleased source state.")
    subparsers.add_parser("check-release-ready", help="Verify release-publication-ready state.")
    subparsers.add_parser("check-tag-ready", help="Verify annotated-tag-ready state.")
    prepare = subparsers.add_parser("prepare", help="Prepare a release from Unreleased notes.")
    prepare.add_argument("version")
    prepare.add_argument("--date", help="UTC release date YYYY-MM-DD; defaults to today.")
    subparsers.add_parser("commit", help="Commit the actual non-empty release-file subset.")
    subparsers.add_parser("tag", help="Create an annotated tag after tag-ready validation.")
    verify = subparsers.add_parser("verify-tag", help="Verify an annotated tag matches VERSION and HEAD.")
    verify.add_argument("tag")
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        repo = repository_root(args.repo)
        config = load_config(repo, args.config)
        if args.command == "check":
            command_check(repo, config)
        elif args.command == "check-source":
            command_check_source(repo, config)
        elif args.command == "check-release-ready":
            command_release_ready(repo, config)
        elif args.command == "check-tag-ready":
            command_tag_ready(repo, config)
        elif args.command == "prepare":
            command_prepare(repo, config, args.version, args.date)
        elif args.command == "commit":
            command_commit(repo, config)
        elif args.command == "tag":
            command_tag(repo, config)
        elif args.command == "verify-tag":
            command_verify_tag(repo, config, args.tag)
        else:
            parser.error(f"unsupported command: {args.command}")
    except ReleaseError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1
    return 0
