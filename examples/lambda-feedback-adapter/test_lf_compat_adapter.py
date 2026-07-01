import json
import os
import subprocess
import sys
import tempfile
import unittest
from dataclasses import dataclass
from pathlib import Path

ADAPTER_DIR = Path(__file__).resolve().parent
FIXTURE_DIR = ADAPTER_DIR.parent / "lambda-feedback-fixtures"
if str(ADAPTER_DIR) not in sys.path:
    sys.path.insert(0, str(ADAPTER_DIR))

from lf_compat_adapter import call_function, load_entrypoint, normalize_result, run_entrypoint


class LFCompatAdapterTests(unittest.TestCase):
    def test_eval_entrypoint_normalizes_toolkit_result(self):
        fn = load_entrypoint(
            FIXTURE_DIR / "boilerplate-python",
            "evaluation_function.main:evaluation_function",
        )

        result = call_function(
            fn,
            method="eval",
            response=" 42 ",
            answer="42",
            params={},
        )

        self.assertEqual(
            result,
            {"is_correct": True, "feedback": "Correct"},
        )

    def test_preview_supports_two_argument_signature_and_relative_imports(self):
        fn = load_entrypoint(
            FIXTURE_DIR / "relative-preview",
            "evaluation_function.evaluation:preview_function",
        )

        result = call_function(
            fn,
            method="preview",
            response=" Foo ",
            answer="unused",
            params={"mode": "set"},
        )

        self.assertEqual(result, {"preview": {"markdown": "Preview: foo / set"}})

    def test_preview_supports_optional_two_argument_signature(self):
        def preview_function(response, params=None):
            return {"preview": {"response": response, "mode": (params or {}).get("mode")}}

        result = call_function(
            preview_function,
            method="preview",
            response="draft",
            answer="unused",
            params={"mode": "optional-two-arg"},
        )

        self.assertEqual(result, {"preview": {"response": "draft", "mode": "optional-two-arg"}})

    def test_preview_keeps_three_argument_signature_with_default_answer(self):
        def preview_function(response, answer=None, params=None):
            return {"preview": {"response": response, "answer": answer, "mode": (params or {}).get("mode")}}

        result = call_function(
            preview_function,
            method="preview",
            response="draft",
            answer="expected",
            params={"mode": "three-arg"},
        )

        self.assertEqual(result, {"preview": {"response": "draft", "answer": "expected", "mode": "three-arg"}})

    def test_eval_supports_relative_import_fixture(self):
        fn = load_entrypoint(
            FIXTURE_DIR / "relative-preview",
            "evaluation_function.evaluation:evaluation_function",
        )

        result = call_function(
            fn,
            method="eval",
            response="a, b",
            answer="b,a",
            params={"mode": "set"},
        )

        self.assertEqual(result["is_correct"], True)
        self.assertEqual(result["feedback"], "matched")

    def test_normalize_result_handles_common_object_shapes_and_scalar_items(self):
        @dataclass
        class DataResult:
            is_correct: bool
            score: object

        class Scalar:
            def item(self):
                return 7

        self.assertEqual(
            normalize_result(DataResult(is_correct=True, score=Scalar())),
            {"is_correct": True, "score": 7},
        )

    def test_run_entrypoint_uses_temporary_workspace_and_restores_process_state(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "evaluator"
            package = root / "evaluation_function"
            package.mkdir(parents=True)
            (package / "__init__.py").write_text("", encoding="utf-8")
            (package / "main.py").write_text(
                """
import os
from pathlib import Path


def evaluation_function(response, answer, params):
    print("hello from evaluator")
    print("warn from evaluator", file=__import__("sys").stderr)
    cwd = Path.cwd()
    (cwd / "scratch.txt").write_text("temporary", encoding="utf-8")
    os.environ["LF_CONTEXT_MUTATION"] = "dirty"
    return {
        "cwd_is_request_workspace": cwd.name.startswith("lf-eval-"),
        "mode": params.get("mode"),
        "response": response,
    }
""",
                encoding="utf-8",
            )
            before_cwd = Path.cwd()
            before_sys_path = list(sys.path)
            old_env = os.environ.get("LF_CONTEXT_MUTATION")

            call = run_entrypoint(
                root,
                "evaluation_function.main:evaluation_function",
                method="eval",
                response="draft",
                answer="expected",
                params={"mode": "hygiene"},
            )

            self.assertEqual(
                call.result,
                {
                    "cwd_is_request_workspace": True,
                    "mode": "hygiene",
                    "response": "draft",
                },
            )
            self.assertEqual(call.stdout.strip(), "hello from evaluator")
            self.assertEqual(call.stderr.strip(), "warn from evaluator")
            self.assertEqual(Path.cwd(), before_cwd)
            self.assertEqual(sys.path, before_sys_path)
            self.assertFalse(Path(call.workdir).exists())
            self.assertFalse((root / "scratch.txt").exists())
            self.assertEqual(os.environ.get("LF_CONTEXT_MUTATION"), old_env)

    def test_cli_runner_outputs_normalized_json(self):
        runner = ADAPTER_DIR / "run_lf_eval.py"
        completed = subprocess.run(
            [
                sys.executable,
                str(runner),
                "--root",
                str(FIXTURE_DIR / "boilerplate-python"),
                "--entrypoint",
                "evaluation_function.main:evaluation_function",
                "--method",
                "eval",
                "--response",
                "no",
                "--answer",
                "yes",
                "--params-json",
                "{}",
            ],
            check=True,
            text=True,
            stdout=subprocess.PIPE,
        )

        self.assertEqual(
            json.loads(completed.stdout),
            {"is_correct": False, "feedback": "Try again"},
        )


if __name__ == "__main__":
    unittest.main()
