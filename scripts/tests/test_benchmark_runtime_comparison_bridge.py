import importlib.util
import pathlib
import sys
import tempfile
import unittest
from unittest import mock


SCRIPT = pathlib.Path(__file__).parents[1] / "benchmark-runtime-comparison-bridge.py"
SPEC = importlib.util.spec_from_file_location("benchmark_runtime_comparison_bridge", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load benchmark script: {SCRIPT}")
module = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = module
SPEC.loader.exec_module(module)


class BridgeContractTests(unittest.TestCase):
    def load_qemu_fixture_validator(self):
        validator_path = SCRIPT.parents[1] / "experiments" / "qemu-fallback" / "validate-evaluator-fixture.py"
        spec = importlib.util.spec_from_file_location("validate_evaluator_fixture", validator_path)
        if spec is None or spec.loader is None:
            raise RuntimeError(f"cannot load QEMU fixture validator: {validator_path}")
        validator = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(validator)
        return validator

    def test_qemu_fixture_validator_rejects_workspace_escape(self):
        validator = self.load_qemu_fixture_validator()
        repo_root = SCRIPT.parents[1]
        with self.assertRaisesRegex(ValueError, "repository-relative"):
            validator.validate(repo_root, "/tmp/pkg", "fixture-v1")
        with self.assertRaisesRegex(ValueError, "parent traversal"):
            validator.validate(repo_root, "./experiments/../qemu-fallback", "fixture-v1")
        with tempfile.TemporaryDirectory() as outside, tempfile.TemporaryDirectory(dir=repo_root) as inside:
            link = pathlib.Path(inside) / "escape"
            link.symlink_to(outside, target_is_directory=True)
            with self.assertRaisesRegex(ValueError, "escapes repository root"):
                validator.validate(repo_root, f"./{link.relative_to(repo_root)}", "fixture-v1")

    def test_qemu_fixture_validator_accepts_default_and_rejects_path_like_id(self):
        validator = self.load_qemu_fixture_validator()
        repo_root = SCRIPT.parents[1]
        validator.validate(
            repo_root,
            "./experiments/qemu-fallback/file-evaluator",
            "qemu-file-evaluator-v1",
        )
        with self.assertRaisesRegex(ValueError, "evaluator id"):
            validator.validate(repo_root, "./experiments/qemu-fallback/file-evaluator", "nested/id")

    def test_qemu_build_and_benchmark_share_fixture_validator(self):
        repo_root = SCRIPT.parents[1]
        for relative in (
            "experiments/qemu-fallback/build-image.sh",
            "experiments/qemu-fallback/benchmark-ci-file.sh",
        ):
            script = (repo_root / relative).read_text()
            self.assertIn('validate-evaluator-fixture.py', script)

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

    def test_exact_python_strategy_lanes_are_declared(self):
        expected = {
            "python-agent-cow": ("snapshot", "linear-memory-cow"),
            "python-agent-memcpy": ("snapshot", "linear-memory-memcpy"),
            "python-agent-single-use": ("single-use", "single-use-prepared"),
            "python-agent-fresh": ("fresh", "fresh-module"),
        }
        for lane_name, (lifecycle, reset_mode) in expected.items():
            lane = module.LANES[lane_name]
            self.assertEqual(lane.family, "python")
            self.assertEqual(lane.lifecycle_class, "clean")
            self.assertEqual(
                module.AGENT_LIFECYCLE_EXPECTATIONS[lane_name]["lifecycle"],
                lifecycle,
            )
            self.assertEqual(
                module.AGENT_LIFECYCLE_EXPECTATIONS[lane_name]["reset_mode"],
                reset_mode,
            )

    def test_agent_strategy_images_compose_and_workflow_are_wired(self):
        repo_root = SCRIPT.parents[1]
        dockerfile = (repo_root / "demo" / "compose" / "Dockerfile").read_text()
        compose = (
            repo_root / "experiments" / "runtime-comparison-bridge" / "compose.yaml"
        ).read_text()
        workflow = (repo_root / ".github" / "workflows" / "bench-runtime-lanes.yml").read_text()
        for suffix in ("cow", "memcpy", "single-use", "fresh"):
            target = f"bridge-python-agent-{suffix}"
            self.assertIn(f"AS {target}", dockerfile)
            self.assertIn(f"{target}:", compose)
            self.assertIn(target, workflow)

        bridge_base = dockerfile.split("AS bridge-python-agent-base", 1)[1].split(
            "AS bridge-python-agent-cow", 1
        )[0]
        self.assertNotIn("FUNCTION_WASM_SNAPSHOT_MODE", bridge_base)
        single_use = dockerfile.split("AS bridge-python-agent-single-use", 1)[1].split(
            "AS bridge-python-agent-fresh", 1
        )[0]
        fresh = dockerfile.split("AS bridge-python-agent-fresh", 1)[1].split(
            "AS bridge-python-pyodide-persistent", 1
        )[0]
        self.assertNotIn("FUNCTION_WASM_SNAPSHOT_MODE", single_use)
        self.assertNotIn("FUNCTION_WASM_SNAPSHOT_MODE", fresh)

    def test_single_use_policy_evidence_requires_ready_hit_then_miss(self):
        evidence = module.validate_single_use_policy_evidence(
            {
                "prepared_ready_before_hit": 1,
                "prepared_ready_after_hit": 0,
                "ready_hit_ns": 2_000_000,
                "immediate_miss_ns": 4_000_000_000,
                "refill_ready_wait_ns": 4_100_000_000,
            }
        )
        self.assertEqual(evidence["policy"], "single-use-prepared-hit-and-refill")
        with self.assertRaisesRegex(ValueError, "prepared_ready_after_hit"):
            module.validate_single_use_policy_evidence(
                {
                    "prepared_ready_before_hit": 1,
                    "prepared_ready_after_hit": 1,
                    "ready_hit_ns": 2_000_000,
                    "immediate_miss_ns": 4_000_000_000,
                    "refill_ready_wait_ns": 4_100_000_000,
                }
            )

    def test_agent_lifecycle_healthcheck_uses_lane_specific_contract(self):
        for lane_name, expected in module.AGENT_LIFECYCLE_EXPECTATIONS.items():
            result = {**expected, "prepared_ready": 1, "status": "ok"}
            with mock.patch.object(
                module,
                "request_json",
                return_value={
                    "command": "healthcheck",
                    "result": result,
                },
            ):
                observed = module.agent_lifecycle_evidence(
                    module.LANES[lane_name], "http://test", 1.0
                )
                for key, value in expected.items():
                    self.assertEqual(observed[key], value)

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
