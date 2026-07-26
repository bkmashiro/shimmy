#!/usr/bin/env python3
"""Benchmark QEMU RPC persistent preboot versus lazy clean-on-borrow in GHA."""

from __future__ import annotations

import argparse
import datetime as dt
import importlib.util
import json
import os
import pathlib
import platform
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
from typing import Any, Dict, List, Sequence


class ManagedServer:
    def __init__(self, name: str, command: Sequence[str], env: Dict[str, str], port: int, log_path: pathlib.Path):
        self.name = name
        self.command = list(command)
        self.env = env
        self.port = port
        self.log_path = log_path
        self.process: subprocess.Popen | None = None
        self.log_handle = None

    def start(self, timeout: float) -> int:
        self.log_path.parent.mkdir(parents=True, exist_ok=True)
        self.log_handle = self.log_path.open("w", encoding="utf-8")
        started = time.perf_counter_ns()
        self.process = subprocess.Popen(
            self.command,
            env=self.env,
            stdout=self.log_handle,
            stderr=subprocess.STDOUT,
            text=True,
            start_new_session=True,
        )
        deadline = time.monotonic() + timeout
        last_error: Exception | None = None
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise RuntimeError(f"{self.name} exited before readiness with code {self.process.returncode}")
            try:
                with urllib.request.urlopen(f"http://127.0.0.1:{self.port}/health", timeout=2) as response:
                    if response.status == 200:
                        return time.perf_counter_ns() - started
            except Exception as exc:
                last_error = exc
            time.sleep(0.1)
        raise TimeoutError(f"{self.name} did not become ready after {timeout}s: {last_error}")

    def stop(self) -> None:
        process = self.process
        self.process = None
        if process is not None and process.poll() is None:
            try:
                os.killpg(process.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                process.wait(timeout=10)
        if self.log_handle is not None:
            self.log_handle.close()
            self.log_handle = None

    def log_tail(self, lines: int = 200) -> str:
        if not self.log_path.exists():
            return ""
        return "\n".join(self.log_path.read_text(errors="replace").splitlines()[-lines:])


def load_benchmark_helper(repo_root: pathlib.Path):
    path = repo_root / "scripts/benchmark-runtime-lanes.py"
    spec = importlib.util.spec_from_file_location("benchmark_runtime_lanes", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load benchmark helper: {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def run(command: Sequence[str], *, cwd: pathlib.Path) -> subprocess.CompletedProcess:
    return subprocess.run(list(command), cwd=cwd, check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def http_json(port: int, command: str, payload: Dict[str, Any], timeout: float) -> Dict[str, Any]:
    request = urllib.request.Request(
        f"http://127.0.0.1:{port}/",
        data=json.dumps(payload, separators=(",", ":")).encode("utf-8"),
        headers={"Content-Type": "application/json", "Command": command},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            body = response.read()
            if response.status != 200:
                raise RuntimeError(f"HTTP {response.status}: {body[:500]!r}")
    except urllib.error.HTTPError as exc:
        body = exc.read()
        raise RuntimeError(f"HTTP {exc.code}: {body[:500]!r}") from exc
    decoded = json.loads(body)
    if not isinstance(decoded, dict):
        raise ValueError("HTTP response must be a JSON object")
    return decoded


def timed_eval(port: int, sequence: int, timeout: float) -> tuple[int, Dict[str, Any]]:
    payload = {
        "response": f"same-{sequence}",
        "answer": f"same-{sequence}",
        "params": {"sequence": sequence},
    }
    started = time.perf_counter_ns()
    response = http_json(port, "eval", payload, timeout)
    return time.perf_counter_ns() - started, response


def measure_requests(port: int, repeated_count: int, timeout: float) -> Dict[str, Any]:
    first_ns, first_response = timed_eval(port, 0, timeout)
    repeated_ns: List[int] = []
    responses = [first_response]
    for sequence in range(1, repeated_count + 1):
        elapsed, response = timed_eval(port, sequence, timeout)
        repeated_ns.append(elapsed)
        responses.append(response)
    return {"first_ns": first_ns, "repeated_ns": repeated_ns, "responses": responses}


def guest_boot_id(port: int, timeout: float) -> str:
    response = http_json(port, "healthcheck", {}, timeout)
    if response.get("command") != "healthcheck":
        raise ValueError(f"unexpected guest health response: {response}")
    result = response.get("result")
    if not isinstance(result, dict):
        raise ValueError(f"guest health response lacks result: {response}")
    boot_id = result.get("boot_id")
    if not isinstance(boot_id, str):
        raise ValueError(f"guest health response lacks boot_id: {response}")
    return boot_id


def timed_guest_boot_id(port: int, timeout: float) -> tuple[int, str]:
    started = time.perf_counter_ns()
    boot_id = guest_boot_id(port, timeout)
    return time.perf_counter_ns() - started, boot_id


def verify_manifest(artifact_dir: pathlib.Path) -> tuple[Dict[str, str], Dict[str, Any]]:
    manifest_path = artifact_dir / "manifest.json"
    manifest = json.loads(manifest_path.read_text())
    import hashlib

    digests = {}
    for key in ("kernel", "initrd", "rootfs"):
        row = manifest[key]
        path = artifact_dir / row["path"]
        actual = hashlib.sha256(path.read_bytes()).hexdigest()
        if actual != row["sha256"]:
            raise ValueError(f"{key} digest mismatch: {actual}")
        digests[key] = actual
    return digests, manifest


def common_environment(evaluator_path: pathlib.Path) -> Dict[str, str]:
    env = os.environ.copy()
    env.update(
        {
            "FUNCTION_INTERFACE": "rpc",
            "FUNCTION_RPC_TRANSPORT": "stdio",
            "FUNCTION_COMMAND": str(evaluator_path),
            "FUNCTION_WORKING_DIR": str(evaluator_path.parent),
            "FUNCTION_MAX_PROCS": "1",
            "FUNCTION_WORKER_SEND_TIMEOUT": "180s",
            "FUNCTION_TIMEOUT": "180s",
            "LOG_LEVEL": "error",
        }
    )
    return env


def qemu_environment(
    base: Dict[str, str],
    *,
    policy: str,
    runner: pathlib.Path,
    qemu_binary: str,
    artifact_dir: pathlib.Path,
) -> Dict[str, str]:
    env = dict(base)
    env.update(
        {
            "FUNCTION_QEMU_ENABLED": "true",
            "FUNCTION_QEMU_RESET_POLICY": policy,
            "FUNCTION_QEMU_RUNNER": str(runner),
            "FUNCTION_QEMU_BINARY": qemu_binary,
            "FUNCTION_QEMU_ROOTFS": str(artifact_dir / "evaluator.squashfs"),
            "FUNCTION_QEMU_IMAGE_MANIFEST": str(artifact_dir / "manifest.json"),
            "FUNCTION_QEMU_ACCELERATOR": "tcg",
            "FUNCTION_QEMU_MEMORY_MB": "512",
            "FUNCTION_QEMU_VCPUS": "1",
            "FUNCTION_QEMU_NETWORK_PROFILE": "none",
            "FUNCTION_QEMU_BOOT_TIMEOUT": "150s",
            "FUNCTION_QEMU_SHUTDOWN_TIMEOUT": "20s",
        }
    )
    return env


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo-root", default=str(pathlib.Path(__file__).resolve().parents[2]))
    parser.add_argument("--artifact-dir", required=True)
    parser.add_argument("--bin-dir", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--repeated-count", type=int, default=3)
    parser.add_argument("--native-port", type=int, default=18480)
    parser.add_argument("--persistent-port", type=int, default=18481)
    parser.add_argument("--lazy-port", type=int, default=18482)
    args = parser.parse_args(argv)
    if args.repeated_count < 1 or args.repeated_count > 10:
        parser.error("--repeated-count must be in [1,10]")
    return args


def main(argv: Sequence[str] | None = None) -> int:
    if os.getenv("GITHUB_ACTIONS") != "true":
        print("refusing QEMU RPC prewarm benchmark outside GitHub Actions", file=sys.stderr)
        return 1
    args = parse_args(argv)
    repo_root = pathlib.Path(args.repo_root).resolve()
    artifact_dir = pathlib.Path(args.artifact_dir).resolve()
    bin_dir = pathlib.Path(args.bin_dir).resolve()
    output = pathlib.Path(args.output).resolve()
    bin_dir.mkdir(parents=True, exist_ok=True)
    output.parent.mkdir(parents=True, exist_ok=True)

    helper = load_benchmark_helper(repo_root)
    digests, _ = verify_manifest(artifact_dir)
    qemu_binary = shutil.which("qemu-system-x86_64")
    if qemu_binary is None:
        raise RuntimeError("qemu-system-x86_64 is unavailable")

    shimmy = bin_dir / "shimmy"
    qemu_runner = bin_dir / "shimmy-qemu-runner"
    rpc_evaluator = bin_dir / "rpc-evaluator"
    run(["go", "build", "-trimpath", "-buildvcs=false", "-o", str(shimmy), "."], cwd=repo_root)
    run(
        ["go", "build", "-trimpath", "-buildvcs=false", "-o", str(qemu_runner), "./cmd/shimmy-qemu-runner"],
        cwd=repo_root,
    )
    run(
        ["go", "build", "-trimpath", "-buildvcs=false", "-o", str(rpc_evaluator), "./experiments/qemu-fallback/rpc-evaluator"],
        cwd=repo_root,
    )
    evaluator_dir = pathlib.Path("/opt/evaluator")
    evaluator_path = evaluator_dir / "rpc-evaluator"
    run(["sudo", "mkdir", "-p", str(evaluator_dir)], cwd=repo_root)
    run(["sudo", "install", "-m", "0755", str(rpc_evaluator), str(evaluator_path)], cwd=repo_root)

    base_env = common_environment(evaluator_path)
    native = ManagedServer(
        "native-rpc",
        [str(shimmy), "serve", "--port", str(args.native_port)],
        base_env,
        args.native_port,
        bin_dir / "native-rpc.log",
    )
    active_qemu: ManagedServer | None = None
    started_at = dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")
    servers = [native]
    try:
        native_liveness = native.start(30)
        native_prewarm_ns, _ = timed_guest_boot_id(args.native_port, 30)
        native_measurement = measure_requests(args.native_port, args.repeated_count, 30)
        native_measurement["liveness_ns"] = native_liveness
        native_measurement["prewarm_probe_ns"] = native_prewarm_ns

        policies: Dict[str, Dict[str, Any]] = {}
        for policy, port in (("off", args.persistent_port), ("lazy", args.lazy_port)):
            active_qemu = ManagedServer(
                f"qemu-rpc-{policy}",
                [str(shimmy), "serve", "--port", str(port)],
                qemu_environment(
                    base_env,
                    policy=policy,
                    runner=qemu_runner,
                    qemu_binary=qemu_binary,
                    artifact_dir=artifact_dir,
                ),
                port,
                bin_dir / f"qemu-rpc-{policy}.log",
            )
            servers.append(active_qemu)
            liveness_ns = active_qemu.start(180)
            prewarm_ns, prewarm_boot_id = timed_guest_boot_id(port, 180)
            measurement = measure_requests(port, args.repeated_count, 180)
            measurement["liveness_ns"] = liveness_ns
            measurement["prewarm_probe_ns"] = prewarm_ns
            measurement["boot_ids"] = [prewarm_boot_id, guest_boot_id(port, 180)]
            policies[policy] = measurement
            print(
                json.dumps(
                    {
                        "policy": policy,
                        "http_liveness_ns": liveness_ns,
                        "prewarm_probe_ns": prewarm_ns,
                        "first_request_after_prewarm_ns": measurement["first_ns"],
                        "repeated_ns": measurement["repeated_ns"],
                        "boot_ids": measurement["boot_ids"],
                    },
                    sort_keys=True,
                ),
                flush=True,
            )
            active_qemu.stop()
            active_qemu = None

        report = helper.build_qemu_rpc_prewarm_report(
            native=native_measurement,
            persistent=policies["off"],
            lazy=policies["lazy"],
            manifest_digests=digests,
            qemu_version=run([qemu_binary, "--version"], cwd=repo_root).stdout.splitlines()[0],
            source_commit=run(["git", "rev-parse", "HEAD"], cwd=repo_root).stdout.strip(),
        )
        report.update(
            {
                "started_at": started_at,
                "completed_at": dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z"),
                "workflow_run_id": os.getenv("GITHUB_RUN_ID"),
                "environment": {
                    "os": platform.platform(),
                    "machine": platform.machine(),
                    "logical_cpus": os.cpu_count(),
                    "cpu": helper.cpu_model(),
                },
                "method": {
                    "http_health_role": "process liveness only",
                    "guest_prewarm_probe_count": 1,
                    "first_request_count": 1,
                    "repeated_request_count": args.repeated_count,
                    "boot_identity_probes": "one prewarm probe and one post-request probe",
                },
            }
        )
        output.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
        print(json.dumps(report, sort_keys=True))
        return 0
    except Exception:
        for server in servers:
            tail = server.log_tail()
            if tail:
                print(f"--- {server.name} log ---\n{tail}", file=sys.stderr)
        raise
    finally:
        if active_qemu is not None:
            active_qemu.stop()
        native.stop()


if __name__ == "__main__":
    raise SystemExit(main())
