#!/usr/bin/env python3
"""Validate the repository-owned QEMU evaluator fixture identity."""

import os
import pathlib
import re
import sys


PACKAGE_RE = re.compile(r"^\./[A-Za-z0-9._/-]+$")
EVALUATOR_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")


def validate(repo_root: pathlib.Path, package: str, evaluator_id: str) -> None:
    root = repo_root.resolve(strict=True)
    if not PACKAGE_RE.fullmatch(package) or pathlib.Path(package).is_absolute():
        raise ValueError("file evaluator package must be a repository-relative ./ path")
    if ".." in package.split("/"):
        raise ValueError("file evaluator package must not contain parent traversal")

    candidate = root.joinpath(package[2:]).resolve(strict=True)
    if pathlib.Path(os.path.commonpath((root, candidate))) != root:
        raise ValueError("file evaluator package escapes repository root")
    if not candidate.is_dir():
        raise ValueError("file evaluator package must resolve to a directory")
    if not EVALUATOR_ID_RE.fullmatch(evaluator_id):
        raise ValueError("file evaluator id must be a path-free identifier of at most 128 characters")


def main(argv: list[str]) -> int:
    if len(argv) != 4:
        print("usage: validate-evaluator-fixture.py REPO_ROOT PACKAGE ID", file=sys.stderr)
        return 2
    try:
        validate(pathlib.Path(argv[1]), argv[2], argv[3])
    except (OSError, ValueError) as error:
        print(f"unsafe QEMU file evaluator fixture: {error}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
