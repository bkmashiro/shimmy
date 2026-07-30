from __future__ import annotations

import os
import pathlib
import subprocess
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
LAUNCHER = ROOT / "scripts" / "benchmark-shimmy-python-ultimate.sh"
WORKFLOWS = ROOT / ".github" / "workflows"


class ShimmyPythonUltimateManualOnlyTests(unittest.TestCase):
    def test_launcher_refuses_ci_environment(self) -> None:
        env = os.environ.copy()
        env["CI"] = "true"
        result = subprocess.run(
            [str(LAUNCHER), "--help"],
            cwd=ROOT,
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("manual-only", result.stderr)

    def test_no_workflow_references_ultimate_benchmark(self) -> None:
        references: list[str] = []
        for path in sorted(WORKFLOWS.glob("*.y*ml")):
            text = path.read_text(encoding="utf-8")
            if "benchmark-shimmy-python-ultimate" in text or "shimmy-python-ultimate" in text:
                references.append(str(path.relative_to(ROOT)))
        self.assertEqual(references, [])


if __name__ == "__main__":
    unittest.main()
