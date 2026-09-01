"""Small compatibility layer for Lambda Feedback evaluator packages."""

from __future__ import annotations

from dataclasses import asdict, is_dataclass
import importlib
import inspect


def load_entrypoint(spec: str):
    if not isinstance(spec, str) or ":" not in spec:
        raise ValueError("Entrypoint must use module:function format")
    module_name, symbol_name = spec.rsplit(":", 1)
    module = importlib.import_module(module_name)
    return getattr(module, symbol_name)


def _preview_accepts_two_args(fn) -> bool:
    try:
        signature = inspect.signature(fn)
    except (TypeError, ValueError):
        return False
    positional = [
        parameter
        for parameter in signature.parameters.values()
        if parameter.kind
        in (inspect.Parameter.POSITIONAL_ONLY, inspect.Parameter.POSITIONAL_OR_KEYWORD)
    ]
    has_varargs = any(
        parameter.kind == inspect.Parameter.VAR_POSITIONAL
        for parameter in signature.parameters.values()
    )
    return not has_varargs and len(positional) == 2


def call_function(fn, method: str, response, answer=None, params=None):
    params = {} if params is None else params
    if method == "preview" and _preview_accepts_two_args(fn):
        return fn(response, params)
    return fn(response, answer, params)


def normalize_result(value):
    normalized = _normalize_jsonish(value)
    return normalized if isinstance(normalized, dict) else {"value": normalized}


def _normalize_jsonish(value):
    if value is None:
        return value
    if isinstance(value, dict):
        return {key: _normalize_jsonish(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_normalize_jsonish(item) for item in value]

    # NumPy scalar subclasses such as np.float64 may also satisfy Python's
    # float check, so normalize item()-capable values before that check.
    item = getattr(value, "item", None)
    if callable(item):
        try:
            converted = item()
        except (TypeError, ValueError):
            converted = value
        if converted is not value:
            return _normalize_jsonish(converted)

    if isinstance(value, (str, int, float, bool)):
        return value

    for method_name in ("to_dict", "model_dump", "dict"):
        method = getattr(value, method_name, None)
        if callable(method):
            converted = method()
            if isinstance(converted, dict):
                return _normalize_jsonish(converted)

    if is_dataclass(value) and not isinstance(value, type):
        return _normalize_jsonish(asdict(value))

    fields = getattr(value, "__dict__", None)
    if isinstance(fields, dict):
        return {
            key: _normalize_jsonish(item)
            for key, item in fields.items()
            if not key.startswith("_")
        }

    return value
