from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from lf_compat_adapter import call_function, load_entrypoint, normalize_result


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Run a Lambda Feedback evaluator entrypoint locally")
    parser.add_argument("--root", required=True, help="Path to evaluator package root")
    parser.add_argument("--eval-entrypoint", required=True, help="Entrypoint for eval method")
    parser.add_argument("--preview-entrypoint", help="Entrypoint for preview method")
    parser.add_argument(
        "--method",
        choices=("eval", "preview"),
        required=True,
        help="Method to invoke",
    )
    parser.add_argument("--input", required=True, help="JSON input payload")
    return parser


def _prepare_sys_path(root: Path) -> None:
    adapter_dir = Path(__file__).resolve().parent
    for path in (adapter_dir, root):
        path_str = str(path.resolve())
        if path_str not in sys.path:
            sys.path.insert(0, path_str)


def _parse_input(raw_input: str) -> tuple[object, object, object]:
    try:
        parsed = json.loads(raw_input)
    except json.JSONDecodeError as exc:
        raise ValueError("Input must be valid JSON") from exc

    if not isinstance(parsed, dict):
        raise ValueError("Input must be a JSON object")

    try:
        response = parsed["response"]
        answer = parsed["answer"]
        params = parsed["params"]
    except KeyError as exc:
        raise ValueError("Input JSON must contain 'response', 'answer', and 'params'") from exc

    return response, answer, params


def main(argv: list[str] | None = None) -> int:
    parser = _build_parser()
    args = parser.parse_args(argv)

    try:
        root = Path(args.root)
        _prepare_sys_path(root)
        response, answer, params = _parse_input(args.input)

        if args.method == "preview" and args.preview_entrypoint:
            spec = args.preview_entrypoint
        else:
            spec = args.eval_entrypoint

        fn = load_entrypoint(spec)
        output = call_function(
            fn,
            method=args.method,
            response=response,
            answer=answer,
            params=params,
        )
        print(json.dumps(normalize_result(output)))
        return 0

    except Exception as exc:
        print(f"Error: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
