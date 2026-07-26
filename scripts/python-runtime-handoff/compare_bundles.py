#!/usr/bin/env python3
"""Compare two Python runtime handoff bundles without weakening byte equality."""

import argparse
import hashlib
import json
import pathlib
import re
from typing import Any, Dict, List, Optional, Tuple


REQUIRED_FIXED_FILES = {
    "manifest.json",
    "SHA256SUMS",
    "sbom.spdx.json",
    "THIRD_PARTY_NOTICES.md",
}
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
CHECKSUM_RE = re.compile(r"^([0-9a-f]{64}) [ *](.+)$")


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def read_uleb(data: bytes, offset: int) -> Tuple[int, int]:
    value = 0
    shift = 0
    for _ in range(10):
        if offset >= len(data):
            raise ValueError("truncated unsigned LEB128")
        byte = data[offset]
        offset += 1
        value |= (byte & 0x7F) << shift
        if byte & 0x80 == 0:
            return value, offset
        shift += 7
    raise ValueError("unsigned LEB128 is too long")


def wasm_sections(path: pathlib.Path) -> List[Dict[str, Any]]:
    data = path.read_bytes()
    if len(data) < 8 or data[:8] != b"\x00asm\x01\x00\x00\x00":
        raise ValueError("invalid Core Wasm header")
    rows = []
    offset = 8
    index = 0
    while offset < len(data):
        section_id = data[offset]
        offset += 1
        size, payload_offset = read_uleb(data, offset)
        payload_end = payload_offset + size
        if payload_end > len(data):
            raise ValueError("truncated Wasm section")
        payload = data[payload_offset:payload_end]
        custom_name = None
        if section_id == 0:
            name_size, name_offset = read_uleb(payload, 0)
            name_end = name_offset + name_size
            if name_end > len(payload):
                raise ValueError("truncated Wasm custom-section name")
            custom_name = payload[name_offset:name_end].decode("utf-8", errors="replace")
        rows.append(
            {
                "index": index,
                "section_id": section_id,
                "custom_name": custom_name,
                "size_bytes": size,
                "sha256": hashlib.sha256(payload).hexdigest(),
            }
        )
        index += 1
        offset = payload_end
    return rows


def manifest_differences(
    left: Any, right: Any, pointer: str = ""
) -> List[Dict[str, Any]]:
    if type(left) is not type(right):
        return [{"pointer": pointer or "/", "left": left, "right": right}]
    if isinstance(left, dict):
        rows = []
        for key in sorted(set(left) | set(right)):
            child = "%s/%s" % (
                pointer,
                str(key).replace("~", "~0").replace("/", "~1"),
            )
            if key not in left or key not in right:
                rows.append(
                    {"pointer": child, "left": left.get(key), "right": right.get(key)}
                )
            else:
                rows.extend(manifest_differences(left[key], right[key], child))
        return rows
    if isinstance(left, list):
        rows = []
        for index in range(max(len(left), len(right))):
            child = "%s/%d" % (pointer, index)
            if index >= len(left) or index >= len(right):
                rows.append(
                    {
                        "pointer": child,
                        "left": left[index] if index < len(left) else None,
                        "right": right[index] if index < len(right) else None,
                    }
                )
            else:
                rows.extend(manifest_differences(left[index], right[index], child))
        return rows
    if left != right:
        return [{"pointer": pointer or "/", "left": left, "right": right}]
    return []


def file_inventory(directory: pathlib.Path) -> Dict[str, pathlib.Path]:
    return {
        path.relative_to(directory).as_posix(): path
        for path in sorted(directory.rglob("*"))
        if path.is_file()
    }


def safe_relative_path(value: str) -> Optional[str]:
    path = pathlib.PurePosixPath(value)
    if not value or path.is_absolute() or ".." in path.parts or path.as_posix() != value:
        return None
    return value


