import importlib.util
import json
import pathlib
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).parents[1] / "analyze_agent_python_ultimate.py"
SPEC = importlib.util.spec_from_file_location("analyze_agent_python_ultimate", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


class AnalyzeAgentPythonUltimateTests(unittest.TestCase):
    def sample_report(self):
        requests = [
            {"index": 0, "started_utc": "2026-07-27T12:00:00Z", "duration_ns": 10_000_000, "outcome": "ok"},
            {"index": 1, "started_utc": "2026-07-27T12:00:00.005Z", "duration_ns": 20_000_000, "outcome": "ok"},
            {"index": 2, "started_utc": "2026-07-27T12:00:00.010Z", "duration_ns": 30_000_000, "outcome": "ok"},
        ]
        phases = []
        for request_id, checkout, execute, restore in [
            (1, 1_000_000, 6_000_000, 2_000_000),
            (2, 2_000_000, 12_000_000, 4_000_000),
            (3, 3_000_000, 18_000_000, 6_000_000),
        ]:
            phases.extend([
                {"phase": "checkout", "purpose": "request", "request_id": request_id, "duration_ns": checkout, "outcome": "ok"},
                {"phase": "execute", "purpose": "request", "request_id": request_id, "duration_ns": execute, "outcome": "ok"},
                {"phase": "restore", "purpose": "request", "request_id": request_id, "duration_ns": restore, "memory_bytes": 67_108_864, "snapshot_selected": "cow", "outcome": "ok"},
            ])
        return {
            "schema": "agent-python-ultimate-run-report/v1",
            "metadata": {"source_commit": "a" * 40, "complete": True, "rows_planned": 1, "rows_completed": 1},
            "plan": {"schema": "agent-python-ultimate-plan/v1", "rows": []},
            "rows": [{
                "schema": "agent-python-ultimate-worker-result/v1",
                "row": {"row_id": "row-1", "campaign": "concurrency", "lifecycle": "snapshot/cow", "pool": 4, "prepared_capacity": 4, "repeat": 3, "dirty_bps": 1000, "arena_mib": 64, "concurrency": 3, "cache_state": "warm", "surface": "direct", "snapshot_selected": "cow"},
                "status": "ok", "started_utc": "2026-07-27T12:00:00Z", "finished_utc": "2026-07-27T12:00:00.050Z",
                "startup_duration_ns": 1, "shutdown_duration_ns": 1,
                "requests": requests, "phases": phases,
                "snapshot_requested": "cow", "snapshot_selected": "cow",
                "process_before": {"platform": "linux"}, "process_before_shutdown": {"platform": "linux", "pss_bytes": 1000}, "process_after": {"platform": "linux"},
            }],
            "by_campaign": [], "by_lane": [],
        }

    def test_derives_latency_throughput_inflight_and_phase_distributions(self):
        analysis = MODULE.analyze(self.sample_report())
        row = analysis["rows"][0]
        self.assertEqual(20_000_000, row["latency_ns"]["p50"])
        self.assertEqual(30_000_000, row["latency_ns"]["p95"])
        self.assertEqual(30_000_000, row["latency_ns"]["p99"])
        self.assertFalse(row["latency_ns"]["p99_claim_eligible"])
        self.assertAlmostEqual(75.0, row["throughput_rps"])
        self.assertAlmostEqual(1.5, row["average_inflight"])
        self.assertAlmostEqual(row["average_inflight"], row["little_law_lambda_w"], places=12)
        self.assertEqual(2_000_000, row["phase_ns"]["checkout"]["p50"])
        self.assertEqual(4_000_000, row["phase_ns"]["restore"]["p50"])
        self.assertEqual(67_108_864, row["restore_memory_bytes"])

    def test_excludes_failed_requests_from_performance_aggregate_but_counts_them(self):
        report = self.sample_report()
        report["rows"][0]["requests"][1]["outcome"] = "failed"
        analysis = MODULE.analyze(report)
        row = analysis["rows"][0]
        self.assertEqual(2, row["successful_requests"])
        self.assertEqual(1, row["failed_requests"])
        self.assertEqual(20_000_000, row["latency_ns"]["p50"])

    def test_rejects_incomplete_or_noncanonical_input(self):
        report = self.sample_report()
        report["metadata"]["complete"] = False
        with self.assertRaisesRegex(ValueError, "complete"):
            MODULE.analyze(report)

    def test_cli_writes_deterministic_json(self):
        report = self.sample_report()
        with tempfile.TemporaryDirectory() as directory:
            source = pathlib.Path(directory) / "report.json"
            first = pathlib.Path(directory) / "analysis-a.json"
            second = pathlib.Path(directory) / "analysis-b.json"
            source.write_text(json.dumps(report), encoding="utf-8")
            self.assertEqual(0, MODULE.main([str(source), "--output", str(first)]))
            self.assertEqual(0, MODULE.main([str(source), "--output", str(second)]))
            self.assertEqual(first.read_bytes(), second.read_bytes())


if __name__ == "__main__":
    unittest.main()
