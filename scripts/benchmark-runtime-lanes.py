#!/usr/bin/env python3
"""Benchmark maintained Shimmy runtime lanes through the public HTTP boundary.

The workloads are intentionally lane-specific. The report separates server readiness,
first request, and steady requests so persistent and fresh lifecycles are not collapsed
into one misleading ranking.
"""

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
import statistics
import subprocess
import sys
import time
import urllib.error
import urllib.request
from typing import Any, Dict, List, Sequence


@dataclasses.dataclass(frozen=True)
class LaneSpec:
    service: str
    port: int
    payload: Dict[str, Any]
    workload: str
    lifecycle: str
    initialization_placement: str
    resource_limit: str


LANES: Dict[str, LaneSpec] = {
    "generic": LaneSpec(
        service="generic",
        port=18081,
        payload={"response": "42", "answer": "42", "params": {}},
        workload="stateful generic WASM equality fixture",
        lifecycle="persistent Shimmy server; one pooled WASM instance restored after every request",
        initialization_placement="module compilation and pool startup occur before HTTP readiness",
        resource_limit="1 CPU / 256 MiB",
    ),
    "reactor-consumer": LaneSpec(
        service="python-reactor",
        port=18082,
        payload={"response": "3.14159", "answer": "3.1416", "params": {"tolerance": 0.001}},
        workload="Python numeric-tolerance evaluator consumed through the pinned external runtime artifact",
        lifecycle="persistent Shimmy server; fresh guest instance lifecycle is owned by the consumer adapter",
        initialization_placement="artifact verification and runtime initialization occur before HTTP readiness",
        resource_limit="2 CPUs / 2 GiB",
    ),
    "pyodide-scipy": LaneSpec(
        service="pyodide-scipy",
        port=18083,
        payload={
            "response": "",
            "answer": "5.0",
            "params": {"test": "ttest", "samples": [4.9, 5.1, 5.0, 5.2, 4.8], "alpha": 0.05},
        },
        workload="Pyodide SciPy one-sample t-test fixture",
        lifecycle="persistent Node/Pyodide worker behind Shimmy framed stdio",
        initialization_placement="HTTP readiness covers Shimmy; lazy child/package work may appear in first request",
        resource_limit="2 CPUs / 2 GiB",
    ),
    "dbi-lean": LaneSpec(
        service="dbi-lean",
        port=18084,
        payload={
            "response": "shimmy-lean",
            "answer": "shimmy-lean",
            "params": {"correct_response_feedback": "Correct from Lean"},
        },
        workload="Lean file evaluator wrapped by DynamoRIO path-policy client",
        lifecycle="persistent Shimmy server; fresh file evaluator process and DBI wrapper per request",
        initialization_placement="Shimmy is ready before per-request native process and DBI startup",
        resource_limit="2 CPUs / 2 GiB",
    ),
}


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")


def summarize_ns(samples: Sequence[int]) -> Dict[str, Any]:
    if not samples:
        raise ValueError("at least one sample is required")
    ordered = sorted(samples)
    rank = max(1, math.ceil(0.95 * len(ordered)))
    return {
        "sample_count": len(ordered),
        "samples_ns": list(samples),
        "min_ns": ordered[0],
        "median_ns": statistics.median(ordered),
        "mean_ns": statistics.fmean(ordered),
        "p95_ns": ordered[rank - 1],
        "max_ns": ordered[-1],
    }


def result_object(response: Dict[str, Any]) -> Dict[str, Any]:
    result = response.get("result")
    if not isinstance(result, dict):
        raise ValueError("response is missing a result object")
    return result


