import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest


HANDOFF_DIR = pathlib.Path(__file__).resolve().parents[1]


def load_module(name: str, filename: str):
    spec = importlib.util.spec_from_file_location(name, HANDOFF_DIR / filename)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {filename}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


COMPARE = load_module("compare_python_runtime_bundles", "compare_bundles.py")
FLOAT128 = load_module("validate_python_runtime_float128", "validate_float128.py")


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def uleb(value: int) -> bytes:
    encoded = bytearray()
    while True:
        byte = value & 0x7F
        value >>= 7
        if value:
            encoded.append(byte | 0x80)
        else:
            encoded.append(byte)
            return bytes(encoded)


def wasm_with_data(payload: bytes) -> bytes:
    return b"\x00asm\x01\x00\x00\x00" + b"\x0b" + uleb(len(payload)) + payload


def write_checksums(directory: pathlib.Path) -> None:
    rows = []
    for path in sorted(directory.iterdir()):
        if path.is_file() and path.name != "SHA256SUMS":
            rows.append(f"{sha256(path.read_bytes())}  {path.name}\n")
    (directory / "SHA256SUMS").write_text("".join(rows))


def write_bundle(
    directory: pathlib.Path,
    wasm: bytes,
    *,
    commit: str = "a" * 40,
    epoch: str = "1784758934",
) -> None:
    directory.mkdir(parents=True)
    artifact_name = "agent-python-runtime.wasm"
    (directory / artifact_name).write_bytes(wasm)
    manifest = {
        "schema_version": 2,
        "abi_version": "v1",
        "artifact_profile": "base",
        "target": "wasm32-wasip1",
        "artifact": {
            "filename": artifact_name,
            "size": len(wasm),
            "sha256": sha256(wasm),
        },
        "build": {
            "repository_commit": commit,
            "source_date_epoch": epoch,
            "compiler_target": "wasm32-wasip1",
            "execution_model": "reactor",
        },
        "wasm": {"imports": [], "exports": []},
    }
    (directory / "manifest.json").write_text(
        json.dumps(manifest, indent=2, sort_keys=True) + "\n"
    )
    (directory / "sbom.spdx.json").write_text("{}\n")
    (directory / "THIRD_PARTY_NOTICES.md").write_text("locked notices\n")
    write_checksums(directory)


class BundleComparisonTests(unittest.TestCase):
    def test_exact_bundles_are_the_only_success(self):
        wasm = wasm_with_data(b"same")
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            write_bundle(root / "left", wasm)
            write_bundle(root / "right", wasm)
            report = COMPARE.compare_directories(root / "left", root / "right")
        self.assertTrue(report["exact_match"])
        self.assertEqual(report["producer_commit"], "a" * 40)
        self.assertEqual(report["source_date_epoch"], "1784758934")
        self.assertEqual(report["artifact_filename"], "agent-python-runtime.wasm")
        self.assertEqual(report["manifest_differences"], [])
        self.assertTrue(all(row["match"] for row in report["files"]))
        self.assertTrue(all(row["match"] for row in report["wasm_sections"]))

    def test_wasm_section_mismatch_is_diagnostic_but_never_accepted(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            write_bundle(root / "left", wasm_with_data(b"left"))
            write_bundle(root / "right", wasm_with_data(b"right"))
            report = COMPARE.compare_directories(root / "left", root / "right")
        self.assertFalse(report["exact_match"])
        mismatch = next(row for row in report["wasm_sections"] if not row["match"])
        self.assertEqual(mismatch["section_id"], 11)
        self.assertNotEqual(mismatch["left_sha256"], mismatch["right_sha256"])

    def test_extra_bundle_file_is_an_exact_mismatch_even_when_checksummed(self):
        wasm = wasm_with_data(b"same")
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            write_bundle(root / "left", wasm)
            write_bundle(root / "right", wasm)
            (root / "right" / "unexpected.txt").write_text("unexpected\n")
            write_checksums(root / "right")
            report = COMPARE.compare_directories(root / "left", root / "right")
        self.assertFalse(report["exact_match"])
        extra = next(row for row in report["files"] if row["path"] == "unexpected.txt")
        self.assertIsNone(extra["left_sha256"])
        self.assertFalse(extra["match"])

    def test_manifest_and_checksum_binding_fail_closed(self):
        wasm = wasm_with_data(b"same")
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            write_bundle(root / "left", wasm)
            write_bundle(root / "right", wasm)
            (root / "right" / "agent-python-runtime.wasm").write_bytes(
                wasm_with_data(b"tampered")
            )
            report = COMPARE.compare_directories(root / "left", root / "right")
        self.assertFalse(report["exact_match"])
        self.assertTrue(
            any("artifact digest" in error for error in report["validation_errors"]),
            report,
        )
        self.assertTrue(
            any("SHA256SUMS" in error for error in report["validation_errors"]),
            report,
        )

    def test_symlinks_are_rejected_even_if_both_bundles_match(self):
        wasm = wasm_with_data(b"same")
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            write_bundle(root / "left", wasm)
            write_bundle(root / "right", wasm)
            for side in ("left", "right"):
                link = root / side / "linked-notices.md"
                link.symlink_to("THIRD_PARTY_NOTICES.md")
                write_checksums(root / side)
            report = COMPARE.compare_directories(root / "left", root / "right")
        self.assertFalse(report["exact_match"])
        self.assertTrue(
            any("symlink" in error for error in report["validation_errors"]), report
        )

    def test_commit_or_epoch_drift_is_never_reproducible(self):
        wasm = wasm_with_data(b"same")
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            write_bundle(root / "left", wasm)
            write_bundle(root / "right", wasm, commit="b" * 40, epoch="1784758935")
            report = COMPARE.compare_directories(root / "left", root / "right")
        self.assertFalse(report["exact_match"])
        pointers = {row["pointer"] for row in report["manifest_differences"]}
        self.assertIn("/build/repository_commit", pointers)
        self.assertIn("/build/source_date_epoch", pointers)


class Float128ContractTests(unittest.TestCase):
    def valid_result(self):
        return {
            "longdouble_itemsize": 16,
            "longdouble_nmant": 112,
            "double_nmant": 52,
            "preserves_extra_precision": True,
            "narrows_to_double_one": True,
            "epsilon_is_narrower": True,
        }

    def test_binary128_result_is_accepted(self):
        validated = FLOAT128.validate_result({"result": self.valid_result()})
        self.assertEqual(validated["longdouble_nmant"], 112)

    def test_x86_extended_or_binary64_shape_is_rejected(self):
        result = self.valid_result()
        result["longdouble_nmant"] = 63
        with self.assertRaisesRegex(ValueError, "binary128 mantissa"):
            FLOAT128.validate_result(result)

    def test_truncated_extra_precision_is_rejected(self):
        result = self.valid_result()
        result["preserves_extra_precision"] = False
        with self.assertRaisesRegex(ValueError, "extra precision"):
            FLOAT128.validate_result(result)

    def test_canary_uses_value_beyond_binary64_precision(self):
        source = (HANDOFF_DIR / "float128_canary.py").read_text()
        self.assertIn("1.0000000000000000000000000000000002", source)
        self.assertIn("np.longdouble", source)
        self.assertIn("longdouble_nmant", source)
        self.assertIn("preserves_extra_precision", source)


if __name__ == "__main__":
    unittest.main()
