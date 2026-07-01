#!/usr/bin/env python3
"""Cross-runtime end-to-end benchmark for Shimmy evaluator paths.

This benchmark intentionally compares a small matrix instead of a single WASM
happy path:

* python-file-env: Lambda Feedback Python fixture through Shimmy's file worker
  with package root/entrypoints supplied by environment variables.
* python-file-request: the same file worker with package root/entrypoint supplied
  per request, mirroring dynamic evaluator package selection.
* python-pyodide: the historical Pyodide/Node compatibility runner, skipped
  when node_modules are not installed.
* python-reactor: the historical reactor-mode CPython/WASI runtime, skipped
  when the large reactor artifact is not present.
* generic-wasm-go: the generic WASM ABI using the stateful Go/WASI demo module.

The default CI profile stays small. The deep profile adds medium payloads and an
explicit uffd placeholder row so artifacts document that dirty-page/userfaultfd
restore is not wired yet rather than silently pretending it ran.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import socket
import platform
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
BIN = ROOT / "bin" / "shimmy-e2e-bench"
ADAPTER_DIR = ROOT / "examples" / "lambda-feedback-adapter"
WORKER = ADAPTER_DIR / "lf_file_worker.py"
BOILERPLATE_ROOT = ROOT / "examples" / "lambda-feedback-fixtures" / "boilerplate-python"
RELATIVE_ROOT = ROOT / "examples" / "lambda-feedback-fixtures" / "relative-preview"
DEMO_WASM_DIR = ROOT / "examples" / "demo-stateful"
WASM = DEMO_WASM_DIR / "eval.wasm"
PYODIDE_DIR = ROOT / "examples" / "eval-pyodide"
PYODIDE_RUNNER = PYODIDE_DIR / "runner.js"
PYODIDE_NODE_MODULE = PYODIDE_DIR / "node_modules" / "pyodide"
REACTOR_SCRIPT = ROOT / "examples" / "python-reactor-lf" / "evaluator.py"
DEFAULT_REACTOR_WASM = ROOT / "internal" / "execution" / "wasm" / "testdata" / "python-reactor.wasm"
LOG_DIR = ROOT / ".benchmark-e2e-logs"


@dataclass(frozen=True)
class RuntimeSpec:
    id: str
    language: str
    interface: str
    description: str


@dataclass(frozen=True)
class PayloadSpec:
    id: str
    command: str
    size: str
    body: dict[str, Any]
    description: str
    expected_correct: bool | None = None


@dataclass(frozen=True)
class BenchmarkCase:
    runtime: RuntimeSpec
    payload: PayloadSpec
    snapshot: str
    profile: str
    skip_reason: str | None = None

    @property
    def name(self) -> str:
        suffix = "" if self.snapshot in ("none", "full") else f"-{self.snapshot}"
        return f"{self.runtime.id}-{self.payload.id}{suffix}"


RUNTIMES: dict[str, RuntimeSpec] = {
    "python-file-env": RuntimeSpec(
        id="python-file-env",
        language="python",
        interface="file",
        description="Python LF fixture through file worker; root/entrypoints from env",
    ),
    "python-file-request": RuntimeSpec(
        id="python-file-request",
        language="python",
        interface="file",
        description="Python LF fixture through file worker; root/entrypoint from request params",
    ),
    "python-pyodide": RuntimeSpec(
        id="python-pyodide",
        language="python",
        interface="pyodide",
        description="Python LF fixture through the Pyodide/Node compatibility runner",
    ),
    "python-reactor": RuntimeSpec(
        id="python-reactor",
        language="python-wasi",
        interface="wasm",
        description="Python LF fixture through the reactor-mode CPython/WASI runtime",
    ),
    "generic-wasm-go": RuntimeSpec(
        id="generic-wasm-go",
        language="go-wasm",
        interface="wasm",
        description="Generic WASI module through Shimmy's WASM ABI",
    ),
}


def payloads_for_runtime(runtime_id: str) -> list[PayloadSpec]:
    heavy_text = "x" * (32 * 1024)
    medium_text = "medium-line\n" * 256
    heavy_cases = [{"answer": f"candidate-{i:02d}", "feedback": f"case {i:02d}"} for i in range(60)]

    if runtime_id == "python-file-request":
        return [
            PayloadSpec(
                id="eval-light",
                command="eval",
                size="light",
                body={
                    "response": "a,b",
                    "answer": "b, a",
                    "params": {
                        "root": str(RELATIVE_ROOT),
                        "entrypoint": "evaluation_function.evaluation:evaluation_function",
                        "mode": "set",
                    },
                },
                description="per-request Python package root/entrypoint, small set compare",
                expected_correct=True,
            ),
            PayloadSpec(
                id="eval-medium",
                command="eval",
                size="medium",
                body={
                    "response": medium_text.strip(),
                    "answer": medium_text.strip(),
                    "params": {
                        "root": str(BOILERPLATE_ROOT),
                        "entrypoint": "evaluation_function.main:evaluation_function",
                    },
                },
                description="per-request Python package with medium response/answer",
                expected_correct=True,
            ),
            PayloadSpec(
                id="eval-heavy",
                command="eval",
                size="heavy",
                body={
                    "response": heavy_text,
                    "answer": heavy_text,
                    "params": {
                        "root": str(BOILERPLATE_ROOT),
                        "entrypoint": "evaluation_function.main:evaluation_function",
                        "cases": heavy_cases,
                    },
                },
                description="per-request Python package with large body and cases payload",
                expected_correct=True,
            ),
            PayloadSpec(
                id="preview-light",
                command="preview",
                size="light",
                body={
                    "response": " Foo ",
                    "params": {
                        "root": str(RELATIVE_ROOT),
                        "entrypoint": "evaluation_function.evaluation:preview_function",
                        "mode": "set",
                    },
                },
                description="per-request two-argument preview entrypoint",
            ),
        ]

    common = [
        PayloadSpec(
            id="eval-light",
            command="eval",
            size="light",
            body={"response": "42", "answer": "42", "params": {}},
            description="small correct eval request",
            expected_correct=True,
        ),
        PayloadSpec(
            id="eval-medium",
            command="eval",
            size="medium",
            body={"response": medium_text.strip(), "answer": medium_text.strip(), "params": {"nested": {"depth": 2}}},
            description="medium multiline response/answer with nested params",
            expected_correct=True,
        ),
        PayloadSpec(
            id="eval-heavy",
            command="eval",
            size="heavy",
            body={"response": heavy_text, "answer": heavy_text, "params": {"cases": heavy_cases}},
            description="large response/answer strings plus cases payload",
            expected_correct=True,
        ),
        PayloadSpec(
            id="preview-light",
            command="preview",
            size="light",
            body={"response": "draft", "params": {"expected": "42"}},
            description="small preview request",
        ),
    ]
    return common


def all_cases() -> list[BenchmarkCase]:
    cases: list[BenchmarkCase] = []
    for runtime in RUNTIMES.values():
        snapshots = ["none"]
        if runtime.id in {"generic-wasm-go", "python-reactor"}:
            snapshots = ["full", "off"]
        for payload in payloads_for_runtime(runtime.id):
            for snapshot in snapshots:
                # Keep the no-isolation control intentionally small.
                if snapshot == "off" and not (payload.command == "eval" and payload.size == "light"):
                    continue
                profile = "deep" if payload.size == "medium" else "ci"
                cases.append(
                    BenchmarkCase(
                        runtime=runtime,
                        payload=payload,
                        snapshot=snapshot,
                        profile=profile,
                        skip_reason=runtime_skip_reason(runtime.id),
                    )
                )
        if runtime.id == "generic-wasm-go":
            cases.append(
                BenchmarkCase(
                    runtime=runtime,
                    payload=payloads_for_runtime(runtime.id)[0],
                    snapshot="uffd",
                    profile="deep",
                    skip_reason="FUNCTION_WASM_SNAPSHOT_STRATEGY=uffd is not implemented/wired yet",
                )
            )
    return cases


def select_cases(profile: str, runtime_filters: set[str] | None, case_filters: set[str] | None) -> list[BenchmarkCase]:
    if profile not in {"ci", "deep"}:
        raise SystemExit(f"unknown profile {profile!r}; expected ci or deep")
    known_runtimes = set(RUNTIMES)
    if runtime_filters:
        unknown = sorted(runtime_filters - known_runtimes)
        if unknown:
            raise SystemExit(f"unknown runtime(s): {', '.join(unknown)}; known: {', '.join(sorted(known_runtimes))}")

    candidates = []
    for case in all_cases():
        if profile == "ci" and case.profile != "ci":
            continue
        if runtime_filters and case.runtime.id not in runtime_filters:
            continue
        if case_filters and case.name not in case_filters and case.payload.id not in case_filters:
            continue
        candidates.append(case)

    if case_filters:
        known_cases = {case.name for case in all_cases()} | {case.payload.id for case in all_cases()}
        unknown = sorted(case_filters - known_cases)
        if unknown:
            raise SystemExit(f"unknown case/payload(s): {', '.join(unknown)}")
    return candidates


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
    run(["go", "build", "-buildmode=c-shared", "-o", str(WASM), "."], cwd=DEMO_WASM_DIR, env=env)


def python_bin() -> str:
    return os.environ.get("PYTHON_BIN") or os.environ.get("PYTHON") or shutil.which("python3") or sys.executable


def reactor_wasm_path() -> Path:
    return Path(os.environ.get("FUNCTION_WASM_MODULE") or os.environ.get("PYTHON_REACTOR_WASM") or DEFAULT_REACTOR_WASM)


def runtime_skip_reason(runtime_id: str) -> str | None:
    if runtime_id == "python-pyodide":
        if shutil.which("node") is None:
            return "node is not installed"
        if not PYODIDE_RUNNER.exists():
            return f"Pyodide runner missing: {PYODIDE_RUNNER}"
        if not PYODIDE_NODE_MODULE.exists():
            return "Pyodide node_modules are not installed; run npm install in examples/eval-pyodide"
    if runtime_id == "python-reactor":
        if platform.system().lower() != "linux":
            return "reactor-python is Linux-only in this branch"
        wasm_path = reactor_wasm_path()
        if not wasm_path.exists():
            return f"python-reactor.wasm missing: set PYTHON_REACTOR_WASM or place artifact at {DEFAULT_REACTOR_WASM}"
        if not REACTOR_SCRIPT.exists():
            return f"reactor benchmark script missing: {REACTOR_SCRIPT}"
    return None


class Server:
    def __init__(self, runtime: RuntimeSpec, snapshot: str) -> None:
        self.runtime = runtime
        self.snapshot = snapshot
        self.port = choose_port()
        self.base_url = f"http://127.0.0.1:{self.port}"
        self.proc: subprocess.Popen[str] | None = None
        self._log_file = None

    def start(self) -> None:
        LOG_DIR.mkdir(exist_ok=True)
        log_path = LOG_DIR / f"{self.runtime.id}-{self.snapshot}.log"
        log_path.unlink(missing_ok=True)
        self._log_file = log_path.open("w", encoding="utf-8")
        env = os.environ.copy()
        env.update({"LOG_LEVEL": "error", "FUNCTION_MAX_PROCS": "1", "FUNCTION_TIMEOUT": "15s"})

        if self.runtime.interface == "wasm":
            module_path = WASM
            extra_env = {"FUNCTION_WASM_SNAPSHOT_STRATEGY": self.snapshot}
            if self.runtime.id == "python-reactor":
                module_path = reactor_wasm_path()
                extra_env.update(
                    {
                        "FUNCTION_WASM_PROFILE": "python-reactor",
                        "FUNCTION_WASM_MODULE": str(module_path),
                        "FUNCTION_WASM_PYTHON_SCRIPT": str(REACTOR_SCRIPT),
                        "FUNCTION_WASM_COMPILE_CACHE": str(ROOT / ".benchmark-e2e-wazero-cache"),
                        "FUNCTION_WASM_MAX_MEMORY_PAGES": os.environ.get("FUNCTION_WASM_MAX_MEMORY_PAGES", "4096"),
                    }
                )
            else:
                extra_env["FUNCTION_WASM_MODULE"] = str(module_path)
            env.update({"FUNCTION_INTERFACE": "wasm", **extra_env})
            cmd = [
                str(BIN),
                "--log-level",
                "error",
                "--interface",
                "wasm",
                "--command",
                str(module_path),
                "--max-workers",
                "1",
                "serve",
                "--host",
                "127.0.0.1",
                "--port",
                str(self.port),
            ]
        elif self.runtime.interface == "pyodide":
            env.update(
                {
                    "FUNCTION_INTERFACE": "pyodide",
                    "FUNCTION_PYODIDE_RUNNER": str(PYODIDE_RUNNER),
                    "FUNCTION_PYODIDE_ROOT": str(BOILERPLATE_ROOT),
                    "FUNCTION_PYODIDE_EVAL_ENTRYPOINT": "evaluation_function.main:evaluation_function",
                    "FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT": "evaluation_function.main:preview_function",
                    "FUNCTION_PYODIDE_ADAPTER": str(ADAPTER_DIR / "lf_compat_adapter.py"),
                }
            )
            cmd = [
                str(BIN),
                "--log-level",
                "error",
                "--interface",
                "pyodide",
                "--max-workers",
                "1",
                "serve",
                "--host",
                "127.0.0.1",
                "--port",
                str(self.port),
            ]
        else:
            cmd = [
                str(BIN),
                "--log-level",
                "error",
                "--interface",
                "file",
                "--max-workers",
                "1",
                "--command",
                python_bin(),
                "--arg",
                str(WORKER),
            ]
            if self.runtime.id == "python-file-env":
                cmd.extend(
                    [
                        "--env",
                        f"FUNCTION_LF_ROOT={BOILERPLATE_ROOT}",
                        "--env",
                        "FUNCTION_LF_ENTRYPOINT=evaluation_function.main:evaluation_function",
                        "--env",
                        "FUNCTION_LF_PREVIEW_ENTRYPOINT=evaluation_function.main:preview_function",
                    ]
                )
            cmd.extend(["serve", "--host", "127.0.0.1", "--port", str(self.port)])

        self.proc = subprocess.Popen(
            cmd,
            cwd=str(ROOT),
            env=env,
            stdout=self._log_file,
            stderr=subprocess.STDOUT,
            text=True,
        )
        self.wait_ready(log_path)

    def wait_ready(self, log_path: Path) -> None:
        assert self.proc is not None
        deadline = time.time() + 20
        last_error: Exception | None = None
        while time.time() < deadline:
            if self.proc.poll() is not None:
                raise RuntimeError(f"server exited early with code {self.proc.returncode}; log: {log_path.read_text(errors='replace')}")
            try:
                with urllib.request.urlopen(f"{self.base_url}/health", timeout=0.5) as resp:
                    if resp.status == 200:
                        return
            except Exception as exc:  # noqa: BLE001
                last_error = exc
            time.sleep(0.1)
        raise RuntimeError(f"server did not become ready: {last_error}; log: {log_path.read_text(errors='replace')}")

    def stop(self) -> None:
        if self.proc is not None and self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait(timeout=3)
        if self._log_file is not None:
            self._log_file.close()


def post_json(base_url: str, case: BenchmarkCase) -> dict[str, Any]:
    body = json.dumps(case.payload.body, separators=(",", ":")).encode("utf-8")
    req = urllib.request.Request(
        f"{base_url}/",
        data=body,
        method="POST",
        headers={"Content-Type": "application/json", "Command": case.payload.command},
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read()
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {exc.code} for {case.name}: {detail}") from exc
    return json.loads(raw)


def validate_response(case: BenchmarkCase, response: dict[str, Any]) -> dict[str, Any]:
    if "error" in response:
        raise AssertionError(f"{case.name} returned error: {response['error']}")
    result = response.get("result")
    if not isinstance(result, dict):
        raise AssertionError(f"{case.name} response missing object result: {response}")

    meta: dict[str, Any] = {}
    if case.payload.command == "eval":
        if case.payload.expected_correct is not None and result.get("is_correct") is not case.payload.expected_correct:
            raise AssertionError(f"{case.name} expected is_correct={case.payload.expected_correct}: {result}")
        if case.runtime.id == "generic-wasm-go":
            count = result.get("guest_invocation_count")
            isolation_ok = result.get("snapshot_isolation_ok")
            meta["guest_invocation_count"] = count
            meta["isolation_ok"] = isolation_ok
            if case.snapshot == "full" and isolation_ok is not True:
                raise AssertionError(f"{case.name} did not preserve WASM snapshot isolation: {result}")
            # "off" is a benchmark comparison mode, not a correctness mode. Some
            # guests may keep mutable state outside restored linear memory or may
            # be rotated by the dispatcher, so record the observed counter instead
            # of requiring leakage as the pass condition.
    elif case.payload.command == "preview":
        preview = result.get("preview")
        if not isinstance(preview, dict):
            raise AssertionError(f"{case.name} response missing preview object: {response}")
    return meta


def percentile(values: list[float], pct: float) -> float:
    ordered = sorted(values)
    index = (len(ordered) - 1) * pct
    lo = int(index)
    hi = min(lo + 1, len(ordered) - 1)
    frac = index - lo
    return ordered[lo] * (1 - frac) + ordered[hi] * frac


def bench_case(base_url: str, case: BenchmarkCase, iterations: int, warmup: int) -> dict[str, Any]:
    if case.skip_reason:
        return {
            "name": case.name,
            "runtime": case.runtime.id,
            "language": case.runtime.language,
            "interface": case.runtime.interface,
            "snapshot": case.snapshot,
            "command": case.payload.command,
            "size": case.payload.size,
            "description": case.payload.description,
            "status": "skipped",
            "skip_reason": case.skip_reason,
        }

    last_meta: dict[str, Any] = {}
    for _ in range(warmup):
        last_meta = validate_response(case, post_json(base_url, case))

    timings_ms: list[float] = []
    response_bytes = 0
    for _ in range(iterations):
        start = time.perf_counter_ns()
        response = post_json(base_url, case)
        elapsed_ms = (time.perf_counter_ns() - start) / 1_000_000
        last_meta = validate_response(case, response)
        response_bytes = len(json.dumps(response, separators=(",", ":")).encode("utf-8"))
        timings_ms.append(elapsed_ms)

    request_bytes = len(json.dumps(case.payload.body, separators=(",", ":")).encode("utf-8"))
    return {
        "name": case.name,
        "runtime": case.runtime.id,
        "language": case.runtime.language,
        "interface": case.runtime.interface,
        "snapshot": case.snapshot,
        "command": case.payload.command,
        "size": case.payload.size,
        "description": case.payload.description,
        "status": "passed",
        "iterations": iterations,
        "warmup": warmup,
        "request_bytes": request_bytes,
        "response_bytes": response_bytes,
        "min_ms": min(timings_ms),
        "mean_ms": statistics.fmean(timings_ms),
        "p50_ms": percentile(timings_ms, 0.50),
        "p95_ms": percentile(timings_ms, 0.95),
        "max_ms": max(timings_ms),
        **last_meta,
    }


def print_table(results: list[dict[str, Any]]) -> None:
    print("\nShimmy cross-runtime e2e benchmark")
    print("case                                           rt                 snap    size     mean ms   p95 ms   status")
    print("-" * 112)
    for row in results:
        if row["status"] == "skipped":
            print(f"{row['name']:<46} {row['runtime']:<18} {row['snapshot']:<7} {row['size']:<8} {'-':>8} {'-':>8} skipped")
            continue
        print(
            f"{row['name']:<46} {row['runtime']:<18} {row['snapshot']:<7} {row['size']:<8} "
            f"{row['mean_ms']:>8.2f} {row['p95_ms']:>8.2f} passed"
        )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", choices=["ci", "deep"], default="ci")
    parser.add_argument("--iterations", type=int, default=5, help="measured requests per case")
    parser.add_argument("--warmup", type=int, default=1, help="warmup requests per case")
    parser.add_argument("--runtime", action="append", help="runtime id to include; may be repeated")
    parser.add_argument("--case", action="append", help="case name or payload id to include; may be repeated")
    parser.add_argument("--json-output", type=Path, help="optional path for machine-readable results")
    parser.add_argument("--skip-build", action="store_true", help="reuse existing binary and eval.wasm")
    args = parser.parse_args(argv)

    if args.iterations <= 0:
        parser.error("--iterations must be positive")
    if args.warmup < 0:
        parser.error("--warmup must be non-negative")

    selected = select_cases(
        profile=args.profile,
        runtime_filters=set(args.runtime) if args.runtime else None,
        case_filters=set(args.case) if args.case else None,
    )
    if not selected:
        parser.error("no benchmark cases selected")

    if not args.skip_build:
        build_artifacts()

    results: list[dict[str, Any]] = []
    server_keys = sorted({(case.runtime.id, case.snapshot) for case in selected if not case.skip_reason})
    for runtime_id, snapshot in server_keys:
        runtime = RUNTIMES[runtime_id]
        server = Server(runtime, snapshot)
        server.start()
        try:
            for case in selected:
                if case.runtime.id == runtime_id and case.snapshot == snapshot:
                    results.append(bench_case(server.base_url, case, args.iterations, args.warmup))
        finally:
            server.stop()

    for case in selected:
        if case.skip_reason:
            results.append(bench_case("", case, args.iterations, args.warmup))

    print_table(results)
    output = {
        "profile": args.profile,
        "iterations": args.iterations,
        "warmup": args.warmup,
        "results": results,
    }
    if args.json_output:
        args.json_output.parent.mkdir(parents=True, exist_ok=True)
        args.json_output.write_text(json.dumps(output, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        print(f"\nWrote JSON results to {args.json_output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
