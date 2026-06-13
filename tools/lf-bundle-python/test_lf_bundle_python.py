from __future__ import annotations

import importlib.util
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
BUNDLER = Path(__file__).resolve().parent / "lf_bundle_python.py"
FIXTURES = ROOT / "examples" / "lambda-feedback-fixtures"
ADAPTER = ROOT / "examples" / "lambda-feedback-adapter"


def load_module(path: Path):
    spec = importlib.util.spec_from_file_location("generated_bundle", path)
    assert spec is not None
    assert spec.loader is not None
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


def test_bundle_boilerplate_generates_standalone_eval_and_preview(tmp_path: Path) -> None:
    out = tmp_path / "boilerplate.bundle.py"

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

    assert result.returncode == 0, result.stderr
    assert out.exists()
    bundle = load_module(out)
    assert bundle.evaluation_function("2", "2", {}) == {"is_correct": True}
    assert bundle.preview_function("x + 1", "", {}) == {"preview": {"sympy": "x + 1"}}


def test_generated_bundle_does_not_need_fixture_paths_at_runtime(tmp_path: Path) -> None:
    out = tmp_path / "boilerplate.bundle.py"
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
    assert result.returncode == 0, result.stderr

    smoke = subprocess.run(
        [
            sys.executable,
            "-c",
            "import importlib.util; "
            f"p={str(out)!r}; "
            "s=importlib.util.spec_from_file_location('b', p); "
            "m=importlib.util.module_from_spec(s); s.loader.exec_module(m); "
            "print(m.evaluation_function('a','a',{})); "
            "print(m.preview_function('a','',{}))",
        ],
        cwd=tmp_path,
        capture_output=True,
        text=True,
        check=False,
    )

    assert smoke.returncode == 0, smoke.stderr
    assert "{'is_correct': True}" in smoke.stdout
    assert "{'preview': {'sympy': 'a'}}" in smoke.stdout


def test_bundle_fails_when_entrypoint_module_is_missing(tmp_path: Path) -> None:
    result = run_bundler(
        "--root",
        str(FIXTURES / "boilerplate-python"),
        "--adapter-root",
        str(ADAPTER),
        "--eval-entrypoint",
        "evaluation_function.missing:evaluation_function",
        "--out",
        str(tmp_path / "bad.bundle.py"),
    )

    assert result.returncode != 0
    assert "evaluation_function.missing" in result.stderr
