import importlib.util
import json
import pathlib
import unittest

MODULE_PATH = pathlib.Path(__file__).with_name("safe_eval.py")
SPEC = importlib.util.spec_from_file_location("safe_eval", MODULE_PATH)
assert SPEC is not None
SAFE_EVAL = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(SAFE_EVAL)

LIMITS = {
    "max_code_bytes": 65536,
    "max_input_bytes": 65536,
    "max_output_bytes": 65536,
    "max_tests": 32,
}


def invoke(response, params=None, answer=None, method="eval", limits=None):
    request = {
        "method": method,
        "payload": {"response": response, "answer": answer, "params": params or {}},
    }
    return json.loads(SAFE_EVAL.invoke(json.dumps(request), json.dumps(limits or LIMITS)))


class SafeEvalTest(unittest.TestCase):
    def test_reactor_entrypoints_are_directly_callable(self):
        evaluated = SAFE_EVAL.evaluation_function(
            "print(6 * 7)", "", {"mode": "demo"}
        )
        previewed = SAFE_EVAL.preview_function("import socket", {})
        self.assertEqual(evaluated["stdout"], "42\n")
        self.assertFalse(previewed["valid"])

    def test_trusted_script_fits_reactor_payload_bound(self):
        self.assertLess(MODULE_PATH.stat().st_size, 1024 * 1024)

    def test_demo_captures_stdout(self):
        result = invoke("print(6 * 7)", {"mode": "demo"})
        self.assertFalse(result["is_correct"])
        self.assertEqual(result["stdout"], "42\n")

    def test_io_tests_use_fresh_namespaces_and_hide_hidden_values(self):
        result = invoke(
            "value = int(input())\nprint(value * value)",
            {
                "mode": "io_test",
                "tests": [
                    {"input": "5\n", "expected_output": "25\n"},
                    {"input": "3\n", "expected_output": "8\n", "hidden": True},
                ],
            },
        )
        self.assertFalse(result["is_correct"])
        self.assertEqual(result["feedback"], "1/2 tests passed.")
        self.assertNotIn("actual", result["tests"][1])
        self.assertNotIn("expected", result["tests"][1])

    def test_injected_io_test(self):
        result = invoke(
            "print(n + 1)",
            {"mode": "io_test", "tests": [{"inject": {"n": 4}, "expected_output": "5\n"}]},
        )
        self.assertTrue(result["is_correct"])

    def test_unit_test_discovers_plain_test_functions(self):
        result = invoke(
            "def square(value):\n    return value * value",
            {"mode": "unit_test", "test_code": "def test_square():\n    assert square(5) == 25"},
        )
        self.assertTrue(result["is_correct"])
        self.assertEqual(result["feedback"], "1/1 unit tests passed.")

    def test_preview_reports_blocked_host_capabilities(self):
        result = invoke("import js\njs.process.exit(0)", method="preview")
        self.assertFalse(result["valid"])
        self.assertIn("import of 'js' is not allowed", result["preview"])

    def test_runtime_rejects_blocked_host_capabilities(self):
        result = invoke("import subprocess\nsubprocess.run(['id'])")
        self.assertFalse(result["is_correct"])
        self.assertEqual(result["kind"], "validation")

    def test_code_limit_fails_closed(self):
        limits = {**LIMITS, "max_code_bytes": 8}
        result = invoke("print('too long')", limits=limits)
        self.assertFalse(result["is_correct"])
        self.assertEqual(result["kind"], "validation")
        self.assertIn("exceeds 8 bytes", result["feedback"])

    def test_output_is_truncated(self):
        limits = {**LIMITS, "max_output_bytes": 32}
        result = invoke("print('x' * (1024 * 1024))", limits=limits)
        self.assertTrue(result["truncated"])
        self.assertLessEqual(len(result["stdout"].encode()), 32)

    def test_output_writer_retains_at_most_the_byte_limit(self):
        writer = SAFE_EVAL._BoundedTextWriter(31)
        writer.write("λ" * (1024 * 1024))
        self.assertEqual(writer.retained_bytes, 31)
        self.assertTrue(writer.truncated)
        self.assertLessEqual(len(writer.getvalue().encode()), 31)

    def test_unit_test_output_is_bounded_while_running(self):
        limits = {**LIMITS, "max_output_bytes": 32}
        result = invoke(
            "def square(value):\n    return value * value",
            {
                "mode": "unit_test",
                "test_code": (
                    "print('x' * (1024 * 1024))\n"
                    "def test_square():\n"
                    "    print('y' * (1024 * 1024))\n"
                    "    assert square(5) == 25"
                ),
            },
            limits=limits,
        )
        self.assertTrue(result["is_correct"])
    def test_io_test_detail_strings_share_one_output_budget(self):
        limits = {**LIMITS, "max_output_bytes": 32}
        result = invoke(
            "print('x' * 32)",
            {"mode": "io_test", "tests": [
                {"expected_output": "x" * 32},
                {"expected_output": "x" * 32},
            ]},
            limits=limits,
        )
        retained = sum(
            len(detail.get(key, "").encode())
            for detail in result["tests"]
            for key in ("actual", "expected", "error")
        )
        self.assertLessEqual(retained, 32)
        self.assertTrue(result["truncated"])

    def test_unit_test_errors_share_one_output_budget(self):
        limits = {**LIMITS, "max_output_bytes": 32}
        test_code = "\n".join(
            f"def test_{index}():\n    raise AssertionError('x' * 1000)"
            for index in range(32)
        )
        result = invoke("pass", {"mode": "unit_test", "test_code": test_code}, limits=limits)
        retained = sum(
            len(detail.get(key, "").encode())
            for detail in result["tests"]
            for key in ("name", "error")
        )
        self.assertLessEqual(retained, 32)
        self.assertTrue(result["truncated"])


if __name__ == "__main__":
    unittest.main()
