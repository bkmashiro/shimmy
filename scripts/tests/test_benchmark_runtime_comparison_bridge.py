import importlib.util
import pathlib
import sys
import unittest


SCRIPT = pathlib.Path(__file__).parents[1] / "benchmark-runtime-comparison-bridge.py"
SPEC = importlib.util.spec_from_file_location("benchmark_runtime_comparison_bridge", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load benchmark script: {SCRIPT}")
module = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = module
SPEC.loader.exec_module(module)


class BridgeContractTests(unittest.TestCase):
    def test_expected_checksum_matches_golden_cases(self):
        self.assertEqual(module.expected_checksum(0, 7), "0000000000000007")
        self.assertEqual(module.expected_checksum(100_000, 7), "5e7135fac6225d57")

    def test_clean_lane_requires_counter_one(self):
        response = {
            "command": "eval",
            "result": {
                "is_correct": True,
                "work_checksum": "0000000000000007",
                "guest_invocation_count": 1,
            },
        }
        observed = module.validate_response(response, expected_checksum="0000000000000007")
        module.validate_counter_sequence("clean", [observed, observed])
        bad = dict(observed)
        bad["guest_invocation_count"] = 2
        with self.assertRaisesRegex(ValueError, "clean lane"):
            module.validate_counter_sequence("clean", [observed, bad])

    def test_semantic_fixture_may_omit_contract_checksum(self):
        response = {
            "command": "eval",
            "result": {"is_correct": True, "guest_invocation_count": 1},
        }
        observed = module.validate_response(
            response,
            expected_checksum="not-used-for-this-edge",
            require_checksum=False,
        )
        self.assertEqual(observed["guest_invocation_count"], 1)
        generic = module.LANES["system-generic-restore"]
        self.assertFalse(generic.reports_checksum)
        self.assertFalse(generic.supports_cpu_workload)

    def test_pyodide_legacy_mode_qualifies_clean_namespace_only(self):
        lane = module.LANES["python-pyodide-clean-namespace"]
        self.assertEqual(lane.lifecycle_class, "clean")
        runner = (SCRIPT.parents[1] / "examples" / "eval-pyodide" / "runner.js").read_text()
        self.assertIn("Fresh namespace — state isolation", runner)
        self.assertIn("_ns = {}", runner)

    def test_persistent_lane_requires_monotonic_counter(self):
        samples = [
            {"guest_invocation_count": 1},
            {"guest_invocation_count": 2},
            {"guest_invocation_count": 3},
        ]
        module.validate_counter_sequence("persistent", samples)
        with self.assertRaisesRegex(ValueError, "monotonic"):
            module.validate_counter_sequence("persistent", [samples[0], samples[2]])

    def test_report_has_only_declared_comparison_edges(self):
        reports = []
        python_sha = "c" * 64
        native_sha = "d" * 64
        for lane in module.LANES.values():
            reports.append(
                {
                    "lane": lane.lane,
                    "family": lane.family,
                    "lifecycle_class": lane.lifecycle_class,
                    "fixture_file": {
                        "sha256": python_sha if lane.family == "python" else native_sha,
                    },
                    "workloads": {"fixed": {"steady": module.summarize_ns([1, 2, 3])}},
                }
            )
        report = module.build_report(
            rows=reports,
            source_commit="a" * 40,
            source_modified=False,
            started_at="2026-07-28T00:00:00Z",
            completed_at="2026-07-28T00:01:00Z",
            environment={"cpu": "test"},
            source_manifest={
                "contract.go": "b" * 64,
                "experiments/runtime-comparison-bridge/python/eval.py": python_sha,
            },
            warmups=2,
            samples=3,
        )
        self.assertEqual(report["schema"], "shimmy-runtime-comparison-bridge/v1")
        self.assertEqual(report["status"], "PASS")
        self.assertFalse(report["universal_ranking_allowed"])
        edge_ids = {edge["id"] for edge in report["comparison_edges"]}
        self.assertEqual(
            edge_ids,
            {
                "system-native-vs-wasm-semantic",
                "python-warm",
                "python-clean",
            },
        )

    def test_report_rejects_fixture_identity_drift(self):
        rows = [
            {
                "lane": "python-native-fresh",
                "family": "python",
                "lifecycle_class": "clean",
                "workloads": {},
                "fixture_file": {"sha256": "b" * 64},
            },
        ]
        with self.assertRaisesRegex(ValueError, "Python fixture SHA-256 mismatch"):
            module.build_report(
                rows=rows,
                source_commit="a" * 40,
                source_modified=False,
                started_at="2026-07-28T00:00:00Z",
                completed_at="2026-07-28T00:01:00Z",
                environment={},
                source_manifest={
                    "experiments/runtime-comparison-bridge/python/eval.py": "a" * 64,
                },
                warmups=0,
                samples=1,
            )
    def test_qemu_fixture_provenance_uses_sidecar_not_runtime_manifest(self):
        repo_root = SCRIPT.parents[1]
        build_script = (repo_root / "experiments" / "qemu-fallback" / "build-image.sh").read_text()
        runtime_manifest = build_script.split('cat >"$OUTPUT_DIR/manifest.json" <<MANIFEST', 1)[1].split(
            "\nMANIFEST\n", 1
        )[0]
        self.assertNotIn("file_evaluator", runtime_manifest)
        self.assertIn('cat >"$OUTPUT_DIR/file-evaluator-manifest.json" <<MANIFEST', build_script)
        benchmark_script = (repo_root / "experiments" / "qemu-fallback" / "benchmark-ci-file.sh").read_text()
        self.assertIn('file-evaluator-manifest.json', benchmark_script)


if __name__ == "__main__":
    unittest.main()
