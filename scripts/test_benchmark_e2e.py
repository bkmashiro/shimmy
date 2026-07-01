#!/usr/bin/env python3
"""Unit tests for the cross-runtime benchmark matrix."""

from __future__ import annotations

import importlib.util
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "benchmark-e2e.py"


def load_benchmark_module():
    spec = importlib.util.spec_from_file_location("benchmark_e2e", SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"could not load {SCRIPT}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class BenchmarkMatrixTests(unittest.TestCase):
    def test_ci_profile_covers_python_paths_generic_wasm_and_snapshot_off(self):
        bench = load_benchmark_module()

        cases = bench.select_cases(profile="ci", runtime_filters=None, case_filters=None)
        keys = {(case.runtime.id, case.payload.size, case.payload.command, case.snapshot) for case in cases}

        self.assertIn(("python-file-env", "light", "eval", "none"), keys)
        self.assertIn(("python-file-env", "heavy", "eval", "none"), keys)
        self.assertIn(("python-file-request", "light", "eval", "none"), keys)
        self.assertIn(("python-file-request", "heavy", "eval", "none"), keys)
        self.assertIn(("generic-wasm-go", "light", "eval", "full"), keys)
        self.assertIn(("generic-wasm-go", "heavy", "eval", "full"), keys)
        self.assertIn(("generic-wasm-go", "light", "eval", "off"), keys)

        preview_runtimes = {case.runtime.id for case in cases if case.payload.command == "preview"}
        self.assertEqual({"python-file-env", "python-file-request", "generic-wasm-go"}, preview_runtimes)

    def test_deep_profile_adds_medium_and_uffd_placeholder_without_enabling_ci(self):
        bench = load_benchmark_module()

        ci = bench.select_cases(profile="ci", runtime_filters=None, case_filters=None)
        deep = bench.select_cases(profile="deep", runtime_filters=None, case_filters=None)

        self.assertFalse(any(case.payload.size == "medium" for case in ci))
        self.assertTrue(any(case.payload.size == "medium" for case in deep))
        self.assertTrue(any(case.snapshot == "uffd" and case.skip_reason for case in deep))

    def test_unknown_runtime_filter_fails_fast(self):
        bench = load_benchmark_module()

        with self.assertRaises(SystemExit):
            bench.select_cases(profile="ci", runtime_filters={"missing-runtime"}, case_filters=None)


if __name__ == "__main__":
    unittest.main()
