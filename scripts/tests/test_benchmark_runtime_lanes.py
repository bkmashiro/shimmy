import importlib.util
import json
import pathlib
import sys
import unittest
from unittest import mock


SCRIPT = pathlib.Path(__file__).parents[1] / "benchmark-runtime-lanes.py"
SPEC = importlib.util.spec_from_file_location("benchmark_runtime_lanes", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load benchmark script: {SCRIPT}")
module = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = module
SPEC.loader.exec_module(module)


class SummaryTests(unittest.TestCase):
    def test_summary_uses_median_and_nearest_rank_p95(self):
        summary = module.summarize_ns(list(range(1, 21)))
        self.assertEqual(summary["sample_count"], 20)
        self.assertEqual(summary["median_ns"], 10.5)
        self.assertEqual(summary["p95_ns"], 19)
        self.assertEqual(summary["min_ns"], 1)
        self.assertEqual(summary["max_ns"], 20)

    def test_summary_rejects_empty_samples(self):
        with self.assertRaisesRegex(ValueError, "at least one sample"):
            module.summarize_ns([])


class LaneAssertionTests(unittest.TestCase):
    def test_generic_requires_reset_evidence(self):
        module.assert_lane_response(
            "generic",
            {"result": {"is_correct": True, "guest_invocation_count": 1, "snapshot_isolation_ok": True}},
        )
        with self.assertRaisesRegex(ValueError, "snapshot_isolation_ok"):
            module.assert_lane_response(
                "generic",
                {"result": {"is_correct": True, "guest_invocation_count": 1, "snapshot_isolation_ok": False}},
            )

    def test_reactor_consumer_requires_correct_result(self):
        module.assert_lane_response("reactor-consumer", {"result": {"is_correct": True}})
        with self.assertRaisesRegex(ValueError, "is_correct"):
            module.assert_lane_response("reactor-consumer", {"result": {"is_correct": False}})

    def test_pyodide_requires_scipy_result_shape(self):
        module.assert_lane_response(
            "pyodide-scipy",
            {"result": {"is_correct": True, "n_samples": 5, "p_value": 0.99}},
        )
        with self.assertRaisesRegex(ValueError, "p_value"):
            module.assert_lane_response(
                "pyodide-scipy",
                {"result": {"is_correct": True, "n_samples": 5, "p_value": "0.99"}},
            )

    def test_dbi_requires_correct_result(self):
        module.assert_lane_response("dbi-lean", {"result": {"is_correct": True}})
        with self.assertRaisesRegex(ValueError, "is_correct"):
            module.assert_lane_response("dbi-lean", {"result": {"is_correct": False}})

    def test_unknown_lane_fails_closed(self):
        with self.assertRaisesRegex(ValueError, "unknown lane"):
            module.assert_lane_response("mystery", {"result": {"is_correct": True}})


class OrchestrationTests(unittest.TestCase):
    def test_compose_starts_service_before_inspecting_its_image(self):
        commands = []

        def fake_run(command, **_kwargs):
            command = list(command)
            commands.append(command)
            if command[:3] == ["docker", "image", "inspect"]:
                return module.subprocess.CompletedProcess(
                    command,
                    0,
                    stdout=json.dumps([{"Id": "sha256:abc", "Size": 123, "RepoDigests": []}]),
                    stderr="",
                )
            if "images" in command:
                return module.subprocess.CompletedProcess(command, 0, stdout="sha256:abc\n", stderr="")
            return module.subprocess.CompletedProcess(command, 0, stdout="", stderr="")

        response = {"result": {"is_correct": True, "guest_invocation_count": 1, "snapshot_isolation_ok": True}}
        with mock.patch.object(module, "run", side_effect=fake_run), mock.patch.object(
            module, "wait_for_health"
        ), mock.patch.object(module, "timed_request", return_value=(100, response)):
            module.benchmark_lane(
                "generic",
                module.LANES["generic"],
                ["docker", "compose", "-f", "compose.yaml"],
                warmups=0,
                samples=1,
                ready_timeout=1,
                request_timeout=1,
            )

        up_index = next(index for index, command in enumerate(commands) if "up" in command)
        images_index = next(index for index, command in enumerate(commands) if "images" in command)
        self.assertLess(up_index, images_index)


class QEMUReportTests(unittest.TestCase):
    def test_qemu_report_requires_response_parity_and_separates_native(self):
        response = {"command": "eval", "result": {"is_correct": True, "echo": {"x": 1}}}
        report = module.build_qemu_report(
            native_samples_ns=[1_000, 2_000, 3_000],
            qemu_samples_ns=[30_000, 31_000, 32_000],
            native_responses=[response, response, response],
            qemu_responses=[response, response, response],
            manifest_digests={"kernel": "a" * 64, "initrd": "b" * 64, "rootfs": "c" * 64},
            qemu_version="QEMU emulator version 10.0",
            source_commit="d" * 40,
        )
        self.assertTrue(report["response_parity"])
        self.assertEqual(report["accelerator"], "tcg")
        self.assertEqual(report["lifecycle"], "fresh QEMU VM per file-interface request")
        self.assertEqual(report["native"]["median_ns"], 2_000)
        self.assertEqual(report["qemu"]["median_ns"], 31_000)

    def test_qemu_report_rejects_response_mismatch(self):
        with self.assertRaisesRegex(ValueError, "response mismatch"):
            module.build_qemu_report(
                native_samples_ns=[1],
                qemu_samples_ns=[2],
                native_responses=[{"result": {"is_correct": True}}],
                qemu_responses=[{"result": {"is_correct": False}}],
                manifest_digests={"kernel": "a" * 64, "initrd": "b" * 64, "rootfs": "c" * 64},
                qemu_version="QEMU emulator version 10.0",
                source_commit="d" * 40,
            )


class QEMURPCPrewarmReportTests(unittest.TestCase):
    def response(self, sequence):
        return {
            "command": "eval",
            "result": {"is_correct": True, "echo": {"params": {"sequence": sequence}}},
        }

    def test_report_proves_prewarm_effectiveness_by_boot_identity(self):
        responses = [self.response(index) for index in range(4)]
        report = module.build_qemu_rpc_prewarm_report(
            native={
                "liveness_ns": 1,
                "prewarm_probe_ns": 2,
                "first_ns": 3,
                "repeated_ns": [4, 5, 6],
                "responses": responses,
            },
            persistent={
                "liveness_ns": 100,
                "prewarm_probe_ns": 1000,
                "first_ns": 6,
                "repeated_ns": [7, 8, 9],
                "responses": responses,
                "boot_ids": ["boot-a", "boot-a"],
            },
            lazy={
                "liveness_ns": 10,
                "prewarm_probe_ns": 1100,
                "first_ns": 1200,
                "repeated_ns": [1300, 1400, 1500],
                "responses": responses,
                "boot_ids": ["boot-b", "boot-c"],
            },
            manifest_digests={"kernel": "a" * 64, "initrd": "b" * 64, "rootfs": "c" * 64},
            qemu_version="QEMU emulator version 10.0",
            source_commit="d" * 40,
        )
        self.assertEqual(report["policies"]["off"]["liveness_plus_prewarm_plus_first_ns"], 1106)
        self.assertEqual(report["policies"]["off"]["repeated_requests"]["median_ns"], 8)
        self.assertTrue(report["policies"]["off"]["prewarm_effective"])
        self.assertFalse(report["policies"]["lazy"]["prewarm_effective"])
        self.assertEqual(report["unsupported_policies"]["eager"]["status"], "rejected")
        self.assertTrue(report["response_parity"])

    def test_report_rejects_policy_response_mismatch(self):
        responses = [self.response(index) for index in range(4)]
        mismatch = list(responses)
        mismatch[2] = self.response(99)
        base = {
            "liveness_ns": 1,
            "prewarm_probe_ns": 2,
            "first_ns": 3,
            "repeated_ns": [4, 5, 6],
            "responses": responses,
        }
        with self.assertRaisesRegex(ValueError, "response mismatch"):
            module.build_qemu_rpc_prewarm_report(
                native=base,
                persistent={**base, "responses": mismatch, "boot_ids": ["boot-a", "boot-a"]},
                lazy={**base, "boot_ids": ["boot-b", "boot-c"]},
                manifest_digests={"kernel": "a" * 64, "initrd": "b" * 64, "rootfs": "c" * 64},
                qemu_version="QEMU emulator version 10.0",
                source_commit="d" * 40,
            )

    def test_report_rejects_wrong_boot_lifecycle(self):
        responses = [self.response(index) for index in range(4)]
        base = {
            "liveness_ns": 1,
            "prewarm_probe_ns": 2,
            "first_ns": 3,
            "repeated_ns": [4, 5, 6],
            "responses": responses,
        }
        with self.assertRaisesRegex(ValueError, "persistent boot IDs"):
            module.build_qemu_rpc_prewarm_report(
                native=base,
                persistent={**base, "boot_ids": ["boot-a", "boot-b"]},
                lazy={**base, "boot_ids": ["boot-c", "boot-d"]},
                manifest_digests={"kernel": "a" * 64, "initrd": "b" * 64, "rootfs": "c" * 64},
                qemu_version="QEMU emulator version 10.0",
                source_commit="d" * 40,
            )


class ReportContractTests(unittest.TestCase):
    def test_lane_report_separates_ready_first_and_steady(self):
        report = module.build_lane_report(
            lane="generic",
            workload="stateful echo",
            lifecycle="persistent server; pooled WASM instance reset after every request",
            initialization_placement="module compiled before readiness",
            ready_ns=1_000,
            first_ns=2_000,
            steady_samples_ns=[3_000, 4_000, 5_000],
            warmup_count=2,
            response={"result": {"is_correct": True, "guest_invocation_count": 1, "snapshot_isolation_ok": True}},
            image={"id": "sha256:abc", "size_bytes": 123},
        )
        self.assertEqual(report["server_ready_ns"], 1_000)
        self.assertEqual(report["first_request_ns"], 2_000)
        self.assertEqual(report["ready_plus_first_ns"], 3_000)
        self.assertEqual(report["steady"]["sample_count"], 3)
        self.assertEqual(report["warmup_count"], 2)
        self.assertEqual(len(report["response_sha256"]), 64)
        self.assertNotIn("response", report)

    def test_report_is_json_serializable(self):
        report = module.build_lane_report(
            lane="reactor-consumer",
            workload="numeric tolerance",
            lifecycle="fresh guest instance per request",
            initialization_placement="artifact verified before readiness",
            ready_ns=1,
            first_ns=2,
            steady_samples_ns=[3],
            warmup_count=0,
            response={"result": {"is_correct": True}},
            image={"id": "sha256:def", "size_bytes": 456},
        )
        json.dumps(report, sort_keys=True)


if __name__ == "__main__":
    unittest.main()