def assert_lane_response(lane: str, response: Dict[str, Any]) -> None:
    result = result_object(response)
    if lane == "generic":
        if result.get("is_correct") is not True:
            raise ValueError("generic is_correct must be true")
        if result.get("guest_invocation_count") != 1:
            raise ValueError("generic guest_invocation_count must be 1")
        if result.get("snapshot_isolation_ok") is not True:
            raise ValueError("generic snapshot_isolation_ok must be true")
        return
    if lane in {"reactor-consumer", "dbi-lean"}:
        if result.get("is_correct") is not True:
            raise ValueError(f"{lane} is_correct must be true")
        return
    if lane == "pyodide-scipy":
        if result.get("is_correct") is not True:
            raise ValueError("pyodide-scipy is_correct must be true")
        if result.get("n_samples") != 5:
            raise ValueError("pyodide-scipy n_samples must be 5")
        p_value = result.get("p_value")
        if not isinstance(p_value, (int, float)) or isinstance(p_value, bool):
            raise ValueError("pyodide-scipy p_value must be numeric")
        return
    raise ValueError(f"unknown lane: {lane}")


def response_digest(response: Dict[str, Any]) -> str:
    encoded = json.dumps(response, sort_keys=True, separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def build_qemu_report(
    *,
    native_samples_ns: Sequence[int],
    qemu_samples_ns: Sequence[int],
    native_responses: Sequence[Dict[str, Any]],
    qemu_responses: Sequence[Dict[str, Any]],
    manifest_digests: Dict[str, str],
    qemu_version: str,
    source_commit: str,
) -> Dict[str, Any]:
    count = len(native_samples_ns)
    if count == 0 or len(qemu_samples_ns) != count:
        raise ValueError("native and QEMU samples must be non-empty and equal length")
    if len(native_responses) != count or len(qemu_responses) != count:
        raise ValueError("response count must match timing samples")
    for index, (native, qemu) in enumerate(zip(native_responses, qemu_responses), start=1):
        if native != qemu:
            raise ValueError(f"response mismatch at sample {index}")
        if result_object(native).get("is_correct") is not True:
            raise ValueError(f"sample {index} is_correct must be true")
    return {
        "schema": "shimmy-qemu-file-benchmark/v1",
        "status": "PASS",
        "source_commit": source_commit,
        "accelerator": "tcg",
        "interface": "file",
        "lifecycle": "fresh QEMU VM per file-interface request",
        "timing_scope": "public HTTP request including guest boot, evaluator execution, response copy, and VM shutdown",
        "response_parity": True,
        "native": summarize_ns(native_samples_ns),
        "qemu": summarize_ns(qemu_samples_ns),
        "manifest_digests": manifest_digests,
        "qemu_version": qemu_version,
    }


def build_qemu_rpc_prewarm_report(
    *,
    native: Dict[str, Any],
    persistent: Dict[str, Any],
    lazy: Dict[str, Any],
    eager: Dict[str, Any],
    manifest_digests: Dict[str, str],
    qemu_version: str,
    source_commit: str,
) -> Dict[str, Any]:
    lanes = {"native": native, "off": persistent, "lazy": lazy}
    for name, row in lanes.items():
        repeated = row.get("repeated_ns")
        responses = row.get("responses")
        if not isinstance(repeated, list) or not repeated:
            raise ValueError(f"{name} repeated_ns must contain samples")
        if not isinstance(responses, list) or len(responses) != len(repeated) + 1:
            raise ValueError(f"{name} response count must equal first plus repeated samples")
        for index, response in enumerate(responses):
            if result_object(response).get("is_correct") is not True:
                raise ValueError(f"{name} response {index} is_correct must be true")

    native_responses = native["responses"]
    for name, row in (("off", persistent), ("lazy", lazy)):
        for index, (expected, observed) in enumerate(zip(native_responses, row["responses"])):
            if expected != observed:
                raise ValueError(f"{name} response mismatch at sample {index}")

    persistent_boots = persistent.get("boot_ids")
    lazy_boots = lazy.get("boot_ids")
    if not isinstance(persistent_boots, list) or len(persistent_boots) < 2 or len(set(persistent_boots)) != 1:
        raise ValueError("persistent boot IDs must be available and identical")
    if persistent_boots[0] in {"", "unavailable"}:
        raise ValueError("persistent boot IDs must be available and identical")
    if not isinstance(lazy_boots, list) or len(lazy_boots) < 2 or len(set(lazy_boots)) != len(lazy_boots):
        raise ValueError("lazy boot IDs must be available and distinct")
    if any(boot_id in {"", "unavailable"} for boot_id in lazy_boots):
        raise ValueError("lazy boot IDs must be available and distinct")

    eager_later = eager.get("later_ready_ns")
    eager_responses = eager.get("responses")
    if not isinstance(eager_later, list) or not eager_later:
        raise ValueError("eager later_ready_ns must contain samples")
    if not isinstance(eager_responses, list) or len(eager_responses) != len(eager_later) + 2:
        raise ValueError("eager response count must equal first, immediate next, and later-ready samples")
    eager_boots = []
    for index, response in enumerate(eager_responses):
        result = result_object(response)
        if result.get("is_correct") is not True:
            raise ValueError(f"eager response {index} is_correct must be true")
        boot_id = result.get("boot_id")
        if not isinstance(boot_id, str) or boot_id in {"", "unavailable"}:
            raise ValueError(f"eager response {index} boot ID must be available")
        if result.get("guest_invocation_count") != 1:
            raise ValueError(f"eager response {index} must be the first invocation in a clean evaluator")
        eager_boots.append(boot_id)
    if len(set(eager_boots)) != len(eager_boots):
        raise ValueError("eager boot IDs must be distinct for every served request")
    if eager.get("startup_interarrival_ns", 0) <= 0 or eager.get("later_interarrival_ns", 0) <= 0:
        raise ValueError("eager preparation intervals must be positive")
    if eager.get("first_ready_ns", 0) <= 0 or eager["first_ready_ns"] >= 1_000_000_000:
        raise ValueError("eager first prepared request must complete below the 1 second ready-hit bound")
    if eager.get("immediate_next_ns", 0) <= 0:
        raise ValueError("eager immediate-next request must be measured")
    if any(value <= 0 or value >= 1_000_000_000 for value in eager_later):
        raise ValueError("eager later-ready requests must complete below the 1 second ready-hit bound")

    def phase(row: Dict[str, Any]) -> Dict[str, Any]:
        liveness_ns = row["liveness_ns"]
        prewarm_ns = row["prewarm_probe_ns"]
        first_ns = row["first_ns"]
        return {
            "http_liveness_ns": liveness_ns,
            "prewarm_probe_ns": prewarm_ns,
            "liveness_plus_prewarm_ns": liveness_ns + prewarm_ns,
            "first_request_after_prewarm_ns": first_ns,
            "liveness_plus_prewarm_plus_first_ns": liveness_ns + prewarm_ns + first_ns,
            "repeated_requests": summarize_ns(row["repeated_ns"]),
            "response_batch_sha256": response_digest({"responses": row["responses"]}),
        }

    return {
        "schema": "shimmy-qemu-rpc-prewarm-benchmark/v3",
        "status": "PASS",
        "source_commit": source_commit,
        "accelerator": "tcg",
        "interface": "rpc",
        "transport": "stdio",
        "request_concurrency": 1,
        "response_parity": True,
        "eager_response_contract": True,
        "http_health_semantics": "process HTTP liveness only; it does not prove that the guest runtime is ready",
        "prewarm_probe": "public HTTP request with Command: healthcheck routed through the selected evaluator",
        "manifest_digests": manifest_digests,
        "qemu_version": qemu_version,
        "native": {
            **phase(native),
            "lifecycle": "persistent native RPC evaluator",
            "prewarm_probe_semantics": "wait for the evaluator-level health response before timed evaluation",
        },
        "policies": {
            "off": {
                **phase(persistent),
                "reset_policy": "off",
                "lifecycle": "persistent QEMU RPC VM reused across requests",
                "initialization_placement": "public /health is liveness only; guest boot is charged to the evaluator-level prewarm probe",
                "repeated_request_semantics": "warm requests in the same guest VM",
                "prewarm_effective": True,
                "boot_identity": {
                    "prewarm_probe": persistent_boots[0],
                    "post_request_probe": persistent_boots[1],
                    "same": True,
                },
            },
            "lazy": {
                **phase(lazy),
                "reset_policy": "lazy",
                "lifecycle": "invocation-scoped QEMU RPC VM booted and destroyed for every request",
                "initialization_placement": "the evaluator-level prewarm probe boots a one-shot guest that is destroyed before the next request",
                "repeated_request_semantics": "repeated fresh-VM requests; not a warm path",
                "prewarm_effective": False,
                "boot_identity": {
                    "prewarm_probe": lazy_boots[0],
                    "post_request_probe": lazy_boots[1],
                    "distinct": True,
                },
            },
            "eager": {
                "reset_policy": "eager",
                "lifecycle": "each QEMU VM serves exactly one request; old VM exit precedes background spawn of one clean replacement",
                "initialization_placement": "replacement boot overlaps the configured idle interval after the prior evaluation",
                "http_liveness_ns": eager["liveness_ns"],
                "startup_preparation_interval_ns": eager["startup_interarrival_ns"],
                "first_ready_request_ns": eager["first_ready_ns"],
                "immediate_next_request_ns": eager["immediate_next_ns"],
                "later_interarrival_ns": eager["later_interarrival_ns"],
                "later_ready_requests": summarize_ns(eager_later),
                "ready_hit_upper_bound_ns": 1_000_000_000,
                "fresh_state_proven": True,
                "boot_identity": {
                    "per_request": eager_boots,
                    "all_distinct": True,
                },
                "response_batch_sha256": response_digest({"responses": eager_responses}),
            },
        },
        "deployment_boundary": "eager is qualified here only for a continuously running non-Lambda HTTP service; Lambda still rejects it without an owned Runtime API response/next loop",
        "comparison_policy": "compare persistent reuse, invocation-scoped fresh boot, eager immediate-next wait, and eager later ready-hit separately",
    }


def build_lane_report(
    *,
    lane: str,
    workload: str,
    lifecycle: str,
    initialization_placement: str,
    ready_ns: int,
    first_ns: int,
    steady_samples_ns: Sequence[int],
    warmup_count: int,
    response: Dict[str, Any],
    image: Dict[str, Any],
) -> Dict[str, Any]:
    assert_lane_response(lane, response)
    return {
        "lane": lane,
        "workload": workload,
        "lifecycle": lifecycle,
        "initialization_placement": initialization_placement,
        "server_ready_semantics": "container start until Shimmy public /health succeeds; child evaluator readiness may be lazy",
        "server_ready_ns": ready_ns,
        "first_request_ns": first_ns,
        "ready_plus_first_ns": ready_ns + first_ns,
        "warmup_count": warmup_count,
        "steady": summarize_ns(steady_samples_ns),
        "response_sha256": response_digest(response),
        "image": image,
    }


def run(command: Sequence[str], *, capture: bool = True, check: bool = True) -> subprocess.CompletedProcess:
    return subprocess.run(
        list(command),
        check=check,
        text=True,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.PIPE if capture else None,
    )


def compose_prefix(compose_file: pathlib.Path) -> List[str]:
    return ["docker", "compose", "--profile", "dbi", "-f", str(compose_file)]


def request_json(url: str, payload: Dict[str, Any], timeout: float) -> Dict[str, Any]:
    request = urllib.request.Request(
        url,
        data=json.dumps(payload, separators=(",", ":")).encode("utf-8"),
        headers={"Content-Type": "application/json", "Command": "eval"},
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
        except Exception as exc:  # readiness retains the last concrete failure
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


def cpu_model() -> str:
    path = pathlib.Path("/proc/cpuinfo")
    if path.exists():
        for line in path.read_text(errors="replace").splitlines():
            if line.lower().startswith("model name") and ":" in line:
                return line.split(":", 1)[1].strip()
    return platform.processor() or "unknown"


def git_commit() -> str:
    return run(["git", "rev-parse", "HEAD"]).stdout.strip()


def benchmark_lane(
    lane: str,
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
    try:
        run([*prefix, "up", "-d", "--no-build", spec.service])
        image = image_metadata(prefix, spec.service)
        base = f"http://127.0.0.1:{spec.port}"
        wait_for_health(base + "/health", ready_timeout)
        ready_ns = time.perf_counter_ns() - started

        first_ns, response = timed_request(base + "/", spec.payload, request_timeout)
        assert_lane_response(lane, response)

        for _ in range(warmups):
            _, warm_response = timed_request(base + "/", spec.payload, request_timeout)
            assert_lane_response(lane, warm_response)

        steady_samples: List[int] = []
        for _ in range(samples):
            elapsed, sample_response = timed_request(base + "/", spec.payload, request_timeout)
            assert_lane_response(lane, sample_response)
            steady_samples.append(elapsed)
            response = sample_response

        report = build_lane_report(
            lane=lane,
            workload=spec.workload,
            lifecycle=spec.lifecycle,
            initialization_placement=spec.initialization_placement,
            ready_ns=ready_ns,
            first_ns=first_ns,
            steady_samples_ns=steady_samples,
            warmup_count=warmups,
            response=response,
            image=image,
        )
        report["service"] = spec.service
        report["resource_limit"] = spec.resource_limit
        if lane == "dbi-lean":
            marker = run([*prefix, "exec", "-T", spec.service, "cat", "/tmp/shimmy-dbi-client-init.marker"]).stdout.strip()
            if marker != "initialized":
                raise ValueError(f"DBI client marker mismatch: {marker!r}")
            report["dbi_client_marker"] = marker
        return report
    except Exception:
        logs = run([*prefix, "logs", "--no-color", "--tail=200", spec.service], check=False).stdout
        if logs:
            print(f"--- {lane} logs ---\n{logs}", file=sys.stderr)
        raise
    finally:
        run([*prefix, "rm", "-s", "-f", spec.service], check=False)


def benchmark_compose(args: argparse.Namespace) -> Dict[str, Any]:
    compose_file = pathlib.Path(args.compose_file).resolve()
    if not compose_file.is_file():
        raise FileNotFoundError(compose_file)
    selected = args.lanes.split(",")
    unknown = [lane for lane in selected if lane not in LANES]
    if unknown:
        raise ValueError(f"unknown lanes: {unknown}")
    prefix = compose_prefix(compose_file)
    started_at = utc_now()
    rows = []
    try:
        for lane in selected:
            print(f"benchmarking {lane}", flush=True)
            rows.append(
                benchmark_lane(
                    lane,
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

    return {
        "schema": "shimmy-runtime-lanes-benchmark/v1",
        "status": "PASS",
        "benchmark_kind": "public-http-runtime-lanes",
        "started_at": started_at,
        "completed_at": utc_now(),
        "source_commit": git_commit(),
        "workflow_run_id": os.getenv("GITHUB_RUN_ID"),
        "environment": {
            "os": platform.platform(),
            "machine": platform.machine(),
            "cpu": cpu_model(),
            "logical_cpus": os.cpu_count(),
            "docker_server_arch": run(["docker", "info", "--format", "{{.Architecture}}"]).stdout.strip(),
        },
        "method": {
            "warmup_count": args.warmups,
            "steady_sample_count": args.samples,
            "request_concurrency": 1,
            "comparison_policy": "workloads and lifecycle differ; compare phases within each lane, not as a universal runtime ranking",
        },
        "lanes": rows,
    }


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--compose-file", default="demo/compose/compose.yaml")
    parser.add_argument("--output", required=True)
    parser.add_argument("--lanes", default=",".join(LANES))
    parser.add_argument("--warmups", type=int, default=2)
    parser.add_argument("--samples", type=int, default=10)
    parser.add_argument("--ready-timeout", type=float, default=240)
    parser.add_argument("--request-timeout", type=float, default=180)
    args = parser.parse_args(argv)
    if args.warmups < 0:
        parser.error("--warmups must be non-negative")
    if args.samples < 1 or args.samples > 1000:
        parser.error("--samples must be in [1,1000]")
    return args


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    report = benchmark_compose(args)
    output = pathlib.Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    print(json.dumps(report, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
