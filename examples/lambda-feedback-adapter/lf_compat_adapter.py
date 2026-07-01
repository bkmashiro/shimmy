"""Backend-independent compatibility helpers for Lambda Feedback evaluators.

This adapter is intentionally small and test-only for now: it lets Shimmy's
WASM/Python runtime experiments exercise real Lambda Feedback evaluator shapes
without requiring the production lf_toolkit package or a specific execution
backend. Runtime integrations can call the same helpers from CPython, Pyodide,
or a future Python reactor wrapper.
"""

from __future__ import annotations

import dataclasses
import importlib
import inspect
import sys
from pathlib import Path
from typing import Any, Callable


class EntrypointError(ValueError):
    """Raised when an evaluator entrypoint cannot be parsed or loaded."""


def load_entrypoint(root: str | Path, entrypoint: str) -> Callable[..., Any]:
    """Load ``module:function`` from an evaluator package root.

    ``root`` is prepended to ``sys.path`` for the import duration and left there
    so relative imports and later lazy imports inside the evaluator keep working.
    Existing modules for the same entrypoint are evicted to avoid stale state
    when tests invoke multiple fixtures with the same package name.
    """

    if ":" not in entrypoint:
        raise EntrypointError(f"entrypoint must be 'module:function', got {entrypoint!r}")
    module_name, func_name = entrypoint.split(":", 1)
    if not module_name or not func_name:
        raise EntrypointError(f"entrypoint must be 'module:function', got {entrypoint!r}")

    root_path = str(Path(root).resolve())
    if root_path not in sys.path:
        sys.path.insert(0, root_path)

    _evict_package_modules(module_name, root_path)
    module = importlib.import_module(module_name)
    try:
        fn = getattr(module, func_name)
    except AttributeError as exc:
        raise EntrypointError(f"entrypoint function {func_name!r} not found in {module_name!r}") from exc
    if not callable(fn):
        raise EntrypointError(f"entrypoint {entrypoint!r} is not callable")
    return fn


def call_function(
    fn: Callable[..., Any],
    *,
    method: str,
    response: Any,
    answer: Any = None,
    params: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Invoke an LF evaluator function and normalize its result.

    Eval functions normally accept ``(response, answer, params)``. Some preview
    functions in existing repositories accept ``(response, params)`` instead;
    this helper detects that shape and preserves compatibility.
    """

    params = params or {}
    if method == "preview" and _accepts_two_positional_args(fn):
        raw = fn(response, params)
    else:
        raw = fn(response, answer, params)
    normalized = normalize_result(raw)
    if not isinstance(normalized, dict):
        raise TypeError(f"normalized evaluator result must be an object, got {type(normalized).__name__}")
    return normalized


def normalize_result(value: Any) -> Any:
    """Convert common LF/toolkit/Python result shapes to JSON-compatible data."""

    if value is None or isinstance(value, (str, int, float, bool)):
        return value
    if isinstance(value, (list, tuple, set)):
        return [normalize_result(v) for v in value]
    if isinstance(value, dict):
        return {str(k): normalize_result(v) for k, v in value.items() if v is not None}
    if dataclasses.is_dataclass(value):
        return normalize_result(dataclasses.asdict(value))
    if hasattr(value, "model_dump") and callable(value.model_dump):
        return normalize_result(value.model_dump())
    if hasattr(value, "dict") and callable(value.dict):
        return normalize_result(value.dict())
    if hasattr(value, "item") and callable(value.item):
        try:
            return normalize_result(value.item())
        except Exception:
            pass

    public = {
        name: attr
        for name in dir(value)
        if not name.startswith("_")
        for attr in [getattr(value, name)]
        if not callable(attr)
    }
    if public:
        return normalize_result(public)
    return value


def _accepts_two_positional_args(fn: Callable[..., Any]) -> bool:
    try:
        sig = inspect.signature(fn)
    except (TypeError, ValueError):
        return False
    positional = [
        p
        for p in sig.parameters.values()
        if p.kind in (inspect.Parameter.POSITIONAL_ONLY, inspect.Parameter.POSITIONAL_OR_KEYWORD)
        and p.default is inspect.Parameter.empty
    ]
    has_varargs = any(p.kind == inspect.Parameter.VAR_POSITIONAL for p in sig.parameters.values())
    return not has_varargs and len(positional) == 2


def _evict_package_modules(module_name: str, root_path: str) -> None:
    package = module_name.split(".", 1)[0]
    for name, module in list(sys.modules.items()):
        if name != package and not name.startswith(package + "."):
            continue
        file = getattr(module, "__file__", None)
        # Evaluator fixtures frequently reuse the same top-level package name
        # (``evaluation_function``). Evict any prior copy of that package before
        # importing from the requested root so relative imports don't resolve to
        # a stale fixture from an earlier invocation.
        if file and (package == "evaluation_function" or str(Path(file).resolve()).startswith(root_path)):
            del sys.modules[name]
