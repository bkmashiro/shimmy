#!/usr/bin/env python3
"""Run the bounded, declared-edge runtime comparison bridge in GitHub Actions."""

from __future__ import annotations

import argparse
import dataclasses
import datetime as dt
import hashlib
import json
import math
import os
import pathlib
import platform
import subprocess
import sys
import time
import urllib.error
import urllib.request
from typing import Any, Dict, List, Mapping, Sequence


MASK = (1 << 64) - 1
MULTIPLIER = 6364136223846793005
INCREMENT = 1442695040888963407
MAX_ITERATIONS = 5_000_000


@dataclasses.dataclass(frozen=True)
class LaneSpec:
    lane: str
    service: str
    port: int
    family: str
    lifecycle_class: str
    lifecycle: str
    initialization_placement: str
    state_guarantee: str
    fixture_source: str
    reports_checksum: bool = True
    supports_cpu_workload: bool = True


LANES: Dict[str, LaneSpec] = {
    row.lane: row
    for row in (
        LaneSpec(
            "system-native-fresh",
            "bridge-system-native",
            18581,
            "system-language",
            "clean",
            "fresh native process per public HTTP request",
            "process start is request-visible",
            "fresh process; guest invocation count must be one",
            "shared Go contract plus native file entrypoint",
        ),
        LaneSpec(
            "system-generic-restore",
            "bridge-system-generic",
            18582,
            "system-language",
            "clean",
            "prepared generic WASM instance restored after every request",
            "module preparation is lazy; linear-memory restore is request-visible",
            "verified linear-memory reset; guest invocation count must be one",
            "verified demo-stateful equality fixture compiled to WASI",
            False,
            False,
        ),
        LaneSpec(
            "system-dbi-fresh",
            "bridge-system-dbi",
            18583,
            "system-language",
            "clean",
            "fresh native process per request under DynamoRIO client",
            "DBI process and client initialization are request-visible",
            "fresh process; guest invocation count must be one",
            "same native bridge binary as system-native-fresh",
        ),
        LaneSpec(
            "python-native-fresh",
            "bridge-python-native-fresh",
            18584,
            "python",
            "clean",
            "fresh native CPython process per public HTTP request",
            "CPython process and script import are request-visible",
            "fresh process; guest invocation count must be one",
            "shared pure-Python eval.py through a thin file adapter",
        ),
        LaneSpec(
            "python-native-persistent",
            "bridge-python-native-persistent",
            18585,
            "python",
            "persistent",
            "persistent native CPython LSP-framed RPC worker",
            "CPython process and script import are lazy first-request costs",
            "mutable Python globals persist and counters must increase monotonically",
            "shared pure-Python eval.py through a thin RPC adapter",
        ),
        LaneSpec(
            "python-agent-cow",
            "bridge-python-agent-cow",
            18586,
            "python",
            "clean",
            "Agent Python snapshot lifecycle with linear-memory COW reset",
            "runtime_prepare is pre-snapshot; restore is request-visible",
            "verified linear-memory reset; guest invocation count must be one",
            "the exact same shared pure-Python eval.py",
        ),
        LaneSpec(
            "python-pyodide-persistent",
            "bridge-python-pyodide-persistent",
            18587,
            "python",
            "persistent",
            "persistent Pyodide worker with no optional package preload",
            "Pyodide startup and script import are lazy first-request costs",
            "mutable Python globals persist and counters must increase monotonically",
            "the exact same shared pure-Python eval.py",
        ),
    )
}

WORKLOADS: Dict[str, Dict[str, int]] = {
    "fixed": {"iterations": 0, "seed": 7},
    "cpu-100k": {"iterations": 100_000, "seed": 7},
}

