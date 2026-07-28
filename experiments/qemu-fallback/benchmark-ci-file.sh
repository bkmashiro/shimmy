#!/usr/bin/env bash
set -euo pipefail

if [[ "${GITHUB_ACTIONS:-}" != "true" ]]; then
  echo "refusing QEMU CI benchmark outside GitHub Actions" >&2
  exit 1
fi

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/../.." && pwd)
ARTIFACT_DIR=${ARTIFACT_DIR:-"$REPO_ROOT/artifacts/qemu-fallback"}
BIN_DIR=${BIN_DIR:-"${RUNNER_TEMP:-/tmp}/qemu-file-benchmark"}
OUTPUT=${OUTPUT:-"$REPO_ROOT/artifacts/runtime-lane-bench/qemu-file.json"}
REQUEST_COUNT=${REQUEST_COUNT:-3}
NATIVE_PORT=${NATIVE_PORT:-18380}
QEMU_PORT=${QEMU_PORT:-18381}
EVALUATOR_DIR=/opt/evaluator
EVALUATOR_PATH="$EVALUATOR_DIR/file-evaluator"
FILE_EVALUATOR_PACKAGE=${FILE_EVALUATOR_PACKAGE:-./experiments/qemu-fallback/file-evaluator}
FILE_EVALUATOR_ID=${FILE_EVALUATOR_ID:-qemu-file-evaluator-v1}

if [[ ! "$REQUEST_COUNT" =~ ^[1-9][0-9]*$ || "$REQUEST_COUNT" -gt 10 ]]; then
  echo "REQUEST_COUNT must be in [1,10]" >&2
  exit 1
fi
python3 "$SCRIPT_DIR/validate-evaluator-fixture.py" \
  "$REPO_ROOT" "$FILE_EVALUATOR_PACKAGE" "$FILE_EVALUATOR_ID"

mkdir -p "$BIN_DIR" "$(dirname "$OUTPUT")"
go build -trimpath -buildvcs=false -o "$BIN_DIR/shimmy" .
go build -trimpath -buildvcs=false -o "$BIN_DIR/shimmy-qemu-runner" ./cmd/shimmy-qemu-runner
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false \
  -ldflags='-s -w -buildid=' -o "$BIN_DIR/file-evaluator" "$FILE_EVALUATOR_PACKAGE"
sudo mkdir -p "$EVALUATOR_DIR"
sudo install -m 0755 "$BIN_DIR/file-evaluator" "$EVALUATOR_PATH"

for path in "$ARTIFACT_DIR/manifest.json" "$ARTIFACT_DIR/file-evaluator-manifest.json" "$ARTIFACT_DIR/vmlinuz" "$ARTIFACT_DIR/initramfs.img" "$ARTIFACT_DIR/evaluator.squashfs"; do
  if [[ ! -f "$path" ]]; then
    printf 'missing QEMU benchmark artifact: %s\n' "$path" >&2
    exit 1
  fi
done
QEMU_BINARY=$(command -v qemu-system-x86_64)

python3 - "$ARTIFACT_DIR/manifest.json" "$ARTIFACT_DIR" <<'PY'
import hashlib
import json
import pathlib
import sys
manifest = json.loads(pathlib.Path(sys.argv[1]).read_text())
root = pathlib.Path(sys.argv[2])
for key in ("kernel", "initrd", "rootfs"):
    path = root / manifest[key]["path"]
    actual = hashlib.sha256(path.read_bytes()).hexdigest()
    if actual != manifest[key]["sha256"]:
        raise SystemExit(f"{key} digest mismatch: {actual}")
PY

native_log="$BIN_DIR/native.log"
qemu_log="$BIN_DIR/qemu.log"
native_pid=""
qemu_pid=""
cleanup() {
  for pid in "$native_pid" "$qemu_pid"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
}
trap cleanup EXIT

common_env=(
  FUNCTION_INTERFACE=file
  FUNCTION_COMMAND="$EVALUATOR_PATH"
  FUNCTION_WORKING_DIR="$EVALUATOR_DIR"
  FUNCTION_MAX_PROCS=1
  FUNCTION_WORKER_SEND_TIMEOUT=180s
  FUNCTION_TIMEOUT=180s
  LOG_LEVEL=error
)

env "${common_env[@]}" "$BIN_DIR/shimmy" serve --port "$NATIVE_PORT" >"$native_log" 2>&1 &
native_pid=$!
env "${common_env[@]}" \
  FUNCTION_QEMU_ENABLED=true \
  FUNCTION_QEMU_RUNNER="$BIN_DIR/shimmy-qemu-runner" \
  FUNCTION_QEMU_BINARY="$QEMU_BINARY" \
  FUNCTION_QEMU_ROOTFS="$ARTIFACT_DIR/evaluator.squashfs" \
  FUNCTION_QEMU_IMAGE_MANIFEST="$ARTIFACT_DIR/manifest.json" \
  FUNCTION_QEMU_ACCELERATOR=tcg \
  FUNCTION_QEMU_MEMORY_MB=512 \
  FUNCTION_QEMU_VCPUS=1 \
  FUNCTION_QEMU_NETWORK_PROFILE=none \
  FUNCTION_QEMU_BOOT_TIMEOUT=150s \
  FUNCTION_QEMU_SHUTDOWN_TIMEOUT=20s \
  "$BIN_DIR/shimmy" serve --port "$QEMU_PORT" >"$qemu_log" 2>&1 &
