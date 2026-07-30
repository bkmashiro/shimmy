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
        patch = json.loads(PATCH_PATH.read_text())[0]
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            target = root / patch["path"]
            target.parent.mkdir(parents=True)
            target.write_text("prefix\n" + patch["old"] + "suffix\n")
            applied = self.module.apply_patch_set(root, PATCH_PATH)
            first = target.read_text()
            applied_again = self.module.apply_patch_set(root, PATCH_PATH)
            self.assertEqual(applied, applied_again)
            self.assertEqual(target.read_text(), first)
            self.assertIn("shimmy_numpy_multiarray_umath", first)
            self.assertNotIn("py.extension_module('_multiarray_umath'", first)

    def test_cross_file_uses_wasi_compilers_and_target_python_shim(self) -> None:
        text = self.module.render_cross_file(
            wasi_sdk=pathlib.Path("/sdk"),
            wasmtime=pathlib.Path("/tools/wasmtime"),
            native_python=pathlib.Path("/native/python"),
            cython=pathlib.Path("/native/cython"),
            target_python_shim=pathlib.Path("/producer/target_python_shim.py"),
        )
        self.assertIn("wasm32-wasip1-clang", text)
        self.assertIn("system = 'wasi'", text)
        self.assertIn("needs_exe_wrapper = true", text)
        self.assertIn("/producer/target_python_shim.py", text)
        self.assertNotIn("agent", text.lower())

    def test_builder_contains_no_prebuilt_runtime_or_external_project(self) -> None:
        source = MODULE_PATH.read_text().lower()
        self.assertNotIn("agent-python-runtime", source)
        self.assertNotIn("webassembly-language-runtimes", source)
        self.assertNotIn("github release", source)


if __name__ == "__main__":
    unittest.main()