def read_checksums(path: pathlib.Path) -> Tuple[Dict[str, str], List[str]]:
    records = {}
    errors = []
    try:
        lines = path.read_text().splitlines()
    except OSError as error:
        return {}, ["cannot read SHA256SUMS: %s" % error]
    for line_number, line in enumerate(lines, start=1):
        match = CHECKSUM_RE.fullmatch(line)
        if match is None:
            errors.append("SHA256SUMS line %d is malformed" % line_number)
            continue
        digest, relative = match.groups()
        relative = safe_relative_path(relative)
        if relative is None:
            errors.append("SHA256SUMS line %d has an unsafe path" % line_number)
            continue
        if relative in records:
            errors.append("SHA256SUMS repeats %s" % relative)
            continue
        records[relative] = digest
    return records, errors


def validate_bundle(
    directory: pathlib.Path,
) -> Tuple[Dict[str, Any], Optional[pathlib.Path], List[str]]:
    errors = []
    inventory = file_inventory(directory) if directory.is_dir() else {}
    if not directory.is_dir():
        return {}, None, ["bundle directory does not exist: %s" % directory]
    symlinks = sorted(
        path.relative_to(directory).as_posix()
        for path in directory.rglob("*")
        if path.is_symlink()
    )
    if symlinks:
        errors.append("bundle contains forbidden symlinks: %s" % symlinks)
    missing = sorted(REQUIRED_FIXED_FILES - set(inventory))
    if missing:
        errors.append("bundle missing required files: %s" % missing)

    manifest = {}
    if "manifest.json" in inventory:
        try:
            value = json.loads(inventory["manifest.json"].read_text())
            if isinstance(value, dict):
                manifest = value
            else:
                errors.append("manifest root must be an object")
        except (OSError, json.JSONDecodeError) as error:
            errors.append("manifest read failed: %s" % error)

    artifact_path = None
    artifact = manifest.get("artifact", {}) if isinstance(manifest, dict) else {}
    filename = artifact.get("filename") if isinstance(artifact, dict) else None
    if not isinstance(filename, str) or safe_relative_path(filename) is None:
        errors.append("manifest artifact filename is missing or unsafe")
    else:
        artifact_path = directory / filename
        if filename not in inventory:
            errors.append("manifest artifact file is missing: %s" % filename)
            artifact_path = None

    if artifact_path is not None:
        if artifact_path.read_bytes()[:8] != b"\x00asm\x01\x00\x00\x00":
            errors.append("artifact does not have the Core Wasm header")
        actual_digest = sha256(artifact_path)
        if artifact.get("sha256") != actual_digest:
            errors.append("manifest artifact digest is invalid")
        if artifact.get("size") != artifact_path.stat().st_size:
            errors.append("manifest artifact size is invalid")

    if manifest:
        if type(manifest.get("schema_version")) is not int or manifest["schema_version"] <= 0:
            errors.append("manifest schema_version must be a positive integer")
        if not isinstance(manifest.get("abi_version"), str) or not manifest["abi_version"]:
            errors.append("manifest abi_version must be a nonempty string")
        if not isinstance(manifest.get("artifact_profile"), str) or not manifest["artifact_profile"]:
            errors.append("manifest artifact_profile must be a nonempty string")
        if manifest.get("target") != "wasm32-wasip1":
            errors.append("manifest target must be wasm32-wasip1")
        build = manifest.get("build", {})
        if not isinstance(build, dict):
            errors.append("manifest build record is missing")
        else:
            commit = build.get("repository_commit")
            epoch = build.get("source_date_epoch")
            if not isinstance(commit, str) or COMMIT_RE.fullmatch(commit) is None:
                errors.append("manifest producer commit must be 40 lowercase hex characters")
            if not isinstance(epoch, str) or not epoch.isdigit() or int(epoch) <= 0:
                errors.append("manifest SOURCE_DATE_EPOCH must be a positive integer string")
            if build.get("compiler_target") != "wasm32-wasip1":
                errors.append("manifest compiler target must be wasm32-wasip1")
            if build.get("execution_model") != "reactor":
                errors.append("manifest execution model must be reactor")

    if "SHA256SUMS" in inventory:
        checksums, checksum_errors = read_checksums(inventory["SHA256SUMS"])
        errors.extend(checksum_errors)
        expected = set(inventory) - {"SHA256SUMS"}
        if set(checksums) != expected:
            errors.append(
                "SHA256SUMS file set differs: missing=%s extra=%s"
                % (sorted(expected - set(checksums)), sorted(set(checksums) - expected))
            )
        for relative in sorted(expected & set(checksums)):
            if checksums[relative] != sha256(inventory[relative]):
                errors.append("SHA256SUMS digest mismatch for %s" % relative)

    return manifest, artifact_path, errors


