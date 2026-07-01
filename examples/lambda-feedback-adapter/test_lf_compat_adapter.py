import json
import subprocess
import sys
import unittest
from dataclasses import dataclass
from pathlib import Path

ADAPTER_DIR = Path(__file__).resolve().parent
FIXTURE_DIR = ADAPTER_DIR.parent / "lambda-feedback-fixtures"
if str(ADAPTER_DIR) not in sys.path:
    sys.path.insert(0, str(ADAPTER_DIR))

from lf_compat_adapter import call_function, load_entrypoint, normalize_result


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
