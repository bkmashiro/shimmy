"""Shimmy file-IO worker for Lambda Feedback-compatible Python evaluators.

Shimmy's file adapter sends JSON as::

    {"command": "eval"|"preview", "params": {...request body...}}

This worker loads a Python ``module:function`` entrypoint from an evaluator root,
invokes it through ``lf_compat_adapter``, and writes Shimmy's schema-compatible
response envelope back to the response file.
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path
from typing import Any

from lf_compat_adapter import call_function, load_entrypoint


def handle_message(message: dict[str, Any]) -> dict[str, Any]:
    command = str(message.get("command") or "eval")
    params = message.get("params")
    if not isinstance(params, dict):
        return error_response("request params must be an object")

    try:
        root, entrypoint, evaluator_params = resolve_config(command, params)
        fn = load_entrypoint(root, entrypoint)
        result = call_function(
            fn,
            method=command,
            response=params.get("response"),
            answer=params.get("answer"),
            params=evaluator_params,
        )
        return {"command": command, "result": result}
    except Exception as exc:  # Keep worker failures schema-compatible for Shimmy.
        return error_response(str(exc), exc.__class__.__name__)


def resolve_config(command: str, request_params: dict[str, Any]) -> tuple[str, str, dict[str, Any]]:
    raw_params = request_params.get("params")
    evaluator_params = dict(raw_params) if isinstance(raw_params, dict) else {}

    root = pop_first(evaluator_params, ("_lf_root", "root")) or os.getenv("LF_EVAL_ROOT")
    entrypoint = (
        pop_first(evaluator_params, ("_lf_entrypoint", "entrypoint"))
        or os.getenv(f"LF_{command.upper()}_ENTRYPOINT")
        or os.getenv("LF_ENTRYPOINT")
    )

    missing = []
    if not entrypoint:
        missing.append("entrypoint")
    if not root:
        missing.append("root")
    if missing:
        raise ValueError(f"missing evaluator params: {', '.join(sorted(missing))}")

    return str(root), str(entrypoint), evaluator_params


def pop_first(data: dict[str, Any], keys: tuple[str, ...]) -> Any:
    for key in keys:
        if key in data:
            return data.pop(key)
    return None


def error_response(message: str, error_type: str | None = None) -> dict[str, Any]:
    error: dict[str, Any] = {"message": message}
    if error_type:
        error["error_thrown"] = error_type
    return {"error": error}


def main(argv: list[str] | None = None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    request_file = os.getenv("EVAL_FILE_NAME_REQUEST") or (argv[0] if len(argv) >= 1 else None)
    response_file = os.getenv("EVAL_FILE_NAME_RESPONSE") or (argv[1] if len(argv) >= 2 else None)
    if not request_file or not response_file:
        print("EVAL_FILE_NAME_REQUEST and EVAL_FILE_NAME_RESPONSE are required", file=sys.stderr)
        return 2

    with Path(request_file).open("r", encoding="utf-8") as f:
        message = json.load(f)
    response = handle_message(message)
    with Path(response_file).open("w", encoding="utf-8") as f:
        json.dump(response, f, separators=(",", ":"), sort_keys=True)
        f.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
