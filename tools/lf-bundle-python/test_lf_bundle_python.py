from __future__ import annotations

import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
BUNDLER = Path(__file__).resolve().parent / "lf_bundle_python.py"
FIXTURES = ROOT / "examples" / "lambda-feedback-fixtures"
ADAPTER = Path(__file__).resolve().parent / "adapter"


def load_module(path: Path):
    spec = importlib.util.spec_from_file_location("generated_bundle", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run_bundler(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(BUNDLER), *args],
        cwd=ROOT,
        capture_output=True,
        text=True,
        check=False,
    )


class BundleTests(unittest.TestCase):
    def test_bundle_boilerplate_generates_reactor_functions(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory) / "boilerplate.bundle.py"
            result = run_bundler(
                "--root",
                str(FIXTURES / "boilerplate-python"),
                "--adapter-root",
                str(ADAPTER),
                "--eval-entrypoint",
                "evaluation_function.evaluation:evaluation_function",
                "--preview-entrypoint",
                "evaluation_function.preview:preview_function",
                "--out",
                str(out),
            )

            self.assertEqual(0, result.returncode, result.stderr)
            bundle = load_module(out)
            self.assertEqual(
                {"is_correct": True},
                bundle.evaluation_function("2", "2", {}),
            )
            self.assertEqual(
                {"preview": {"sympy": "x + 1"}},
                bundle.preview_function("x + 1", "", {}),
            )

    def test_generated_bundle_runs_without_fixture_paths(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory) / "boilerplate.bundle.py"
            result = run_bundler(
                "--root",
                str(FIXTURES / "boilerplate-python"),
                "--adapter-root",
                str(ADAPTER),
                "--eval-entrypoint",
                "evaluation_function.evaluation:evaluation_function",
                "--preview-entrypoint",
                "evaluation_function.preview:preview_function",
                "--out",
                str(out),
            )
            self.assertEqual(0, result.returncode, result.stderr)

            smoke = subprocess.run(
                [
                    sys.executable,
                    "-c",
                    "import importlib.util; "
                    f"spec=importlib.util.spec_from_file_location('bundle', {str(out)!r}); "
                    "module=importlib.util.module_from_spec(spec); spec.loader.exec_module(module); "
                    "print(module.evaluation_function('a','a',{})); "
                    "print(module.preview_function('a',{}))",
                ],
                cwd=Path(directory),
                capture_output=True,
                text=True,
                check=False,
            )

            self.assertEqual(0, smoke.returncode, smoke.stderr)
            self.assertIn("{'is_correct': True}", smoke.stdout)
            self.assertIn("{'preview': {'sympy': 'a'}}", smoke.stdout)

    def test_bundle_embeds_relative_package_modules(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            package = root / "evaluator_pkg"
            package.mkdir()
            (package / "__init__.py").write_text("")
            (package / "helpers.py").write_text("def expected():\n    return 42\n")
            (package / "evaluation.py").write_text(
                "from .helpers import expected\n"
                "def evaluation_function(response, answer, params):\n"
                "    return {'is_correct': response == expected()}\n"
            )
            out = root / "relative.bundle.py"
            result = run_bundler(
                "--root",
                str(root),
                "--adapter-root",
                str(ADAPTER),
                "--eval-entrypoint",
                "evaluator_pkg.evaluation:evaluation_function",
                "--out",
                str(out),
            )

            self.assertEqual(0, result.returncode, result.stderr)
            bundle = load_module(out)
            self.assertEqual(
                {"is_correct": True},
                bundle.evaluation_function(42, 0, {}),
            )

    def test_adapter_normalizes_nested_numpy_style_scalars(self) -> None:
        adapter = load_module(ADAPTER / "lf_compat_adapter.py")

        class Scalar:
            def __init__(self, value):
                self.value = value

            def item(self):
                return self.value

        class FloatScalar(float):
            def item(self):
                return float(self)

        class Result:
            def to_dict(self):
                return {
                    "is_correct": Scalar(True),
                    "allowed_diff": FloatScalar(0.5),
                }

        self.assertEqual(
            {"is_correct": True, "allowed_diff": 0.5},
            adapter.normalize_result(Result()),
        )

    def test_bundle_fails_when_entrypoint_module_is_missing(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            result = run_bundler(
                "--root",
                str(FIXTURES / "boilerplate-python"),
                "--adapter-root",
                str(ADAPTER),
                "--eval-entrypoint",
                "evaluation_function.missing:evaluation_function",
                "--out",
                str(Path(directory) / "bad.bundle.py"),
            )

            self.assertNotEqual(0, result.returncode)
            self.assertIn("evaluation_function.missing", result.stderr)


if __name__ == "__main__":
    unittest.main()
