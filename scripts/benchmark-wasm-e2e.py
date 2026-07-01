#!/usr/bin/env python3
"""End-to-end benchmark for Shimmy's WASM execution path.

The benchmark builds the demo WASM evaluator, starts a real Shimmy HTTP server
with FUNCTION_INTERFACE=wasm, and measures POST requests through the public
runtime endpoint. It intentionally covers several payload shapes instead of only
a tiny happy-path request.
"""

from __future__ import annotations

import argparse
import json
import os
import socket
import statistics
import subprocess
import sys
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[1]
BIN = ROOT / "bin" / "shimmy-wasm-e2e-bench"
DEMO_DIR = ROOT / "examples" / "demo-stateful"
WASM = DEMO_DIR / "eval.wasm"
LOG = ROOT / ".benchmark-wasm-e2e-server.log"


@dataclass(frozen=True)
class PayloadCase:
    name: str
    command: str
    body: dict[str, Any]
    description: str
    expected_correct: bool | None = None


def payload_cases() -> list[PayloadCase]:
    large_answer = "x" * (32 * 1024)
    medium_preview = "preview-line\n" * 64
    cases = [
        {"answer": f"candidate-{i:02d}", "feedback": f"case {i:02d} feedback"}
        for i in range(29)
    ] + [{"answer": "target-case", "feedback": "matched final case"}]

    return [
        PayloadCase(
            name="eval-short-correct",
            command="eval",
            body={"response": "42", "answer": "42", "params": {}},
            description="small correct eval request",
            expected_correct=True,
        ),
        PayloadCase(
            name="eval-short-incorrect",
            command="eval",
            body={"response": "41", "answer": "42", "params": {}},
            description="small incorrect eval request",
            expected_correct=False,
        ),
        PayloadCase(
            name="eval-large-response",
            command="eval",
            body={"response": large_answer, "answer": large_answer, "params": {}},
            description="large response/answer strings through HTTP + WASM memory",
            expected_correct=True,
        ),
        PayloadCase(
            name="eval-cases-heavy",
            command="eval",
            body={"response": "target-case", "answer": "canonical", "params": {"cases": cases}},
            description="incorrect eval plus host-side case matching that re-enters the evaluator",
            expected_correct=False,
        ),
        PayloadCase(
            name="preview-medium",
            command="preview",
            body={"response": medium_preview, "params": {"mode": "markdown"}},
            description="preview command with medium multiline content",
            expected_correct=None,
        ),
    ]


def run(cmd: list[str], *, cwd: Path = ROOT, env: dict[str, str] | None = None) -> None:
    print("$", " ".join(cmd), file=sys.stderr)
    subprocess.run(cmd, cwd=str(cwd), env=env, check=True)


def choose_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return int(s.getsockname()[1])


def build_artifacts() -> None:
    BIN.parent.mkdir(parents=True, exist_ok=True)
    run(["go", "build", "-trimpath", "-buildvcs=false", "-o", str(BIN), "."])
    env = os.environ.copy()
    env.update({"GOOS": "wasip1", "GOARCH": "wasm"})
    run(["go", "build", "-buildmode=c-shared", "-o", str(WASM), "."], cwd=DEMO_DIR, env=env)


def start_server(port: int) -> subprocess.Popen[str]:
    LOG.unlink(missing_ok=True)
    env = os.environ.copy()
    env.update(
        {
            "LOG_LEVEL": "error",
            "FUNCTION_INTERFACE": "wasm",
            "FUNCTION_WASM_MODULE": str(WASM),
            "FUNCTION_MAX_PROCS": "1",
            "FUNCTION_TIMEOUT": "10s",
        }
    )
    log_file = LOG.open("w", encoding="utf-8")
    proc = subprocess.Popen(
        [str(BIN), "serve", "--host", "127.0.0.1", "--port", str(port)],
        cwd=str(ROOT),
        env=env,
        stdout=log_file,
        stderr=subprocess.STDOUT,
        text=True,
    )
    # Keep the file object alive via the process object for the server lifetime.
    proc._shimmy_log_file = log_file  # type: ignore[attr-defined]
    return proc


def stop_server(proc: subprocess.Popen[str]) -> None:
    if proc.poll() is None:
        proc.terminate()
        try:
            proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=3)
    log_file = getattr(proc, "_shimmy_log_file", None)
    if log_file is not None:
        log_file.close()


def wait_ready(base_url: str, proc: subprocess.Popen[str]) -> None:
    deadline = time.time() + 15
    last_error: Exception | None = None
    while time.time() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(f"server exited early with code {proc.returncode}; log: {LOG.read_text(encoding='utf-8', errors='replace')}")
        try:
            with urllib.request.urlopen(f"{base_url}/health", timeout=0.5) as resp:
                if resp.status == 200:
                    return
        except Exception as exc:  # noqa: BLE001 - readiness retry loop
            last_error = exc
        time.sleep(0.1)
    raise RuntimeError(f"server did not become ready: {last_error}; log: {LOG.read_text(encoding='utf-8', errors='replace')}")


