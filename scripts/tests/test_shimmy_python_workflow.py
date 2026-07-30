from __future__ import annotations

import pathlib
import re
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "build.yml"


class ShimmyPythonWorkflowTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.text = WORKFLOW.read_text()

    def test_manual_mode_is_explicit_and_standard_is_default(self) -> None:
        prefix = self.text.split("jobs:", 1)[0]
        self.assertIn("workflow_dispatch:", prefix)
        self.assertRegex(prefix, r"mode:\s*\n\s+description:")
        self.assertIn("default: standard", prefix)
        self.assertIn("- shimmy-python", prefix)

    def test_artifact_jobs_are_manual_mode_only(self) -> None:
        for job in ("build-shimmy-python", "test-shimmy-python"):
            match = re.search(
                rf"^  {job}:\n(?P<body>.*?)(?=^  [a-zA-Z0-9_-]+:|\Z)",
                self.text,
                re.MULTILINE | re.DOTALL,
            )
            if match is None:
                self.fail(f"missing workflow job {job}")
            body = match.group("body")
            self.assertIn("github.event_name == 'workflow_dispatch'", body)
            self.assertIn("inputs.mode == 'shimmy-python'", body)

    def test_real_artifact_is_uploaded_then_downloaded_for_host_e2e(self) -> None:
        self.assertIn("build/python-reactor/producer/build/build-base.sh", self.text)
        self.assertIn("actions/upload-artifact@v7", self.text)
        self.assertIn("retention-days: 7", self.text)
        self.assertIn("actions/download-artifact@v8", self.text)
        self.assertIn("sha256sum -c SHA256SUMS", self.text)
        self.assertIn("SHIMMY_PYTHON_RUNTIME_ARTIFACT", self.text)
        self.assertIn("TestShimmyPythonArtifactE2E", self.text)

    def test_no_cross_project_or_release_path(self) -> None:
        lowered = self.text.lower()
        self.assertNotIn("agent-python-runtime", lowered)
        self.assertNotIn("webassembly-language-runtimes", lowered)
        self.assertNotIn("git lfs pull", lowered)
        if "  build-shimmy-python:" not in self.text:
            self.fail("missing workflow job build-shimmy-python")
        artifact_jobs = self.text.split("  build-shimmy-python:", 1)[1]
        self.assertNotIn("release", artifact_jobs.lower())
        self.assertNotRegex(artifact_jobs, r"checkout@[^\n]+\n(?:.*\n){0,8}\s+repository:")


if __name__ == "__main__":
    unittest.main()
