#!/usr/bin/env python3
"""Derive reproducible runtime performance metrics from ultimate raw evidence."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import math
import pathlib
import statistics
import sys
from collections import defaultdict
from typing import Any, Iterable


ANALYSIS_SCHEMA = "agent-python-runtime-performance-analysis/v1"
REPORT_SCHEMA = "agent-python-ultimate-run-report/v1"


def _timestamp_ns(value: str) -> int:
    fraction_ns = 0
    time_part = value.split("T", 1)[-1]
    if "." in time_part:
        fraction = time_part.split(".", 1)[1]
        fraction = fraction.split("Z", 1)[0].split("+", 1)[0].split("-", 1)[0]
        fraction_ns = int((fraction + "000000000")[:9])
    if value.endswith("Z"):
        value = value[:-1] + "+00:00"
    parsed = dt.datetime.fromisoformat(value)
    if parsed.tzinfo is None:
        raise ValueError("timestamps must include a timezone")
    utc = parsed.astimezone(dt.timezone.utc)
    epoch = dt.datetime(1970, 1, 1, tzinfo=dt.timezone.utc)
    delta = utc - epoch
    whole_ns = (delta.days * 86_400 + delta.seconds) * 1_000_000_000
    # fromisoformat retains microseconds; replace its truncated fraction with
    # the exact RFC3339Nano fraction extracted above.
    return whole_ns + (fraction_ns if "." in time_part else delta.microseconds * 1_000)


def _nearest_rank(values: list[int], probability: float) -> int:
    if not values:
        raise ValueError("percentile requires samples")
    ordered = sorted(values)
    rank = max(1, math.ceil(probability * len(ordered)))
    return ordered[rank - 1]


def _distribution(values: Iterable[int]) -> dict[str, Any]:
    samples = sorted(int(value) for value in values)
    if not samples:
        return {"n": 0, "p99_claim_eligible": False}
    return {
        "n": len(samples),
        "min": samples[0],
        "mean": statistics.fmean(samples),
        "p50": int(statistics.median(samples)),
        "p90": _nearest_rank(samples, 0.90),
        "p95": _nearest_rank(samples, 0.95),
        "p99": _nearest_rank(samples, 0.99),
        "max": samples[-1],
        "p99_claim_eligible": len(samples) >= 100,
    }


def _request_window(requests: list[dict[str, Any]]) -> tuple[int, int]:
    starts = [_timestamp_ns(sample["started_utc"]) for sample in requests]
    finishes = [
        started + int(sample["duration_ns"])
        for started, sample in zip(starts, requests)
    ]
    return min(starts), max(finishes)


def _analyze_row(result: dict[str, Any]) -> dict[str, Any]:
    row = result["row"]
    successful = [sample for sample in result.get("requests", []) if sample.get("outcome") == "ok"]
    failed = [sample for sample in result.get("requests", []) if sample.get("outcome") != "ok"]
    durations = [int(sample["duration_ns"]) for sample in successful]

    throughput = None
    average_inflight = None
    little_law = None
    window_ns = None
    if successful:
        window_start, window_finish = _request_window(successful)
        window_ns = window_finish - window_start
        if window_ns <= 0:
            raise ValueError(f"non-positive request window for {row['row_id']}")
        window_seconds = window_ns / 1_000_000_000
        throughput = len(successful) / window_seconds
        average_inflight = sum(durations) / 1_000_000_000 / window_seconds
        little_law = throughput * (statistics.fmean(durations) / 1_000_000_000)

    phases: dict[str, list[int]] = defaultdict(list)
    restore_memory = set()
    for event in result.get("phases", []):
        if event.get("purpose") != "request" or event.get("outcome") != "ok":
            continue
        phase = event.get("phase")
        if not isinstance(phase, str):
            continue
        phases[phase].append(int(event["duration_ns"]))
        if phase == "restore" and int(event.get("memory_bytes", 0)) > 0:
            restore_memory.add(int(event["memory_bytes"]))
    if len(restore_memory) > 1:
        raise ValueError(f"restore memory drift for {row['row_id']}")

    return {
        "row_id": row["row_id"],
        "campaign": row["campaign"],
        "lifecycle": row["lifecycle"],
        "status": result["status"],
        "pool": row["pool"],
        "prepared_capacity": row["prepared_capacity"],
        "concurrency": row.get("concurrency", 1),
        "cpu_profile": row.get("cpu_profile", "none"),
        "arena_mib": row.get("arena_mib", 0),
        "dirty_bps": row.get("dirty_bps", 0),
        "dirty_pattern": row.get("dirty_pattern", ""),
        "successful_requests": len(successful),
        "failed_requests": len(failed),
        "measurement_window_ns": window_ns,
        "throughput_rps": throughput,
        "average_inflight": average_inflight,
        "little_law_lambda_w": little_law,
        "latency_ns": _distribution(durations),
        "phase_ns": {name: _distribution(values) for name, values in sorted(phases.items())},
        "restore_memory_bytes": next(iter(restore_memory), None),
        "process_pss_before_shutdown_bytes": result.get("process_before_shutdown", {}).get("pss_bytes"),
        "process_private_dirty_before_shutdown_bytes": result.get("process_before_shutdown", {}).get("private_dirty_bytes"),
    }


def analyze(report: dict[str, Any]) -> dict[str, Any]:
    if report.get("schema") != REPORT_SCHEMA:
        raise ValueError("unsupported canonical report schema")
    metadata = report.get("metadata")
    if not isinstance(metadata, dict) or metadata.get("complete") is not True:
        raise ValueError("canonical report must be complete")
    rows = report.get("rows")
    if not isinstance(rows, list):
        raise ValueError("canonical report rows are required")
    planned = metadata.get("rows_planned")
    completed = metadata.get("rows_completed")
    if planned != completed or completed != len(rows):
        raise ValueError("canonical report row counts do not match")

    analyzed_rows = [_analyze_row(row) for row in rows]
    return {
        "schema": ANALYSIS_SCHEMA,
        "source": {
            "report_schema": REPORT_SCHEMA,
            "source_commit": metadata.get("source_commit"),
            "rows": len(rows),
        },
        "formulae": {
            "little_law": "L = lambda * W",
            "stable_throughput_approximation": "lambda ~= C / W only below saturation with bounded queue growth",
            "pool_bound": "lambda_pool ~= pool_size / mean_slot_hold_time",
            "reset_model": "T_reset ~= T_fixed + alpha*dirty_unique_pages + beta*mapped_bytes + gamma*cleanup_work",
        },
        "claim_boundaries": [
            "p99_claim_eligible requires at least 100 successful samples in the row",
            "throughput is a closed-window observation over successful requests, not an open-loop arrival-rate result",
            "average_inflight is the exact request-interval integral divided by the measurement window",
            "dirty_bps is requested workload input; it is not an observed physical dirty-page count",
            "phase durations are observer-instrumented and must not be added to end-to-end latency across independent runs",
        ],
        "rows": analyzed_rows,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("report")
    parser.add_argument("--output", required=True)
    args = parser.parse_args(argv)
    source = pathlib.Path(args.report)
    output = pathlib.Path(args.output)
    report = json.loads(source.read_text(encoding="utf-8"))
    result = analyze(report)
    encoded = json.dumps(result, ensure_ascii=False, indent=2, sort_keys=True) + "\n"
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(encoded, encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main())