def post_json(base_url: str, case: PayloadCase) -> dict[str, Any]:
    body = json.dumps(case.body, separators=(",", ":")).encode("utf-8")
    req = urllib.request.Request(
        f"{base_url}/",
        data=body,
        method="POST",
        headers={"Content-Type": "application/json", "Command": case.command},
    )
    try:
        with urllib.request.urlopen(req, timeout=20) as resp:
            raw = resp.read()
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {exc.code} for {case.name}: {detail}") from exc
    return json.loads(raw)


def validate_response(case: PayloadCase, response: dict[str, Any]) -> None:
    if "error" in response:
        raise AssertionError(f"{case.name} returned error: {response['error']}")
    result = response.get("result")
    if not isinstance(result, dict):
        raise AssertionError(f"{case.name} response missing object result: {response}")
    if case.command == "eval":
        if result.get("guest_invocation_count") != 1 or result.get("snapshot_isolation_ok") is not True:
            raise AssertionError(f"{case.name} did not preserve WASM snapshot isolation: {result}")
        if case.expected_correct is not None and result.get("is_correct") is not case.expected_correct:
            raise AssertionError(f"{case.name} expected is_correct={case.expected_correct}: {result}")
    elif case.command == "preview":
        preview = result.get("preview")
        if not isinstance(preview, dict) or "content" not in preview:
            raise AssertionError(f"{case.name} response missing preview content: {response}")


def percentile(values: list[float], pct: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    index = (len(ordered) - 1) * pct
    lo = int(index)
    hi = min(lo + 1, len(ordered) - 1)
    frac = index - lo
    return ordered[lo] * (1 - frac) + ordered[hi] * frac


def bench_case(base_url: str, case: PayloadCase, iterations: int, warmup: int) -> dict[str, Any]:
    for _ in range(warmup):
        validate_response(case, post_json(base_url, case))

    timings_ms: list[float] = []
    response_bytes = len(json.dumps(post_json(base_url, case), separators=(",", ":")).encode("utf-8"))
    for _ in range(iterations):
        start = time.perf_counter_ns()
        response = post_json(base_url, case)
        elapsed_ms = (time.perf_counter_ns() - start) / 1_000_000
        validate_response(case, response)
        timings_ms.append(elapsed_ms)

    request_bytes = len(json.dumps(case.body, separators=(",", ":")).encode("utf-8"))
    return {
        "name": case.name,
        "command": case.command,
        "description": case.description,
        "iterations": iterations,
        "request_bytes": request_bytes,
        "response_bytes": response_bytes,
        "min_ms": min(timings_ms),
        "mean_ms": statistics.fmean(timings_ms),
        "p50_ms": percentile(timings_ms, 0.50),
        "p95_ms": percentile(timings_ms, 0.95),
        "max_ms": max(timings_ms),
    }


def print_table(results: list[dict[str, Any]]) -> None:
    print("\nWASM end-to-end benchmark (HTTP -> Shimmy -> WASM -> HTTP)")
    print("payload                 cmd      req B   mean ms   p50 ms   p95 ms   max ms")
    print("-" * 78)
    for row in results:
        print(
            f"{row['name']:<23} {row['command']:<8} {row['request_bytes']:>6} "
            f"{row['mean_ms']:>9.2f} {row['p50_ms']:>8.2f} {row['p95_ms']:>8.2f} {row['max_ms']:>8.2f}"
        )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--iterations", type=int, default=25, help="measured requests per payload")
    parser.add_argument("--warmup", type=int, default=3, help="warmup requests per payload")
    parser.add_argument("--payload", action="append", help="payload name to run; may be repeated")
    parser.add_argument("--json-output", type=Path, help="optional path for machine-readable results")
    parser.add_argument("--skip-build", action="store_true", help="reuse existing binary and eval.wasm")
    args = parser.parse_args(argv)

    if args.iterations <= 0:
        parser.error("--iterations must be positive")
    if args.warmup < 0:
        parser.error("--warmup must be non-negative")

    cases = payload_cases()
    if args.payload:
        wanted = set(args.payload)
        known = {case.name for case in cases}
        unknown = sorted(wanted - known)
        if unknown:
            parser.error(f"unknown payload(s): {', '.join(unknown)}; known: {', '.join(sorted(known))}")
        cases = [case for case in cases if case.name in wanted]

    if not args.skip_build:
        build_artifacts()

    port = choose_port()
    base_url = f"http://127.0.0.1:{port}"
    proc = start_server(port)
    try:
        wait_ready(base_url, proc)
        results = [bench_case(base_url, case, args.iterations, args.warmup) for case in cases]
    finally:
        stop_server(proc)

    print_table(results)
    output = {
        "base_url": base_url,
        "iterations": args.iterations,
        "warmup": args.warmup,
        "wasm": str(WASM),
        "results": results,
    }
    if args.json_output:
        args.json_output.parent.mkdir(parents=True, exist_ok=True)
        args.json_output.write_text(json.dumps(output, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        print(f"\nWrote JSON results to {args.json_output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
