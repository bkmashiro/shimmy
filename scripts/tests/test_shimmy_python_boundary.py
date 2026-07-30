from __future__ import annotations

import pathlib
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]


class ShimmyPythonBoundaryTests(unittest.TestCase):
    def test_no_tracked_python_runtime_bundle(self) -> None:
        self.assertFalse((ROOT / "build/python-reactor/artifacts").exists())
        attributes = ROOT / ".gitattributes"
        if attributes.exists():
            self.assertNotIn("filter=lfs", attributes.read_text())

    def test_host_and_benchmark_use_only_shimmy_identity(self) -> None:
        paths = [
            path
            for path in (ROOT / "internal/execution/wasm").glob("shimmy_python*.go")
            if not path.name.endswith("_test.go")
        ]
        paths += [
            path
            for path in (ROOT / "experiments/shimmy-python-ultimate").rglob("*.go")
            if not path.name.endswith("_test.go")
        ]
        combined = "\n".join(path.read_text() for path in paths)
        for forbidden in ("AgentPython", "agent_python", "agent_runtime_v1", "runtime_prepare", "runtime_init"):
            self.assertNotIn(forbidden, combined, forbidden)

    def test_workflows_do_not_fetch_legacy_python_artifacts(self) -> None:
        combined = "\n".join(
            path.read_text() for path in (ROOT / ".github/workflows").glob("*.yml")
        ).lower()
        self.assertNotIn("git lfs pull", combined)
        self.assertNotIn("agent-python-runtime", combined)

    def test_producer_has_no_external_runtime_url(self) -> None:
        producer = ROOT / "build/python-reactor/producer"
        combined = "\n".join(
            path.read_text(errors="replace")
            for path in producer.rglob("*")
            if path.is_file() and "tests" not in path.parts
        ).lower()
        self.assertNotIn("github.com/bkmashiro/agent-python", combined)
        self.assertNotIn("webassembly-language-runtimes/releases", combined)


if __name__ == "__main__":
    unittest.main()
