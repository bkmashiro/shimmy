from __future__ import annotations

import importlib.util
import json
import pathlib
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = ROOT / "tools/build_numpy_core.py"
PATCH_PATH = ROOT / "patches/numpy/static-core.json"
LINK_PATH = ROOT / "build/link-reactor.mk"


def load_module():
    spec = importlib.util.spec_from_file_location("build_numpy_core", MODULE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("unable to load NumPy builder")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class NumPyBuilderTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.module = load_module()

    def test_exact_static_core_patch_is_idempotent(self) -> None:
        patches = json.loads(PATCH_PATH.read_text())
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            for patch in patches:
                target = root / patch["path"]
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text("prefix\n" + patch["old"] + "suffix\n")
            applied = self.module.apply_patch_set(root, PATCH_PATH)
            first = {path: (root / path).read_text() for path in (item["path"] for item in patches)}
            applied_again = self.module.apply_patch_set(root, PATCH_PATH)
            self.assertEqual(applied, applied_again)
            self.assertEqual({path: (root / path).read_text() for path in first}, first)
            core = (root / patches[0]["path"]).read_text()
            self.assertIn("shimmy_numpy_multiarray_umath", core)
            self.assertNotIn("py.extension_module('_multiarray_umath'", core)

    def test_cross_file_uses_wasi_compilers_and_target_python_shim(self) -> None:
        text = self.module.render_cross_file(
            wasi_sdk=pathlib.Path("/sdk"),
            wasmtime=pathlib.Path("/tools/wasmtime"),
            native_python=pathlib.Path("/native/python"),
            cython=pathlib.Path("/native/cython"),
            target_python_shim=pathlib.Path("/producer/target_python_shim.py"),
            target_python_include=pathlib.Path("/target/Include"),
            target_python_platinclude=pathlib.Path("/target/build"),
        )
        self.assertIn("wasm32-wasip1-clang", text)
        self.assertIn("system = 'wasi'", text)
        self.assertIn("needs_exe_wrapper = true", text)
        self.assertIn("/producer/target_python_shim.py", text)
        self.assertIn("/target/Include", text)
        self.assertIn("/target/build", text)
        self.assertNotIn("agent", text.lower())

    def test_builder_contains_no_prebuilt_runtime_or_external_project(self) -> None:
        source = MODULE_PATH.read_text().lower()
        self.assertNotIn("agent-python-runtime", source)
        self.assertNotIn("webassembly-language-runtimes", source)
        self.assertNotIn("github release", source)

    def test_numpy_archive_precedes_cpython_library_without_linker_group(self) -> None:
        text = LINK_PATH.read_text()
        self.assertLess(text.index("$(SHIMMY_NUMPY_LINK)"), text.index("$(BLDLIBRARY)"))
        self.assertNotIn("--start-group", text)
        self.assertNotIn("--end-group", text)


if __name__ == "__main__":
    unittest.main()
