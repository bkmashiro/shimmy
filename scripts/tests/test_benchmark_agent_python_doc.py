from __future__ import annotations

import pathlib
import subprocess
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
LAUNCHER = ROOT / "scripts" / "benchmark-agent-python-doc.sh"
JOB_SCRIPT = ROOT / "experiments" / "agent-python-ultimate" / "slurm" / "job.sh"


class AgentPythonDoCLauncherTests(unittest.TestCase):
    def run_launcher(self, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [str(LAUNCHER), *args],
            cwd=ROOT,
            text=True,
            capture_output=True,
            check=False,
        )

    def test_validate_run_id_rejects_path_like_values(self) -> None:
        for value in ("", "/tmp", "../escape", "a/b", "UPPERCASE", "short"):
            with self.subTest(value=value):
                result = self.run_launcher("validate-run-id", value)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("invalid run id", result.stderr)

    def test_validate_run_id_accepts_generated_shape(self) -> None:
        result = self.run_launcher(
            "validate-run-id", "agent-python-20260727t001500z-a1b2c3d4"
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_render_sbatch_is_manual_fixed_host_and_bounded(self) -> None:
        result = self.run_launcher(
            "render-sbatch", "agent-python-20260727t001500z-a1b2c3d4"
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        rendered = result.stdout
        for required in (
            "--partition=a16",
            "--nodelist=gpuvm36",
            "--nodes=1",
            "--ntasks=1",
            "--cpus-per-task=6",
            "--mem=48G",
            "--gres=gpu:nvidia_a16:1",
            "--time=2-12:00:00",
            "--export=NIL",
            "--chdir=/tmp",
            "--output=/tmp/shimmy-agent-python-%j-slurm.out",
        ):
            self.assertIn(required, rendered)
        self.assertNotIn("--exclusive", rendered)
        self.assertNotIn(".github", rendered)
        launcher_text = LAUNCHER.read_text(encoding="utf-8")
        self.assertIn('sbcast -v --force --jobid="$job_id.batch"', launcher_text)
        self.assertIn("sbcast did not confirm the batch step credential", launcher_text)
        stage_body = launcher_text.split("stage_job() {", 1)[1].split("job_status() {", 1)[0]
        self.assertNotIn('test -d "/tmp/shimmy-agent-python-$job_id"', stage_body)
        self.assertIn("sleep 5\nbroadcast_output=", stage_body)

    def test_slurm_job_uses_tmp_checksum_pull_ack_protocol(self) -> None:
        text = JOB_SCRIPT.read_text(encoding="utf-8")
        for required in (
            "umask 077",
            "SLURM_JOB_ID",
            "input.tar.zst",
            "sha256sum -c",
            "RESULT.READY",
            "ACK",
            'rm -rf -- "$run_root"',
        ):
            self.assertIn(required, text)
        self.assertIn('/tmp/shimmy-agent-python-', text)
        self.assertIn("input checksum file must contain exactly", text)
        self.assertIn('$checksum_name" != "input.tar.zst', text)
        self.assertIn("trap cleanup_run_root EXIT", text)
        self.assertIn('wait_for_file "$run_root/ACK" 172800', text)
        self.assertNotIn("/vol/bitbucket", text)


if __name__ == "__main__":
    unittest.main()
