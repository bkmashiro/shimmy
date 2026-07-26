import importlib.util
import pathlib
import sys
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "experiments" / "qemu-fallback" / "benchmark-ci-rpc-prewarm.py"
SPEC = importlib.util.spec_from_file_location("benchmark_ci_rpc_prewarm", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {SCRIPT}")
module = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = module
SPEC.loader.exec_module(module)


class EagerMeasurementTests(unittest.TestCase):
    def test_eager_measurement_waits_for_prepared_hits_but_not_immediate_next(self):
        sleeps = []
        calls = []

        def sleeper(seconds):
            sleeps.append(seconds)

        def evaluate(port, sequence, timeout):
            calls.append((port, sequence, timeout, len(sleeps)))
            return sequence + 1, {"command": "eval", "result": {"is_correct": True, "sequence": sequence}}

        result = module.measure_eager_requests(
            18483,
            repeated_count=3,
            timeout=180,
            interarrival_seconds=30,
            sleeper=sleeper,
            evaluate=evaluate,
        )

        self.assertEqual(sleeps, [30, 30, 30, 30])
        self.assertEqual([call[3] for call in calls], [1, 1, 2, 3, 4])
        self.assertEqual(result["first_ready_ns"], 1)
        self.assertEqual(result["immediate_next_ns"], 2)
        self.assertEqual(result["later_ready_ns"], [3, 4, 5])
        self.assertEqual(result["startup_interarrival_ns"], 30_000_000_000)
        self.assertEqual(result["later_interarrival_ns"], 30_000_000_000)
        self.assertEqual(len(result["responses"]), 5)


if __name__ == "__main__":
    unittest.main()
