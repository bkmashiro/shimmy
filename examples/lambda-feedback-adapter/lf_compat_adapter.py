"""Backend-independent compatibility helpers for Lambda Feedback evaluators.

This adapter is intentionally small and test-only for now: it lets Shimmy's
WASM/Python runtime experiments exercise real Lambda Feedback evaluator shapes
without requiring the production lf_toolkit package or a specific execution
backend. Runtime integrations can call the same helpers from CPython, Pyodide,
or a future Python reactor wrapper.
"""

from __future__ import annotations

import contextlib
import dataclasses
import importlib
import inspect
import io
import os
import sys
import tempfile
from pathlib import Path
from typing import Any, Callable, Iterator


class EntrypointError(ValueError):
    """Raised when an evaluator entrypoint cannot be parsed or loaded."""


@dataclasses.dataclass(frozen=True)
class EvaluatorCallResult:
    """Result from a hygienic evaluator call."""

    result: dict[str, Any]
    stdout: str
    stderr: str
    workdir: str


@contextlib.contextmanager
def evaluator_context(
    root: str | Path,
    *,
    env: dict[str, str] | None = None,
) -> Iterator[Path]:
    """Run evaluator code from an isolated temporary cwd and restore process state.

    This is best-effort lifecycle hygiene for native Python execution, not a
    security sandbox. It restores cwd, ``sys.path``, and environment variables
    after the call while cleaning the temporary request workspace.
    """

    previous_cwd = Path.cwd()
    previous_sys_path = list(sys.path)
    previous_env = dict(os.environ)
    with tempfile.TemporaryDirectory(prefix="lf-eval-") as workdir:
        try:
            if env:
                os.environ.update(env)
            os.chdir(workdir)
            yield Path(workdir)
        finally:
            os.chdir(previous_cwd)
            sys.path[:] = previous_sys_path
            os.environ.clear()
            os.environ.update(previous_env)


def run_entrypoint(
    root: str | Path,
    entrypoint: str,
    *,
    method: str,
    response: Any,
    answer: Any = None,
    params: dict[str, Any] | None = None,
    env: dict[str, str] | None = None,
) -> EvaluatorCallResult:
    """Load and call an evaluator entrypoint inside ``evaluator_context``.

    Captures evaluator stdout/stderr and removes modules imported from the
    evaluator root afterwards so repeated package-shaped calls do not share
    accidental import state.
    """

    stdout = io.StringIO()
    stderr = io.StringIO()
    root_path = str(Path(root).resolve())
    module_name = entrypoint.split(":", 1)[0] if ":" in entrypoint else entrypoint
    with evaluator_context(root, env=env) as workdir:
        workdir_text = str(workdir)
        try:
            with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                fn = load_entrypoint(root, entrypoint)
                result = call_function(
                    fn,
                    method=method,
                    response=response,
                    answer=answer,
                    params=params,
                )
        finally:
            _evict_package_modules(module_name, root_path)
    return EvaluatorCallResult(
        result=result,
        stdout=stdout.getvalue(),
        stderr=stderr.getvalue(),
        workdir=workdir_text,
    )


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
    if root_path in sys.path:
        sys.path.remove(root_path)
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
    if method == "preview" and _preview_prefers_two_args(fn):
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


def _preview_prefers_two_args(fn: Callable[..., Any]) -> bool:
    try:
        sig = inspect.signature(fn)
    except (TypeError, ValueError):
        return False
    positional = [
        p
        for p in sig.parameters.values()
        if p.kind in (inspect.Parameter.POSITIONAL_ONLY, inspect.Parameter.POSITIONAL_OR_KEYWORD)
    ]
    has_varargs = any(p.kind == inspect.Parameter.VAR_POSITIONAL for p in sig.parameters.values())
    if has_varargs or len(positional) < 2:
        return False
    if len(positional) == 2:
        return True
    # Some preview helpers are written as preview(response, params=None). Avoid
    # mistaking preview(response, answer=None, params=None) for that shape.
    return positional[1].name in {"params", "parameters", "preview_params"}


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
