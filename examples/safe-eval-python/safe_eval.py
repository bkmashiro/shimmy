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


class _BoundedTextWriter(io.TextIOBase):
    """Text sink that never retains more than limit UTF-8 bytes."""

    def __init__(self, limit: int) -> None:
        super().__init__()
        self._limit = max(0, limit)
        self._buffer = bytearray()
        self.truncated = False

    @property
    def retained_bytes(self) -> int:
        return len(self._buffer)

    def writable(self) -> bool:
        return True

    def write(self, value: str) -> int:
        if not isinstance(value, str):
            raise TypeError("write() argument must be str")
        if not value:
            return 0
        remaining = self._limit - len(self._buffer)
        if remaining <= 0:
            self.truncated = True
            return len(value)

        offset = 0
        while offset < len(value) and remaining > 0:
            chunk = value[offset : offset + min(4096, remaining)]
            encoded = chunk.encode("utf-8")
            available = remaining
            self._buffer.extend(encoded[:available])
            offset += len(chunk)
            remaining = self._limit - len(self._buffer)
            if len(encoded) > available:
                self.truncated = True
                break
        if offset < len(value):
            self.truncated = True
        return len(value)

    def getvalue(self) -> str:
        if not self.truncated:
            return bytes(self._buffer).decode("utf-8", errors="ignore")
        return _render_truncated(bytes(self._buffer), self._limit)


def _render_truncated(encoded: bytes, limit: int) -> str:
    suffix = b"\n[output truncated]"
    if limit <= len(suffix):
        return suffix[:limit].decode("utf-8", errors="ignore")
    budget = limit - len(suffix)
    return encoded[:budget].decode("utf-8", errors="ignore") + suffix.decode()


def _bounded_traceback(limit: int) -> tuple[str, bool]:
    writer = _BoundedTextWriter(limit)
    traceback.print_exc(file=writer)
    return writer.getvalue(), writer.truncated


class _TextBudget:
    """Shared UTF-8 budget for dynamic strings in one structured result."""

    def __init__(self, limit: int) -> None:
        self.remaining = max(0, limit)
        self.truncated = False

    def take(self, value: str) -> str:
        writer = _BoundedTextWriter(self.remaining)
        writer.write(value)
        rendered = writer.getvalue()
        self.remaining -= len(rendered.encode("utf-8"))
        self.truncated = self.truncated or writer.truncated
        return rendered


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
    stdout = _BoundedTextWriter(output_limit)
    stderr = _BoundedTextWriter(output_limit)
    original_input = builtins.input
    try:
        builtins.input = safe_input
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            exec(compile(source, "<student>", "exec"), namespace, namespace)
        return {
            "ok": True,
            "namespace": namespace,
            "stdout": stdout.getvalue(),
            "stderr": stderr.getvalue(),
            "truncated": stdout.truncated or stderr.truncated,
        }
    except BaseException:
        error, traceback_truncated = _bounded_traceback(output_limit)
        return {
            "ok": False,
            "kind": "runtime",
            "error": error,
            "stdout": stdout.getvalue(),
            "stderr": "",
            "truncated": stdout.truncated or stderr.truncated or traceback_truncated,
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
    result_budget = _TextBudget(limits["max_output_bytes"])
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
            detail.update({"actual": result_budget.take(actual), "expected": result_budget.take(expected.rstrip())})
        if not run["ok"]:
            detail["error"] = result_budget.take(run["error"] if not hidden else "hidden test failed")
        details.append(detail)
    total = len(tests)
    return {
        "is_correct": total > 0 and passed == total,
        "feedback": f"{passed}/{total} tests passed.",
        "passed": passed,
        "total": total,
        "tests": details,
        "truncated": result_budget.truncated,
    }


def _unit_test(code: str, test_code: str, limits: dict[str, int]) -> dict[str, Any]:
    student = _execute(code, "", {}, limits["max_output_bytes"])
    if not student["ok"]:
        return {"is_correct": False, "feedback": student["error"], "kind": student["kind"]}
    violations = validate_source(test_code)
    if violations:
        return {"is_correct": False, "feedback": "\n".join(violations), "kind": "validation"}

    namespace = student["namespace"]
    test_stdout = _BoundedTextWriter(limits["max_output_bytes"])
    test_stderr = _BoundedTextWriter(limits["max_output_bytes"])
    try:
        with contextlib.redirect_stdout(test_stdout), contextlib.redirect_stderr(test_stderr):
            exec(compile(test_code, "<tests>", "exec"), namespace, namespace)
    except BaseException:
        error, _ = _bounded_traceback(limits["max_output_bytes"])
        return {"is_correct": False, "feedback": error, "kind": "test_setup"}

    tests = sorted((name, value) for name, value in namespace.items() if name.startswith("test_") and callable(value))
    if len(tests) > limits["max_tests"]:
        raise ValidationError(f"unit tests exceeds {limits['max_tests']} entries")
    details = []
    result_budget = _TextBudget(limits["max_output_bytes"])
    for name, test in tests:
        try:
            with contextlib.redirect_stdout(test_stdout), contextlib.redirect_stderr(test_stderr):
                test()
            details.append({"name": result_budget.take(name), "passed": True})
        except BaseException:
            error, _ = _bounded_traceback(limits["max_output_bytes"])
            details.append({
                "name": result_budget.take(name),
                "passed": False,
                "error": result_budget.take(error),
            })
    passed = sum(int(item["passed"]) for item in details)
    return {
        "is_correct": bool(details) and passed == len(details),
        "feedback": f"{passed}/{len(details)} unit tests passed.",
        "passed": passed,
        "total": len(details),
        "tests": details,
        "truncated": result_budget.truncated or test_stdout.truncated or test_stderr.truncated,
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
