from __future__ import annotations

import importlib.util
import io
import pathlib
import sys
import tarfile
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = ROOT / "tools" / "build_runtime.py"


def load_module():
    spec = importlib.util.spec_from_file_location("build_runtime", MODULE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("unable to load build module")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class BuildRuntimeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.module = load_module()

    def test_cpython_build_plan_uses_official_wasi_entrypoint(self) -> None:
        command = self.module.cpython_build_command(
            pathlib.Path("/work/Python-3.14.6"),
            pathlib.Path("/work/wasi-sdk"),
            8,
        )
        self.assertEqual(command[:3], [sys.executable, "Tools/wasm/wasi", "build"])
        self.assertEqual(
            command[-4:], ["--wasi-sdk", "/work/wasi-sdk", "--parallel", "8"]
        )

    def test_verify_blob_checks_size_and_digest(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "source.bin"
            path.write_bytes(b"shimmy")
            self.module.verify_blob(
                path,
                expected_size=6,
                expected_sha256=(
                    "5fce9515359f4d3533aa138c0e88369bec576ce56b11742c81ac8376425cf379"
                ),
            )
            with self.assertRaisesRegex(ValueError, "size mismatch"):
                self.module.verify_blob(path, expected_size=5, expected_sha256="0" * 64)

    def test_safe_tar_rejects_parent_traversal(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            archive = root / "bad.tar"
            payload = root / "payload"
            payload.write_text("bad")
            with tarfile.open(archive, "w") as handle:
                handle.add(payload, arcname="../escape")
            with self.assertRaisesRegex(ValueError, "unsafe archive member"):
                self.module.extract_archive(archive, root / "out")

    def test_safe_tar_allows_relative_link_within_archive_root(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            archive = root / "sdk.tar"
            with tarfile.open(archive, "w") as handle:
                target = tarfile.TarInfo("sdk/share/man/man3/el_init.3")
                target.size = 2
                handle.addfile(target, io.BytesIO(b"ok"))
                link = tarfile.TarInfo("sdk/share/man/man3/el_tok_init.3")
                link.type = tarfile.SYMTYPE
                link.linkname = "el_init.3"
                handle.addfile(link)
            output = root / "out"
            self.module.extract_archive(archive, output)
            self.assertEqual((output / "sdk/share/man/man3/el_tok_init.3").read_text(), "ok")

    def test_safe_tar_rejects_link_escaping_archive_root(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            archive = root / "bad-link.tar"
            with tarfile.open(archive, "w") as handle:
                link = tarfile.TarInfo("sdk/link")
                link.type = tarfile.SYMTYPE
                link.linkname = "../../escape"
                handle.addfile(link)
            with self.assertRaisesRegex(ValueError, "unsafe archive link"):
                self.module.extract_archive(archive, root / "out")

    def test_source_contains_no_external_runtime_dependency(self) -> None:
        source = MODULE_PATH.read_text().lower()
        self.assertIn('wasmtime_root / "wasmtime"', source)
        self.assertNotIn('wasmtime_root / "bin" / "wasmtime"', source)
        self.assertNotIn("agent-python-runtime", source)
        self.assertNotIn("webassembly-language-runtimes", source)
        self.assertNotIn("latest", source)


if __name__ == "__main__":
    unittest.main()