def compare_sections(
    left_path: pathlib.Path, right_path: pathlib.Path
) -> List[Dict[str, Any]]:
    left = wasm_sections(left_path)
    right = wasm_sections(right_path)
    rows = []
    for index in range(max(len(left), len(right))):
        left_row = left[index] if index < len(left) else None
        right_row = right[index] if index < len(right) else None
        identity = left_row or right_row
        if identity is None:
            continue
        rows.append(
            {
                "index": index,
                "section_id": identity["section_id"],
                "custom_name": identity["custom_name"],
                "left_size_bytes": left_row["size_bytes"] if left_row else None,
                "right_size_bytes": right_row["size_bytes"] if right_row else None,
                "left_sha256": left_row["sha256"] if left_row else None,
                "right_sha256": right_row["sha256"] if right_row else None,
                "match": left_row == right_row,
            }
        )
    return rows


def compare_directories(left_dir: pathlib.Path, right_dir: pathlib.Path) -> Dict[str, Any]:
    left_manifest, left_artifact, left_errors = validate_bundle(left_dir)
    right_manifest, right_artifact, right_errors = validate_bundle(right_dir)
    validation_errors = ["left: %s" % error for error in left_errors] + [
        "right: %s" % error for error in right_errors
    ]

    left_files = file_inventory(left_dir) if left_dir.is_dir() else {}
    right_files = file_inventory(right_dir) if right_dir.is_dir() else {}
    files = []
    for relative in sorted(set(left_files) | set(right_files)):
        left_path = left_files.get(relative)
        right_path = right_files.get(relative)
        left_digest = sha256(left_path) if left_path else None
        right_digest = sha256(right_path) if right_path else None
        files.append(
            {
                "path": relative,
                "left_size_bytes": left_path.stat().st_size if left_path else None,
                "right_size_bytes": right_path.stat().st_size if right_path else None,
                "left_sha256": left_digest,
                "right_sha256": right_digest,
                "match": left_digest is not None and left_digest == right_digest,
            }
        )

    differences = manifest_differences(left_manifest, right_manifest)
    sections = []
    if left_artifact is not None and right_artifact is not None:
        try:
            sections = compare_sections(left_artifact, right_artifact)
        except ValueError as error:
            validation_errors.append("Wasm section parse failed: %s" % error)

    left_build = left_manifest.get("build", {}) if left_manifest else {}
    right_build = right_manifest.get("build", {}) if right_manifest else {}
    producer_commit = left_build.get("repository_commit")
    source_date_epoch = left_build.get("source_date_epoch")
    if producer_commit != right_build.get("repository_commit"):
        producer_commit = None
    if source_date_epoch != right_build.get("source_date_epoch"):
        source_date_epoch = None

    left_filename = (left_manifest.get("artifact") or {}).get("filename") if left_manifest else None
    right_filename = (right_manifest.get("artifact") or {}).get("filename") if right_manifest else None
    artifact_filename = left_filename if left_filename == right_filename else None

    exact_match = (
        not validation_errors
        and not differences
        and set(left_files) == set(right_files)
        and all(row["match"] for row in files)
    )
    return {
        "schema_version": 1,
        "comparison_type": "shimmy-python-runtime-handoff-exact-bundles",
        "exact_match": exact_match,
        "artifact_filename": artifact_filename,
        "producer_commit": producer_commit,
        "source_date_epoch": source_date_epoch,
        "validation_errors": validation_errors,
        "files": files,
        "manifest_differences": differences,
        "wasm_sections": sections,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("left", type=pathlib.Path)
    parser.add_argument("right", type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()
    report = compare_directories(args.left, args.right)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    return 0 if report["exact_match"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
