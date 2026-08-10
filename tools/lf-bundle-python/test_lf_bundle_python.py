from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
BUNDLER = HERE / "lf_bundle_python.py"
ADAPTER = HERE / "adapter"


def load_module(path: Path):
    spec = importlib.util.spec_from_file_location("generated_bundle", path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run_bundler(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(BUNDLER), *args],
        cwd=HERE,
        capture_output=True,
        text=True,
        check=False,
    )


def write_package(root: Path, evaluation_source: str, preview_source: str | None = None) -> None:
    package = root / "evaluation_function"
    package.mkdir(parents=True)
    (package / "__init__.py").write_text("")
    (package / "helpers.py").write_text("def same(left, right):\n    return left == right\n")
    (package / "evaluation.py").write_text(evaluation_source)
    if preview_source is not None:
        (package / "preview.py").write_text(preview_source)


class BundleTests(unittest.TestCase):
    def test_package_dispatch_and_toolkit_result_normalization(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            tmp = Path(td)
            fixture = tmp / "fixture"
            write_package(
                fixture,
                "from lf_toolkit.evaluation import Result\n"
                "from .helpers import same\n"
                "def evaluation_function(response, answer, params):\n"
                "    return Result(is_correct=same(response, answer))\n",
                "from lf_toolkit.preview import Result, Preview\n"
                "def preview_function(response, params):\n"
                "    return Result(preview=Preview(sympy=response))\n",
            )
            out = tmp / "bundle.py"
            result = run_bundler(
                "--root", str(fixture),
                "--adapter-root", str(ADAPTER),
                "--eval-entrypoint", "evaluation_function.evaluation:evaluation_function",
                "--preview-entrypoint", "evaluation_function.preview:preview_function",
                "--out", str(out),
            )
            self.assertEqual(0, result.returncode, result.stderr)
            bundle = load_module(out)
            self.assertEqual(
                {"is_correct": True},
                bundle.dispatch("eval", {"response": "2", "answer": "2", "params": {}}),
            )
            self.assertEqual(
                {"is_correct": True},
                bundle.evaluation_function("2", "2", {}),
            )
            self.assertEqual(
                {"preview": {"sympy": "x + 1"}},
                bundle.dispatch("preview", {"response": "x + 1", "params": {}}),
            )

    def test_pure_python_include_root_satisfies_dependency(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            tmp = Path(td)
            fixture = tmp / "fixture"
            write_package(
                fixture,
                "from helper_pkg import value\n"
                "def evaluation_function(response, answer, params):\n"
                "    return {'is_correct': value() == 42}\n",
            )
            include_root = tmp / "vendor"
            helper = include_root / "helper_pkg"
            helper.mkdir(parents=True)
            (helper / "__init__.py").write_text("def value():\n    return 42\n")
            out = tmp / "bundle.py"
            result = run_bundler(
                "--root", str(fixture),
                "--adapter-root", str(ADAPTER),
                "--include-root", str(include_root),
                "--eval-entrypoint", "evaluation_function.evaluation:evaluation_function",
                "--out", str(out),
            )
            self.assertEqual(0, result.returncode, result.stderr)
            self.assertTrue(load_module(out).dispatch("eval", {"response": 0, "answer": 0, "params": {}})["is_correct"])

    def test_missing_third_party_module_fails_before_writing_bundle(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            tmp = Path(td)
            fixture = tmp / "fixture"
            write_package(
                fixture,
                "from sympy import simplify\n"
                "def evaluation_function(response, answer, params):\n"
                "    return {'is_correct': simplify(response) == simplify(answer)}\n",
            )
            out = tmp / "bundle.py"
            result = run_bundler(
                "--root", str(fixture),
                "--adapter-root", str(ADAPTER),
                "--eval-entrypoint", "evaluation_function.evaluation:evaluation_function",
                "--out", str(out),
            )
            self.assertNotEqual(0, result.returncode)
            self.assertIn("unresolved imports: sympy", result.stderr)
            self.assertIn("--include-root", result.stderr)
            self.assertFalse(out.exists())

    def test_runtime_modules_come_from_bound_manifest(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            tmp = Path(td)
            fixture = tmp / "fixture"
            write_package(
                fixture,
                "import numpy as np\n"
                "def evaluation_function(response, answer, params):\n"
                "    return {'is_correct': np.allclose(response, answer)}\n",
            )
            manifest = tmp / "manifest.json"
            manifest.write_text(json.dumps({
                "schema": "shimmy-python-runtime-artifact/v1",
                "artifact_contract": "shimmy-python-runtime/v1",
                "profile": "numpy-core",
                "python_modules": ["numpy"],
            }))
            out = tmp / "bundle.py"
            result = run_bundler(
                "--root", str(fixture),
                "--adapter-root", str(ADAPTER),
                "--runtime-manifest", str(manifest),
                "--eval-entrypoint", "evaluation_function.evaluation:evaluation_function",
                "--out", str(out),
            )
            self.assertEqual(0, result.returncode, result.stderr)
            self.assertTrue(out.exists())

    def test_adapter_normalizes_to_dict_and_scalar_item(self) -> None:
        adapter_path = ADAPTER / "lf_compat_adapter.py"
        adapter = load_module(adapter_path)

        class ToolkitResult:
            def to_dict(self):
                return {"is_correct": Scalar(True)}

        class Scalar:
            def __init__(self, value):
                self.value = value

            def item(self):
                return self.value

        self.assertEqual({"is_correct": True}, adapter.normalize_result(ToolkitResult()))


if __name__ == "__main__":
    unittest.main()
