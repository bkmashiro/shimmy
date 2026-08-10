from __future__ import annotations

import ast
import builtins
import contextlib
import io
import json
import traceback
from typing import Any

DEFAULT_LIMITS = {
    "max_output_bytes": 64 * 1024,
    "max_code_bytes": 64 * 1024,
    "max_tests": 32,
    "max_input_bytes": 64 * 1024,
}

_BLOCKED_MODULES = {
    "builtins",
    "ctypes",
    "http",
    "importlib",
    "js",
    "micropip",
    "multiprocessing",
    "os",
    "pathlib",
    "pickle",
    "pyodide",
    "requests",
    "shutil",
    "socket",
    "subprocess",
    "sys",
    "threading",
    "urllib",
}
_BLOCKED_CALLS = {"compile", "eval", "exec", "open", "__import__"}


class ValidationError(ValueError):
    pass


class _SafetyVisitor(ast.NodeVisitor):
    def __init__(self) -> None:
        self.violations: list[str] = []

    def visit_Import(self, node: ast.Import) -> None:
        for alias in node.names:
            root = alias.name.split(".", 1)[0]
            if root in _BLOCKED_MODULES:
                self.violations.append(f"import of '{root}' is not allowed")
        self.generic_visit(node)

    def visit_ImportFrom(self, node: ast.ImportFrom) -> None:
        if node.module:
            root = node.module.split(".", 1)[0]
            if root in _BLOCKED_MODULES:
                self.violations.append(f"import of '{root}' is not allowed")
        self.generic_visit(node)

    def visit_Call(self, node: ast.Call) -> None:
        if isinstance(node.func, ast.Name) and node.func.id in _BLOCKED_CALLS:
            self.violations.append(f"use of '{node.func.id}()' is not allowed")
        self.generic_visit(node)

    def visit_Attribute(self, node: ast.Attribute) -> None:
        if node.attr.startswith("__") and node.attr.endswith("__"):
            self.violations.append(f"dunder attribute '{node.attr}' is not allowed")
        self.generic_visit(node)


def validate_source(source: str) -> list[str]:
    try:
        tree = ast.parse(source)
    except SyntaxError as exc:
        line = exc.lineno or 0
        return [f"SyntaxError: {exc.msg} (line {line})"]
    visitor = _SafetyVisitor()
    visitor.visit(tree)
    return visitor.violations


def _bounded_text(value: Any, name: str, limit: int) -> str:
    text = "" if value is None else str(value)
    if len(text.encode("utf-8")) > limit:
        raise ValidationError(f"{name} exceeds {limit} bytes")
    return text


def _trim_output(value: str, limit: int) -> tuple[str, bool]:
    encoded = value.encode("utf-8")
    if len(encoded) <= limit:
        return value, False
    suffix = "\n[output truncated]"
    budget = max(0, limit - len(suffix.encode("utf-8")))
    trimmed = encoded[:budget].decode("utf-8", errors="ignore") + suffix
    return trimmed, True


def _execute(source: str, stdin: str, inject: dict[str, Any], output_limit: int) -> dict[str, Any]:
    violations = validate_source(source)
    if violations:
        return {"ok": False, "kind": "validation", "error": "\n".join(violations), "stdout": "", "stderr": ""}

    input_lines = iter(stdin.splitlines())

    def safe_input(prompt: str = "") -> str:
        if prompt:
            print(prompt, end="")
        try:
            return next(input_lines)
        except StopIteration as exc:
            raise EOFError("input exhausted") from exc

    namespace: dict[str, Any] = {"__name__": "__student__", **inject}
    stdout = io.StringIO()
    stderr = io.StringIO()
    original_input = builtins.input
    try:
        builtins.input = safe_input
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            exec(compile(source, "<student>", "exec"), namespace, namespace)
        out, out_truncated = _trim_output(stdout.getvalue(), output_limit)
        err, err_truncated = _trim_output(stderr.getvalue(), output_limit)
        return {
            "ok": True,
            "namespace": namespace,
            "stdout": out,
            "stderr": err,
            "truncated": out_truncated or err_truncated,
        }
    except BaseException:
        out, out_truncated = _trim_output(stdout.getvalue(), output_limit)
        error, err_truncated = _trim_output(traceback.format_exc(), output_limit)
        return {
            "ok": False,
            "kind": "runtime",
            "error": error,
            "stdout": out,
            "stderr": "",
            "truncated": out_truncated or err_truncated,
        }
    finally:
        builtins.input = original_input


def _demo(code: str, limits: dict[str, int]) -> dict[str, Any]:
    run = _execute(code, "", {}, limits["max_output_bytes"])
    if not run["ok"]:
        return {"is_correct": False, "feedback": run["error"], "stdout": run["stdout"], "kind": run["kind"]}
    return {
        "is_correct": False,
        "feedback": "Program completed.",
        "stdout": run["stdout"],
        "stderr": run["stderr"],
        "truncated": run["truncated"],
    }