EDGE_DEFINITIONS = (
    {
        "id": "system-native-vs-wasm-semantic",
        "lanes": ["system-native-fresh", "system-generic-restore"],
        "basis": "same fixed equality semantics, public HTTP payload, verified invocation count one, runner and resource limits",
        "claim_boundary": "fixed-cost application E2E only; implementations and mechanisms differ, so this is not an intrinsic runtime score",
    },
    {
        "id": "system-native-vs-dbi",
        "lanes": ["system-native-fresh", "system-dbi-fresh"],
        "basis": "same native binary, file interface, public HTTP payload, clean-state lifecycle, runner and resource limits",
        "claim_boundary": "pairwise DynamoRIO wrapper tax; not a universal DBI score",
    },
    {
        "id": "python-warm",
        "lanes": ["python-native-persistent", "python-agent-cow", "python-pyodide-persistent"],
        "basis": "exact same Python file and public HTTP payload on one runner with equal resource limits",
        "claim_boundary": "warm application E2E only; Agent resets linear memory while native CPython and Pyodide preserve mutable globals",
    },
    {
        "id": "python-clean",
        "lanes": ["python-native-fresh", "python-agent-cow"],
        "basis": "exact same Python file, public HTTP payload, verified invocation count one, runner and resource limits",
        "claim_boundary": "clean-request pair; Pyodide is absent because the current lane has no qualified per-request reset",
    },
)

SOURCE_PATHS = (
    "experiments/runtime-comparison-bridge/contract/contract.go",
    "examples/demo-stateful/main.go",
    "experiments/runtime-comparison-bridge/native-file/main.go",
    "experiments/runtime-comparison-bridge/python/eval.py",
    "experiments/runtime-comparison-bridge/python/file_evaluator.py",
    "experiments/runtime-comparison-bridge/python/rpc_evaluator.py",
)


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")


def summarize_ns(samples: Sequence[int]) -> Dict[str, Any]:
    if not samples:
        raise ValueError("summary requires at least one sample")
    ordered = sorted(samples)
    count = len(ordered)
    middle = count // 2
    median = float(ordered[middle]) if count % 2 else (ordered[middle - 1] + ordered[middle]) / 2
    p95 = ordered[max(0, math.ceil(0.95 * count) - 1)]
    return {
        "sample_count": count,
        "median_ns": median,
        "p95_ns": p95,
        "min_ns": ordered[0],
        "max_ns": ordered[-1],
    }


def expected_checksum(iterations: int, seed: int) -> str:
    if isinstance(iterations, bool) or iterations < 0 or iterations > MAX_ITERATIONS:
        raise ValueError("iterations out of range")
    if isinstance(seed, bool) or seed < 0 or seed > MASK:
        raise ValueError("seed out of range")
    value = seed
    for index in range(iterations):
        value = (value * MULTIPLIER + INCREMENT + index) & MASK
    return f"{value:016x}"


def result_object(response: Mapping[str, Any]) -> Dict[str, Any]:
    result = response.get("result")
    if not isinstance(result, dict):
        raise ValueError(f"response result must be an object: {response}")
    return dict(result)


def validate_response(
    response: Mapping[str, Any], *, expected_checksum: str, require_checksum: bool = True
) -> Dict[str, Any]:
    if response.get("command") != "eval":
        raise ValueError(f"response command must be eval: {response}")
    result = result_object(response)
    if result.get("is_correct") is not True:
        raise ValueError(f"is_correct must be true: {result}")
    if require_checksum and result.get("work_checksum") != expected_checksum:
        raise ValueError(
            f"work checksum mismatch: got {result.get('work_checksum')!r}, want {expected_checksum!r}"
        )
    counter = result.get("guest_invocation_count")
    if isinstance(counter, bool) or not isinstance(counter, int) or counter < 1:
        raise ValueError(f"guest invocation count must be a positive integer: {counter!r}")
    return result


