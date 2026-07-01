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
    def with_env(self, updates, removed=()):
        class EnvGuard:
            def __enter__(inner_self):
                keys = set(updates) | set(removed)
                inner_self.old_env = {key: os.environ.get(key) for key in keys}
                for key in removed:
                    os.environ.pop(key, None)
                for key, value in updates.items():
                    os.environ[key] = value

            def __exit__(inner_self, exc_type, exc, tb):
                for key, value in inner_self.old_env.items():
                    if value is None:
                        os.environ.pop(key, None)
                    else:
                        os.environ[key] = value

        return EnvGuard()

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

    def test_handle_message_ignores_legacy_short_env_names(self):
        with self.with_env(
            {
                "LF_EVAL_ROOT": str(BOILERPLATE_ROOT),
                "LF_EVAL_ENTRYPOINT": "evaluation_function.main:evaluation_function",
            },
            removed=("FUNCTION_LF_ROOT", "FUNCTION_LF_ENTRYPOINT", "FUNCTION_LF_PREVIEW_ENTRYPOINT", "FUNCTION_LF_EVAL_ENTRYPOINT"),
        ):
            result = lf_file_worker.handle_message(
                {"command": "eval", "params": {"response": "42", "answer": "42", "params": {}}}
            )

        self.assertEqual(result["error"]["message"], "missing evaluator params: entrypoint, root")

    def test_handle_message_uses_function_lf_env_names_for_package_mode(self):
        with self.with_env(
            {
                "FUNCTION_LF_ROOT": str(BOILERPLATE_ROOT),
                "FUNCTION_LF_ENTRYPOINT": "evaluation_function.main:evaluation_function",
            },
            removed=("LF_EVAL_ROOT", "LF_EVAL_ENTRYPOINT", "LF_ENTRYPOINT"),
        ):
            result = lf_file_worker.handle_message(
                {"command": "eval", "params": {"response": "42", "answer": "42", "params": {}}}
            )

        self.assertEqual(result, {"command": "eval", "result": {"is_correct": True, "feedback": "Correct"}})

    def test_handle_message_uses_command_specific_function_lf_preview_entrypoint(self):
        with self.with_env(
            {
                "FUNCTION_LF_ROOT": str(BOILERPLATE_ROOT),
                "FUNCTION_LF_ENTRYPOINT": "evaluation_function.main:evaluation_function",
                "FUNCTION_LF_PREVIEW_ENTRYPOINT": "evaluation_function.main:preview_function",
            },
            removed=("LF_EVAL_ROOT", "LF_EVAL_ENTRYPOINT", "LF_PREVIEW_ENTRYPOINT", "LF_ENTRYPOINT"),
        ):
            result = lf_file_worker.handle_message(
                {"command": "preview", "params": {"response": "draft", "params": {"expected": "answer"}}}
            )

        self.assertEqual(
            result,
            {"command": "preview", "result": {"preview": {"response": "draft", "expected": "answer"}}},
        )

    def test_handle_message_runs_evaluator_with_hygiene_context(self):
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
    print("worker stdout")
    Path("worker-scratch.txt").write_text("scratch", encoding="utf-8")
    os.environ["LF_WORKER_MUTATION"] = "dirty"
    return {"cwd_is_workspace": Path.cwd().name.startswith("lf-eval-"), "response": response}
""",
                encoding="utf-8",
            )
            before_cwd = Path.cwd()
            scratch = before_cwd / "worker-scratch.txt"
            if scratch.exists():
                scratch.unlink()
            old_env = os.environ.get("LF_WORKER_MUTATION")

            result = lf_file_worker.handle_message(
                {
                    "command": "eval",
                    "params": {
                        "response": "draft",
                        "answer": "expected",
                        "params": {"root": str(root), "entrypoint": "evaluation_function.main:evaluation_function"},
                    },
                }
            )

            self.assertEqual(result, {"command": "eval", "result": {"cwd_is_workspace": True, "response": "draft"}})
            self.assertEqual(Path.cwd(), before_cwd)
            self.assertFalse(scratch.exists())
            self.assertEqual(os.environ.get("LF_WORKER_MUTATION"), old_env)

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
