#!/usr/bin/env python3
"""Validate the result of float128_canary.py from the final consumer path."""

import argparse
import json
import pathlib
import sys
from typing import Any, Dict


REQUIRED_INTEGER_FIELDS = (
    "longdouble_itemsize",
    "longdouble_nmant",
    "double_nmant",
)
REQUIRED_TRUE_FIELDS = (
    "preserves_extra_precision",
    "narrows_to_double_one",
    "epsilon_is_narrower",
)


def validate_result(value: Any) -> Dict[str, Any]:
    if not isinstance(value, dict):
        raise ValueError("canary response must be an object")
    if "status" in value and value["status"] != "ok":
        raise ValueError("canary response status is not ok")
    if "result" in value:
        value = value["result"]
    if not isinstance(value, dict):
        raise ValueError("canary result must be an object")

    for field in REQUIRED_INTEGER_FIELDS:
        if type(value.get(field)) is not int:
            raise ValueError("%s must be an integer" % field)
    for field in REQUIRED_TRUE_FIELDS:
        if value.get(field) is not True:
            if field == "preserves_extra_precision":
                raise ValueError("binary128 canary lost extra precision")
            raise ValueError("%s must be true" % field)

    if value["longdouble_itemsize"] != 16:
        raise ValueError("long double storage must be 16 bytes")
    if value["longdouble_nmant"] < 112:
        raise ValueError("long double does not expose a binary128 mantissa")
    if value["double_nmant"] > 53:
        raise ValueError("double mantissa metadata is unexpected")
    if value["longdouble_nmant"] <= value["double_nmant"]:
        raise ValueError("long double mantissa is not wider than double")
    return value


def load_json(path: str) -> Any:
    if path == "-":
        return json.load(sys.stdin)
    return json.loads(pathlib.Path(path).read_text())


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("result", help="JSON response path, or - for stdin")
    args = parser.parse_args()
    validated = validate_result(load_json(args.result))
    print(
        json.dumps(
            {
                "binary128_validated": True,
                "longdouble_itemsize": validated["longdouble_itemsize"],
                "longdouble_nmant": validated["longdouble_nmant"],
                "double_nmant": validated["double_nmant"],
            },
            sort_keys=True,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