def validate_counter_sequence(lifecycle_class: str, observations: Sequence[Mapping[str, Any]]) -> None:
    counters: List[int] = []
    for row in observations:
        counter = row.get("guest_invocation_count")
        if isinstance(counter, bool) or not isinstance(counter, int) or counter < 1:
            raise ValueError(f"invalid guest invocation counter: {counter!r}")
        counters.append(counter)
    if not counters:
        raise ValueError("counter sequence is empty")
    if lifecycle_class == "clean":
        if any(counter != 1 for counter in counters):
            raise ValueError(f"clean lane must report invocation count one: {counters}")
        return
    if lifecycle_class == "persistent":
        expected = list(range(counters[0], counters[0] + len(counters)))
        if counters != expected:
            raise ValueError(f"persistent counters must be monotonic consecutive values: {counters}")
        return
    raise ValueError(f"unknown lifecycle class: {lifecycle_class}")


def run(
    command: Sequence[str], *, capture: bool = True, check: bool = True
) -> subprocess.CompletedProcess:
    return subprocess.run(
        list(command),
        check=check,
        text=True,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.PIPE if capture else None,
    )


def command_output(completed: subprocess.CompletedProcess) -> str:
    return (completed.stdout or "") + (completed.stderr or "")


def compose_prefix(compose_file: pathlib.Path) -> List[str]:
    return ["docker", "compose", "--profile", "dbi", "-f", str(compose_file)]


def request_json(
    url: str,
    payload: Dict[str, Any],
    timeout: float,
    *,
    command: str = "eval",
) -> Dict[str, Any]:
    request = urllib.request.Request(
        url,
        data=json.dumps(payload, separators=(",", ":")).encode("utf-8"),
        headers={"Content-Type": "application/json", "Command": command},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            body = response.read()
            if response.status != 200:
                raise RuntimeError(f"HTTP {response.status}: {body[:500]!r}")
    except urllib.error.HTTPError as exc:
        body = exc.read()
        raise RuntimeError(f"HTTP {exc.code}: {body[:500]!r}") from exc
    decoded = json.loads(body)
    if not isinstance(decoded, dict):
        raise ValueError("HTTP response must be a JSON object")
    return decoded


def timed_request(url: str, payload: Dict[str, Any], timeout: float) -> tuple[int, Dict[str, Any]]:
    started = time.perf_counter_ns()
    response = request_json(url, payload, timeout)
    return time.perf_counter_ns() - started, response


def wait_for_health(url: str, timeout: float) -> None:
    deadline = time.monotonic() + timeout
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=min(2.0, timeout)) as response:
                if response.status == 200:
                    return
        except Exception as exc:
            last_error = exc
        time.sleep(0.1)
    raise TimeoutError(f"health endpoint not ready after {timeout}s: {last_error}")


def image_metadata(prefix: Sequence[str], service: str) -> Dict[str, Any]:
    image_id = run([*prefix, "images", "-q", service]).stdout.strip()
    if not image_id:
        raise RuntimeError(f"compose returned no image id for {service}")
    inspected = json.loads(run(["docker", "image", "inspect", image_id]).stdout)
    if not isinstance(inspected, list) or len(inspected) != 1:
        raise RuntimeError(f"unexpected docker inspect response for {service}")
    row = inspected[0]
    return {
        "id": row.get("Id", image_id),
        "size_bytes": row.get("Size"),
        "repo_digests": row.get("RepoDigests") or [],
    }


def container_sha256(prefix: Sequence[str], service: str, path: str) -> str:
    output = run([*prefix, "exec", "-T", service, "sha256sum", path]).stdout.strip()
    digest = output.split(None, 1)[0] if output else ""
    if len(digest) != 64 or any(character not in "0123456789abcdef" for character in digest):
        raise ValueError(f"invalid fixture SHA-256 from {service}: {output!r}")
    return digest


def cpu_model() -> str:
    cpuinfo = pathlib.Path("/proc/cpuinfo")
    if cpuinfo.exists():
        for line in cpuinfo.read_text(errors="replace").splitlines():
            if line.lower().startswith("model name") and ":" in line:
                return line.split(":", 1)[1].strip()
    return platform.processor() or "unknown"