qemu_pid=$!

wait_for_health() {
  local port=$1 pid=$2 log=$3
  for _ in $(seq 1 600); do
    if ! kill -0 "$pid" 2>/dev/null; then
      printf 'Shimmy on port %s exited early\n' "$port" >&2
      tail -200 "$log" >&2
      return 1
    fi
    if curl --silent --fail "http://127.0.0.1:${port}/health" >/dev/null; then
      return 0
    fi
    sleep 0.1
  done
  printf 'Shimmy on port %s did not become ready\n' "$port" >&2
  tail -200 "$log" >&2
  return 1
}
wait_for_health "$NATIVE_PORT" "$native_pid" "$native_log"
wait_for_health "$QEMU_PORT" "$qemu_pid" "$qemu_log"

REPO_ROOT="$REPO_ROOT" \
ARTIFACT_DIR="$ARTIFACT_DIR" \
BIN_DIR="$BIN_DIR" \
OUTPUT="$OUTPUT" \
REQUEST_COUNT="$REQUEST_COUNT" \
NATIVE_PORT="$NATIVE_PORT" \
QEMU_PORT="$QEMU_PORT" \
QEMU_BINARY="$QEMU_BINARY" \
FILE_EVALUATOR_PACKAGE="$FILE_EVALUATOR_PACKAGE" \
FILE_EVALUATOR_ID="$FILE_EVALUATOR_ID" \
python3 - <<'PY'
import datetime as dt
import hashlib
import importlib.util
import json
import os
import pathlib
import platform
import subprocess
import sys
import time
import urllib.request

root = pathlib.Path(os.environ["REPO_ROOT"])
spec = importlib.util.spec_from_file_location("benchmark_runtime_lanes", root / "scripts/benchmark-runtime-lanes.py")
if spec is None or spec.loader is None:
    raise SystemExit("cannot load benchmark helper")
module = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = module
spec.loader.exec_module(module)

payload = {"response": "same", "answer": "same", "params": {"source": "gha-runtime-lane-benchmark"}}
headers = {"Content-Type": "application/json", "Command": "eval"}

def request(port):
    req = urllib.request.Request(
        f"http://127.0.0.1:{port}/",
        data=json.dumps(payload, separators=(",", ":")).encode(),
        headers=headers,
        method="POST",
    )
    started = time.perf_counter_ns()
    with urllib.request.urlopen(req, timeout=180) as response:
        body = response.read()
        if response.status != 200:
            raise SystemExit(f"HTTP {response.status}: {body[:500]!r}")
    return time.perf_counter_ns() - started, json.loads(body)

count = int(os.environ["REQUEST_COUNT"])
native_times = []
qemu_times = []
native_responses = []
qemu_responses = []
for index in range(count):
    elapsed, response = request(int(os.environ["NATIVE_PORT"]))
    native_times.append(elapsed)
    native_responses.append(response)
    elapsed, response = request(int(os.environ["QEMU_PORT"]))
    qemu_times.append(elapsed)
    qemu_responses.append(response)
    print(json.dumps({"sample": index + 1, "native_ns": native_times[-1], "qemu_ns": qemu_times[-1]}), flush=True)

manifest = json.loads((pathlib.Path(os.environ["ARTIFACT_DIR"]) / "manifest.json").read_text())
native_binary_sha = hashlib.sha256(pathlib.Path(os.environ["BIN_DIR"]).joinpath("file-evaluator").read_bytes()).hexdigest()
manifest_fixture = json.loads((pathlib.Path(os.environ["ARTIFACT_DIR"]) / "file-evaluator-manifest.json").read_text())
if not isinstance(manifest_fixture, dict) or manifest_fixture.get("sha256") != native_binary_sha:
    raise SystemExit(f"host/guest evaluator binary mismatch: host={native_binary_sha}, manifest={manifest_fixture}")
report = module.build_qemu_report(
    native_samples_ns=native_times,
    qemu_samples_ns=qemu_times,
    native_responses=native_responses,
    qemu_responses=qemu_responses,
    manifest_digests={key: manifest[key]["sha256"] for key in ("kernel", "initrd", "rootfs")},
    qemu_version=subprocess.check_output([os.environ["QEMU_BINARY"], "--version"], text=True).splitlines()[0],
    source_commit=subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(),
)
report.update({
    "completed_at": dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z"),
    "workflow_run_id": os.getenv("GITHUB_RUN_ID"),
    "environment": {
        "os": platform.platform(),
        "machine": platform.machine(),
        "logical_cpus": os.cpu_count(),
        "cpu": module.cpu_model(),
    },
    "comparison_policy": "QEMU TCG full-boot samples are a compatibility-cost profile and are not ranked against persistent warm runtime lanes",
    "fixture": {
        "id": os.environ["FILE_EVALUATOR_ID"],
        "package": os.environ["FILE_EVALUATOR_PACKAGE"],
        "native_binary_sha256": native_binary_sha,
        "manifest": manifest_fixture,
        "host_guest_binary_identical": True,
    },
})
output = pathlib.Path(os.environ["OUTPUT"])
output.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
print(json.dumps(report, sort_keys=True))
PY

printf 'QEMU benchmark evidence: %s\n' "$OUTPUT"
