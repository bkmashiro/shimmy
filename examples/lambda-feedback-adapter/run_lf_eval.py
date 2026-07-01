#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

THIS_DIR = Path(__file__).resolve().parent
if str(THIS_DIR) not in sys.path:
    sys.path.insert(0, str(THIS_DIR))

from lf_compat_adapter import call_function, load_entrypoint


def main() -> int:
    parser = argparse.ArgumentParser(description="Run a Lambda Feedback evaluator fixture locally")
    parser.add_argument("--root", required=True, help="Evaluator package root")
    parser.add_argument("--entrypoint", required=True, help="module:function entrypoint")
    parser.add_argument("--method", choices=["eval", "preview"], default="eval")
    parser.add_argument("--response", default="")
    parser.add_argument("--answer", default=None)
    parser.add_argument("--params-json", default="{}")
    args = parser.parse_args()

    params = json.loads(args.params_json)
    if not isinstance(params, dict):
        raise SystemExit("--params-json must decode to a JSON object")

    fn = load_entrypoint(args.root, args.entrypoint)
    result = call_function(
        fn,
        method=args.method,
        response=args.response,
        answer=args.answer,
        params=params,
    )
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