def source_manifest(repo_root: pathlib.Path) -> Dict[str, str]:
    result = {}
    for relative in SOURCE_PATHS:
        data = (repo_root / relative).read_bytes()
        result[relative] = hashlib.sha256(data).hexdigest()
    return result


def agent_lifecycle_evidence(base: str, timeout: float) -> Dict[str, Any]:
    response = request_json(base + "/", {}, timeout, command="healthcheck")
    result = result_object(response)
    expected = {
        "lifecycle": "snapshot",
        "snapshot_selected": "cow",
        "reset_mode": "linear-memory-cow",
    }
    for key, value in expected.items():
        if result.get(key) != value:
            raise ValueError(f"Agent Python healthcheck {key} = {result.get(key)!r}, want {value!r}")
    return {key: result[key] for key in expected}


def benchmark_lane(
    spec: LaneSpec,
    prefix: Sequence[str],
    *,
    warmups: int,
    samples: int,
    ready_timeout: float,
    request_timeout: float,
) -> Dict[str, Any]:
    run([*prefix, "rm", "-s", "-f", spec.service], check=False)
    started = time.perf_counter_ns()
    all_results: List[Dict[str, Any]] = []
    try:
        run([*prefix, "up", "-d", "--no-build", spec.service])
        image = image_metadata(prefix, spec.service)
        base = f"http://127.0.0.1:{spec.port}"
        wait_for_health(base + "/health", ready_timeout)
        ready_ns = time.perf_counter_ns() - started

        lifecycle_evidence = None
        if spec.lane == "python-agent-cow":
            lifecycle_evidence = agent_lifecycle_evidence(base, request_timeout)

        if spec.family == "python":
            fixture_path = "/opt/evaluators/eval.py"
        elif spec.lane == "system-generic-restore":
            fixture_path = "/opt/evaluators/runtime-bridge-generic.wasm"
        else:
            fixture_path = "/opt/evaluators/runtime-bridge-native"
        fixture_sha256 = container_sha256(prefix, spec.service, fixture_path)

        workload_reports: Dict[str, Any] = {}
        selected_workloads = {
            profile: workload
            for profile, workload in WORKLOADS.items()
            if spec.supports_cpu_workload or profile == "fixed"
        }
        for profile, workload in selected_workloads.items():
            checksum = expected_checksum(workload["iterations"], workload["seed"])
            payload = {"response": "42", "answer": "42", "params": dict(workload)}
            entry_ns, response = timed_request(base + "/", payload, request_timeout)
            entry_result = validate_response(
                response, expected_checksum=checksum, require_checksum=spec.reports_checksum
            )
            all_results.append(entry_result)

            warmup_ns = []
            for _ in range(warmups):
                elapsed, response = timed_request(base + "/", payload, request_timeout)
                warmup_ns.append(elapsed)
                all_results.append(
                    validate_response(
                        response, expected_checksum=checksum, require_checksum=spec.reports_checksum
                    )
                )

            steady_ns = []
            for _ in range(samples):
                elapsed, response = timed_request(base + "/", payload, request_timeout)
                steady_ns.append(elapsed)
                all_results.append(
                    validate_response(
                        response, expected_checksum=checksum, require_checksum=spec.reports_checksum
                    )
                )

            workload_reports[profile] = {
                "payload": payload,
                "expected_work_checksum": checksum if spec.reports_checksum else None,
                "entry_request_ns": entry_ns,
                "entry_request_semantics": (
                    "first evaluator request after public server readiness"
                    if profile == "fixed"
                    else "first request for this workload after the fixed-cost profile on the same worker"
                ),
                "warmup_samples_ns": warmup_ns,
                "steady_samples_ns": steady_ns,
                "steady": summarize_ns(steady_ns),
            }

        validate_counter_sequence(spec.lifecycle_class, all_results)
        counters = [row["guest_invocation_count"] for row in all_results]
        dbi_client_marker = None
        if spec.lane == "system-dbi-fresh":
            dbi_client_marker = run(
                [*prefix, "exec", "-T", spec.service, "cat", "/tmp/shimmy-dbi-client-init.marker"]
            ).stdout.strip()
            if dbi_client_marker != "initialized":
                raise ValueError(f"DBI client marker mismatch: {dbi_client_marker!r}")

        pyodide_packages = None
        if spec.lane == "python-pyodide-persistent":
            pyodide_packages = run(
                [*prefix, "exec", "-T", spec.service, "printenv", "FUNCTION_PYODIDE_PACKAGES"]
            ).stdout.strip()
            if pyodide_packages:
                raise ValueError(f"Pyodide bridge must not preload optional packages: {pyodide_packages!r}")

        report = {
            "lane": spec.lane,
            "service": spec.service,
            "family": spec.family,
            "lifecycle_class": spec.lifecycle_class,
            "lifecycle": spec.lifecycle,
            "initialization_placement": spec.initialization_placement,
            "state_guarantee": spec.state_guarantee,
            "fixture_source": spec.fixture_source,
            "fixture_file": {"path": fixture_path, "sha256": fixture_sha256},
            "server_ready_ns": ready_ns,
            "server_ready_semantics": "container start until Shimmy public /health succeeds; evaluator initialization may remain lazy",
            "resource_limit": {"cpus": 2.0, "memory": "2 GiB", "max_procs": 1},
            "counter_evidence": {
                "observation_count": len(counters),
                "per_request": counters,
                "first": counters[0],
                "last": counters[-1],
                "all_one": all(counter == 1 for counter in counters),
                "monotonic_consecutive": counters == list(range(counters[0], counters[0] + len(counters))),
            },
            "workloads": workload_reports,
            "image": image,
        }
        if lifecycle_evidence is not None:
            report["lifecycle_evidence"] = lifecycle_evidence
        if dbi_client_marker is not None:
            report["dbi_client_marker"] = dbi_client_marker
        if pyodide_packages is not None:
            report["pyodide_optional_packages"] = []
        return report
    except Exception:
        logs = command_output(run([*prefix, "logs", "--no-color", "--tail=200", spec.service], check=False))
        if logs:
            print(f"--- {spec.lane} logs ---\n{logs}", file=sys.stderr)
        raise
    finally:
        run([*prefix, "rm", "-s", "-f", spec.service], check=False)


