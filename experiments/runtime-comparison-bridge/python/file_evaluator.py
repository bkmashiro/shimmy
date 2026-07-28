#!/usr/bin/env python3
"""Fresh-process file adapter for the shared Python bridge fixture."""

import importlib.util
import json
import pathlib
import sys


def load_evaluator():
    path = pathlib.Path(__file__).with_name("eval.py")
    spec = importlib.util.spec_from_file_location("shimmy_bridge_eval", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load evaluator: {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main(argv):
    if len(argv) < 2:
        raise ValueError("usage: file_evaluator.py [args...] request.json response.json")
    request_path = pathlib.Path(argv[-2])
    response_path = pathlib.Path(argv[-1])
    request = json.loads(request_path.read_text(encoding="utf-8"))
    if request.get("command") != "eval":
        raise ValueError(f"unsupported command: {request.get('command')!r}")
    payload = request.get("params") or {}
    evaluator = load_evaluator()
    result = evaluator.evaluation_function(
        payload.get("response"), payload.get("answer"), payload.get("params")
    )
    response_path.write_text(
        json.dumps({"command": "eval", "result": result}, separators=(",", ":")),
        encoding="utf-8",
    )


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except Exception as exc:
        print(exc, file=sys.stderr)
        raise SystemExit(1)
