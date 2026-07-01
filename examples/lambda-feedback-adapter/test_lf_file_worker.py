import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
ADAPTER_DIR = Path(__file__).resolve().parent
if str(ADAPTER_DIR) not in sys.path:
    sys.path.insert(0, str(ADAPTER_DIR))

import lf_file_worker

BOILERPLATE_ROOT = ROOT / "lambda-feedback-fixtures" / "boilerplate-python"
RELATIVE_ROOT = ROOT / "lambda-feedback-fixtures" / "relative-preview"


class LFFileWorkerTest(unittest.TestCase):
    def test_handle_message_wraps_eval_result_for_shimmy_file_adapter(self):
        message = {
            "command": "eval",
            "params": {
                "response": "answer",
                "answer": "answer",
                "params": {"root": str(BOILERPLATE_ROOT), "entrypoint": "evaluation_function.main:evaluation_function"},
            },
        }

        result = lf_file_worker.handle_message(message)

        self.assertEqual(
            result,
            {"command": "eval", "result": {"is_correct": True, "feedback": "Correct"}},
        )

    def test_handle_message_supports_preview_entrypoint_and_params(self):
        message = {
            "command": "preview",
            "params": {
                "response": "foo",
                "params": {"root": str(RELATIVE_ROOT), "entrypoint": "evaluation_function.evaluation:preview_function", "mode": "bar"},
            },
        }

        result = lf_file_worker.handle_message(message)

        self.assertEqual(result, {"command": "preview", "result": {"preview": {"markdown": "Preview: foo / bar"}}})

    def test_handle_message_returns_schema_compatible_error(self):
        result = lf_file_worker.handle_message({"command": "eval", "params": {"response": "x"}})

        self.assertEqual(result["error"]["message"], "missing evaluator params: entrypoint, root")
        self.assertNotIn("result", result)

    def test_handle_message_uses_environment_entrypoint_config(self):
        old_env = {key: os.environ.get(key) for key in ("LF_EVAL_ROOT", "LF_EVAL_ENTRYPOINT", "LF_ENTRYPOINT")}
        try:
            os.environ["LF_EVAL_ROOT"] = str(BOILERPLATE_ROOT)
            os.environ["LF_EVAL_ENTRYPOINT"] = "evaluation_function.main:evaluation_function"
            os.environ.pop("LF_ENTRYPOINT", None)
            result = lf_file_worker.handle_message(
                {"command": "eval", "params": {"response": "42", "answer": "42", "params": {}}}
            )
        finally:
            for key, value in old_env.items():
                if value is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = value

        self.assertEqual(result, {"command": "eval", "result": {"is_correct": True, "feedback": "Correct"}})

    def test_cli_reads_request_file_and_writes_response_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            req = Path(tmp) / "request.json"
            res = Path(tmp) / "response.json"
            req.write_text(
                json.dumps(
                    {
                        "command": "eval",
                        "params": {
                            "response": "a, b",
                            "answer": "b,a",
                            "params": {"root": str(RELATIVE_ROOT), "entrypoint": "evaluation_function.evaluation:evaluation_function", "mode": "set"},
                        },
                    }
                ),
                encoding="utf-8",
            )
            env = os.environ.copy()
            env["EVAL_FILE_NAME_REQUEST"] = str(req)
            env["EVAL_FILE_NAME_RESPONSE"] = str(res)

            completed = subprocess.run(
                [sys.executable, str(ADAPTER_DIR / "lf_file_worker.py")],
                cwd=str(ADAPTER_DIR),
                env=env,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )

            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertEqual(
                json.loads(res.read_text(encoding="utf-8")),
                {"command": "eval", "result": {"is_correct": True, "feedback": "matched"}},
            )


if __name__ == "__main__":
    unittest.main()
