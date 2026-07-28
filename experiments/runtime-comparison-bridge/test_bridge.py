import importlib.util
import io
import json
import pathlib
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parent
EVAL_PATH = ROOT / "python" / "eval.py"
SPEC = importlib.util.spec_from_file_location("runtime_comparison_bridge_eval", EVAL_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load evaluator: {EVAL_PATH}")
module = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = module
SPEC.loader.exec_module(module)


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load module: {path}")
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


class PythonBridgeEvaluatorTests(unittest.TestCase):
    def test_exact_workload_and_state_counter(self):
        first = module.evaluation_function("42", "42", {"iterations": 100_000, "seed": 7})
        second = module.evaluation_function("42", "42", {"iterations": 0, "seed": 7})
        self.assertEqual(first["work_checksum"], "5e7135fac6225d57")
        self.assertEqual(first["guest_invocation_count"], 1)
        self.assertEqual(second["work_checksum"], "0000000000000007")
        self.assertEqual(second["guest_invocation_count"], 2)
        self.assertTrue(first["is_correct"])

    def test_rejects_boolean_and_unbounded_work(self):
        for iterations in (True, -1, module.MAX_ITERATIONS + 1):
            result = module.evaluation_function("42", "42", {"iterations": iterations, "seed": 7})
            self.assertFalse(result["is_correct"])
            self.assertIn("error", result)


class PythonBridgeAdapterTests(unittest.TestCase):
    def payload(self):
        return {
            "response": "42",
            "answer": "42",
            "params": {"iterations": 100_000, "seed": 7},
        }

    def test_file_adapter_round_trip(self):
        adapter = load_module("runtime_bridge_file_adapter", ROOT / "python" / "file_evaluator.py")
        with tempfile.TemporaryDirectory() as directory:
            request = pathlib.Path(directory) / "request.json"
            response = pathlib.Path(directory) / "response.json"
            request.write_text(json.dumps({"command": "eval", "params": self.payload()}))
            adapter.main([str(request), str(response)])
            decoded = json.loads(response.read_text())
        self.assertEqual(decoded["command"], "eval")
        self.assertEqual(decoded["result"]["work_checksum"], "5e7135fac6225d57")
        self.assertEqual(decoded["result"]["guest_invocation_count"], 1)

    def test_rpc_adapter_round_trip_and_persistent_counter(self):
        adapter = load_module("runtime_bridge_rpc_adapter", ROOT / "python" / "rpc_evaluator.py")
        frames = []
        for request_id in (1, 2):
            payload = json.dumps(
                {"jsonrpc": "2.0", "id": request_id, "method": "eval", "params": [self.payload()]},
                separators=(",", ":"),
            ).encode()
            frames.append(f"Content-Length: {len(payload)}\r\n\r\n".encode() + payload)
        output = io.BytesIO()
        adapter.serve(io.BytesIO(b"".join(frames)), output)
        output.seek(0)
        first = adapter.read_frame(output)
        second = adapter.read_frame(output)
        self.assertEqual(first["result"]["guest_invocation_count"], 1)
        self.assertEqual(second["result"]["guest_invocation_count"], 2)
        self.assertEqual(second["result"]["work_checksum"], "5e7135fac6225d57")


if __name__ == "__main__":
    unittest.main()
