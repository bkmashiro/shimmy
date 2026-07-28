#!/usr/bin/env python3
"""Persistent LSP-framed JSON-RPC adapter for the shared Python fixture."""

import importlib.util
import json
import pathlib
import sys

MAX_FRAME_BYTES = 4 << 20


def load_evaluator():
    path = pathlib.Path(__file__).with_name("eval.py")
    spec = importlib.util.spec_from_file_location("shimmy_bridge_eval", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load evaluator: {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def read_frame(stream):
    length = None
    while True:
        line = stream.readline()
        if line == b"":
            return None
        if line in (b"\n", b"\r\n"):
            break
        name, separator, value = line.partition(b":")
        if separator and name.strip().lower() == b"content-length":
            length = int(value.strip())
    if length is None or length < 0 or length > MAX_FRAME_BYTES:
        raise ValueError("invalid Content-Length")
    payload = stream.read(length)
    if len(payload) != length:
        raise EOFError("short RPC frame")
    return json.loads(payload)


def write_frame(stream, payload):
    encoded = json.dumps(payload, separators=(",", ":")).encode("utf-8")
    if len(encoded) > MAX_FRAME_BYTES:
        raise ValueError("response frame exceeds maximum size")
    stream.write(f"Content-Length: {len(encoded)}\r\n\r\n".encode("ascii"))
    stream.write(encoded)
    stream.flush()


def serve(input_stream, output_stream):
    evaluator = load_evaluator()
    while True:
        request = read_frame(input_stream)
        if request is None:
            return
        request_id = request.get("id")
        method = request.get("method")
        if method == "healthcheck":
            result = {"status": "ok"}
        elif method == "eval":
            params = request.get("params") or []
            payload = params[0] if isinstance(params, list) and params else {}
            result = evaluator.evaluation_function(
                payload.get("response"), payload.get("answer"), payload.get("params")
            )
        else:
            write_frame(
                output_stream,
                {"jsonrpc": "2.0", "id": request_id, "error": {"code": -32601, "message": "method not found"}},
            )
            continue
        write_frame(output_stream, {"jsonrpc": "2.0", "id": request_id, "result": result})


if __name__ == "__main__":
    serve(sys.stdin.buffer, sys.stdout.buffer)