def _io_test(code: str, tests: list[Any], limits: dict[str, int]) -> dict[str, Any]:
    if len(tests) > limits["max_tests"]:
        raise ValidationError(f"tests exceeds {limits['max_tests']} entries")
    details = []
    passed = 0
    for index, raw_test in enumerate(tests, 1):
        if not isinstance(raw_test, dict):
            raise ValidationError(f"test {index} must be an object")
        stdin = _bounded_text(raw_test.get("input", ""), f"test {index} input", limits["max_input_bytes"])
        expected = _bounded_text(raw_test.get("expected_output", ""), f"test {index} expected_output", limits["max_output_bytes"])
        inject = raw_test.get("inject", {})
        if not isinstance(inject, dict):
            raise ValidationError(f"test {index} inject must be an object")
        run = _execute(code, stdin, inject, limits["max_output_bytes"])
        actual = run["stdout"].rstrip()
        correct = bool(run["ok"] and actual == expected.rstrip())
        passed += int(correct)
        hidden = bool(raw_test.get("hidden", False))
        detail: dict[str, Any] = {"index": index, "passed": correct, "hidden": hidden}
        if not hidden:
            detail.update({"actual": actual, "expected": expected.rstrip()})
        if not run["ok"]:
            detail["error"] = run["error"] if not hidden else "hidden test failed"
        details.append(detail)
    total = len(tests)
    return {
        "is_correct": total > 0 and passed == total,
        "feedback": f"{passed}/{total} tests passed.",
        "passed": passed,
        "total": total,
        "tests": details,
    }


def _unit_test(code: str, test_code: str, limits: dict[str, int]) -> dict[str, Any]:
    student = _execute(code, "", {}, limits["max_output_bytes"])
    if not student["ok"]:
        return {"is_correct": False, "feedback": student["error"], "kind": student["kind"]}
    violations = validate_source(test_code)
    if violations:
        return {"is_correct": False, "feedback": "\n".join(violations), "kind": "validation"}

    namespace = student["namespace"]
    test_stdout = io.StringIO()
    test_stderr = io.StringIO()
    try:
        with contextlib.redirect_stdout(test_stdout), contextlib.redirect_stderr(test_stderr):
            exec(compile(test_code, "<tests>", "exec"), namespace, namespace)
    except BaseException:
        error, _ = _trim_output(traceback.format_exc(), limits["max_output_bytes"])
        return {"is_correct": False, "feedback": error, "kind": "test_setup"}

    tests = sorted((name, value) for name, value in namespace.items() if name.startswith("test_") and callable(value))
    if len(tests) > limits["max_tests"]:
        raise ValidationError(f"unit tests exceeds {limits['max_tests']} entries")
    details = []
    for name, test in tests:
        try:
            with contextlib.redirect_stdout(test_stdout), contextlib.redirect_stderr(test_stderr):
                test()
            details.append({"name": name, "passed": True})
        except BaseException:
            error, _ = _trim_output(traceback.format_exc(), limits["max_output_bytes"])
            details.append({"name": name, "passed": False, "error": error})
    passed = sum(int(item["passed"]) for item in details)
    return {
        "is_correct": bool(details) and passed == len(details),
        "feedback": f"{passed}/{len(details)} unit tests passed.",
        "passed": passed,
        "total": len(details),
        "tests": details,
    }


def _dispatch(method: str, payload: dict[str, Any], limits: dict[str, int]) -> dict[str, Any]:
    params = payload.get("params") or {}
    try:
        code = _bounded_text(payload.get("response", ""), "response", limits["max_code_bytes"])
        if method == "preview":
            violations = validate_source(code)
            result = {
                "is_correct": None,
                "preview": "Valid Python syntax." if not violations else "\n".join(violations),
                "valid": not violations,
            }
        else:
            mode = params.get("mode", "demo")
            if mode == "demo":
                result = _demo(code, limits)
            elif mode == "io_test":
                result = _io_test(code, params.get("tests", []), limits)
            elif mode == "unit_test":
                test_code = _bounded_text(params.get("test_code", payload.get("answer", "")), "test_code", limits["max_code_bytes"])
                result = _unit_test(code, test_code, limits)
            else:
                raise ValidationError("mode must be demo, io_test, or unit_test")
    except ValidationError as exc:
        result = {"is_correct": False, "feedback": str(exc), "kind": "validation"}
    return result


def evaluation_function(response: Any, answer: Any, params: dict[str, Any]) -> dict[str, Any]:
    return _dispatch(
        "eval",
        {"response": response, "answer": answer, "params": dict(params or {})},
        DEFAULT_LIMITS,
    )


def preview_function(response: Any, params: dict[str, Any]) -> dict[str, Any]:
    return _dispatch(
        "preview",
        {"response": response, "params": dict(params or {})},
        DEFAULT_LIMITS,
    )


def invoke(request_json: str, limits_json: str) -> str:
    """Host-CPython test adapter; the Reactor calls the functions above directly."""
    request = json.loads(request_json)
    limits = json.loads(limits_json)
    result = _dispatch(
        request.get("method", "eval"),
        request.get("payload") or {},
        limits,
    )
    return json.dumps(result, ensure_ascii=False, separators=(",", ":"))