def build_report(
    *,
    rows: Sequence[Dict[str, Any]],
    source_commit: str,
    source_modified: bool,
    started_at: str,
    completed_at: str,
    environment: Dict[str, Any],
    source_manifest: Dict[str, str],
    warmups: int,
    samples: int,
) -> Dict[str, Any]:
    by_lane = {row["lane"]: row for row in rows}
    if {"system-native-fresh", "system-dbi-fresh"}.issubset(by_lane):
        native_sha = by_lane["system-native-fresh"]["fixture_file"]["sha256"]
        dbi_sha = by_lane["system-dbi-fresh"]["fixture_file"]["sha256"]
        if native_sha != dbi_sha:
            raise ValueError(f"native/DBI fixture binary mismatch: {native_sha} != {dbi_sha}")

    python_lanes = [row for row in rows if row.get("family") == "python"]
    if python_lanes:
        source_key = "experiments/runtime-comparison-bridge/python/eval.py"
        expected_python_sha = source_manifest.get(source_key)
        if not expected_python_sha:
            raise ValueError(f"source manifest lacks {source_key}")
        mismatches = {
            row["lane"]: row["fixture_file"]["sha256"]
            for row in python_lanes
            if row["fixture_file"]["sha256"] != expected_python_sha
        }
        if mismatches:
            raise ValueError(f"Python fixture SHA-256 mismatch: {mismatches}")

    present = {row["lane"] for row in rows}
    edges = [dict(edge) for edge in EDGE_DEFINITIONS if set(edge["lanes"]).issubset(present)]
    return {
        "schema": "shimmy-runtime-comparison-bridge/v1",
        "status": "PASS",
        "benchmark_kind": "declared-edge-public-http-comparison-graph",
        "started_at": started_at,
        "completed_at": completed_at,
        "source_commit": source_commit,
        "source_modified": source_modified,
        "workflow_run_id": os.getenv("GITHUB_RUN_ID"),
        "environment": environment,
        "source_manifest_sha256": source_manifest,
        "method": {
            "request_concurrency": 1,
            "warmup_count_per_workload": warmups,
            "steady_sample_count_per_workload": samples,
            "workloads": WORKLOADS,
            "timing_boundary": "client perf_counter immediately before HTTP request through full response body and JSON decode",
            "comparison_rule": "only declared edges may be compared; first, steady, clean-state, persistent-state, and background replacement costs remain separate",
        },
        "universal_ranking_allowed": False,
        "comparison_edges": edges,
        "non_edges": [
            "system-language lanes versus Python lanes",
            "DBI versus Python or Pyodide",
            "persistent Pyodide versus clean-request lanes as an isolation-equivalent score",
            "legacy ReactorPythonDispatcher versus current AgentPythonDispatcher",
        ],
        "lanes": list(rows),
    }


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--compose-file",
        default="experiments/runtime-comparison-bridge/compose.yaml",
    )
    parser.add_argument("--lanes", default=",".join(LANES))
    parser.add_argument("--warmups", type=int, default=2)
    parser.add_argument("--samples", type=int, default=10)
    parser.add_argument("--ready-timeout", type=float, default=240.0)
    parser.add_argument("--request-timeout", type=float, default=180.0)
    parser.add_argument("--output", required=True)
    parser.add_argument("--allow-local", action="store_true")
    args = parser.parse_args(argv)
    if args.warmups < 0 or args.warmups > 20:
        parser.error("--warmups must be in [0,20]")
    if args.samples < 1 or args.samples > 100:
        parser.error("--samples must be in [1,100]")
    selected = args.lanes.split(",")
    unknown = [lane for lane in selected if lane not in LANES]
    if unknown:
        parser.error(f"unknown lanes: {unknown}")
    args.selected_lanes = selected
    return args


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    if os.getenv("GITHUB_ACTIONS") != "true" and not args.allow_local:
        print("refusing bridge benchmark outside GitHub Actions without --allow-local", file=sys.stderr)
        return 1

    repo_root = pathlib.Path(__file__).resolve().parents[1]
    compose_file = (repo_root / args.compose_file).resolve()
    if not compose_file.is_file():
        raise FileNotFoundError(compose_file)
    prefix = compose_prefix(compose_file)
    started_at = utc_now()
    rows = []
    try:
        for lane in args.selected_lanes:
            print(f"benchmarking {lane}", flush=True)
            rows.append(
                benchmark_lane(
                    LANES[lane],
                    prefix,
                    warmups=args.warmups,
                    samples=args.samples,
                    ready_timeout=args.ready_timeout,
                    request_timeout=args.request_timeout,
                )
            )
    finally:
        run([*prefix, "down", "--remove-orphans"], check=False)

    source_commit = run(["git", "rev-parse", "HEAD"]).stdout.strip()
    source_modified = bool(run(["git", "status", "--porcelain", "--untracked-files=no"]).stdout.strip())
    report = build_report(
        rows=rows,
        source_commit=source_commit,
        source_modified=source_modified,
        started_at=started_at,
        completed_at=utc_now(),
        environment={
            "os": platform.platform(),
            "machine": platform.machine(),
            "cpu": cpu_model(),
            "logical_cpus": os.cpu_count(),
            "docker_server_arch": run(["docker", "info", "--format", "{{.Architecture}}"]).stdout.strip(),
        },
        source_manifest=source_manifest(repo_root),
        warmups=args.warmups,
        samples=args.samples,
    )
    output = pathlib.Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(json.dumps({"status": report["status"], "output": str(output), "lanes": len(rows)}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
