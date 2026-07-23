#!/usr/bin/env bash
set -euo pipefail

ROOT=${ROOT:-/vol/bitbucket/ys25/shimmy/qemu-fallback}
BUNDLE_DIR=${BUNDLE_DIR:-$ROOT/bundle}
RUNTIME_DIR=${RUNTIME_DIR:-$ROOT/runtime}
RESULT_DIR=${RESULT_DIR:-$ROOT/evidence}
ACCELERATOR=${ACCELERATOR:-tcg}
REQUEST_COUNT=${REQUEST_COUNT:-3}
NATIVE_PORT=${NATIVE_PORT:-38080}
QEMU_PORT=${QEMU_PORT:-38081}
QEMU_BINARY=${QEMU_BINARY:-/usr/bin/qemu-system-x86_64}

host=$(hostname -f)
case "$host" in
  potoo01.doc.ic.ac.uk|potoo02.doc.ic.ac.uk) ;;
  *)
    printf 'refusing to run QEMU profile on non-batch host: %s\n' "$host" >&2
    exit 1
    ;;
esac

if [[ "$ROOT" != /vol/bitbucket/ys25/shimmy/qemu-fallback ]]; then
  printf 'unexpected profile root: %s\n' "$ROOT" >&2
  exit 1
fi
if [[ ! "$REQUEST_COUNT" =~ ^[1-9][0-9]*$ || "$REQUEST_COUNT" -gt 20 ]]; then
  printf 'REQUEST_COUNT must be in [1,20]\n' >&2
  exit 1
fi
if [[ "$ACCELERATOR" == kvm && ( ! -r /dev/kvm || ! -w /dev/kvm ) ]]; then
  printf 'KVM requested but /dev/kvm is not readable and writable by %s\n' "$(id -un)" >&2
  exit 1
fi
if [[ "$ACCELERATOR" != tcg && "$ACCELERATOR" != kvm ]]; then
  printf 'unsupported accelerator: %s\n' "$ACCELERATOR" >&2
  exit 1
fi

SHIMMY_BINARY="$RUNTIME_DIR/shimmy"
RUNNER_BINARY="$RUNTIME_DIR/shimmy-qemu-runner"
EVALUATOR_BINARY="$RUNTIME_DIR/file-evaluator"
MANIFEST="$BUNDLE_DIR/manifest.json"
ROOTFS="$BUNDLE_DIR/evaluator.squashfs"

for path in "$SHIMMY_BINARY" "$RUNNER_BINARY" "$EVALUATOR_BINARY" "$MANIFEST" "$ROOTFS" "$BUNDLE_DIR/vmlinuz" "$BUNDLE_DIR/initramfs.img" "$QEMU_BINARY"; do
  if [[ ! -f "$path" || ! -r "$path" ]]; then
    printf 'missing profile input: %s\n' "$path" >&2
    exit 1
  fi
done
for path in "$SHIMMY_BINARY" "$RUNNER_BINARY" "$EVALUATOR_BINARY" "$QEMU_BINARY"; do
  if [[ ! -x "$path" ]]; then
    printf 'profile executable is not executable: %s\n' "$path" >&2
    exit 1
  fi
done

python3 - "$MANIFEST" "$BUNDLE_DIR" <<'PY'
import hashlib
import json
import pathlib
import sys

manifest_path = pathlib.Path(sys.argv[1])
root = pathlib.Path(sys.argv[2])
manifest = json.loads(manifest_path.read_text())
for key in ("kernel", "initrd", "rootfs"):
    artifact = root / manifest[key]["path"]
    digest = hashlib.sha256(artifact.read_bytes()).hexdigest()
    if digest != manifest[key]["sha256"]:
        raise SystemExit(f"{key} digest mismatch: {digest}")
PY

mkdir -p "$RESULT_DIR"
run_id=$(date -u +%Y%m%dT%H%M%SZ)-${ACCELERATOR}
run_dir="$RESULT_DIR/$run_id"
mkdir -p "$run_dir"
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
  FUNCTION_COMMAND="$EVALUATOR_BINARY"
  FUNCTION_WORKING_DIR="$RUNTIME_DIR"
  FUNCTION_TIMEOUT=180s
  FUNCTION_MAX_PROCS=1
  LOG_LEVEL=info
)

env "${common_env[@]}" PORT="$NATIVE_PORT" "$SHIMMY_BINARY" serve >"$run_dir/native.log" 2>&1 &
native_pid=$!
env "${common_env[@]}" \
  PORT="$QEMU_PORT" \
  FUNCTION_QEMU_ENABLED=true \
  FUNCTION_QEMU_RUNNER="$RUNNER_BINARY" \
  FUNCTION_QEMU_BINARY="$QEMU_BINARY" \
  FUNCTION_QEMU_ROOTFS="$ROOTFS" \
  FUNCTION_QEMU_IMAGE_MANIFEST="$MANIFEST" \
  FUNCTION_QEMU_ACCELERATOR="$ACCELERATOR" \
  FUNCTION_QEMU_MEMORY_MB=512 \
  FUNCTION_QEMU_VCPUS=1 \
  FUNCTION_QEMU_NETWORK_PROFILE=none \
  FUNCTION_QEMU_BOOT_TIMEOUT=120s \
  "$SHIMMY_BINARY" serve >"$run_dir/qemu.log" 2>&1 &
qemu_pid=$!

wait_for_health() {
  local port=$1
  local pid=$2
  for _ in $(seq 1 300); do
    if ! kill -0 "$pid" 2>/dev/null; then
      return 1
    fi
    if curl --silent --fail "http://127.0.0.1:$port/health" >/dev/null; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}
wait_for_health "$NATIVE_PORT" "$native_pid"
wait_for_health "$QEMU_PORT" "$qemu_pid"

payload='{"response":"same","answer":"same","params":{"source":"doc-profile"}}'
: >"$run_dir/native-times.txt"
: >"$run_dir/qemu-times.txt"
for index in $(seq 1 "$REQUEST_COUNT"); do
  curl --max-time 180 --silent --show-error --fail \
    --output "$run_dir/native-$index.json" \
    --write-out '%{time_total}\n' \
    --header 'Content-Type: application/json' \
    --header 'Command: eval' \
    --data "$payload" \
    "http://127.0.0.1:$NATIVE_PORT/" >>"$run_dir/native-times.txt"
  curl --max-time 180 --silent --show-error --fail \
    --output "$run_dir/qemu-$index.json" \
    --write-out '%{time_total}\n' \
    --header 'Content-Type: application/json' \
    --header 'Command: eval' \
    --data "$payload" \
    "http://127.0.0.1:$QEMU_PORT/" >>"$run_dir/qemu-times.txt"
done

python3 - "$run_dir" "$REQUEST_COUNT" "$ACCELERATOR" "$host" "$MANIFEST" <<'PY'
import json
import pathlib
import statistics
import subprocess
import sys

root = pathlib.Path(sys.argv[1])
count = int(sys.argv[2])
accelerator = sys.argv[3]
host = sys.argv[4]
manifest = json.loads(pathlib.Path(sys.argv[5]).read_text())
for index in range(1, count + 1):
    native = json.loads((root / f"native-{index}.json").read_text())
    qemu = json.loads((root / f"qemu-{index}.json").read_text())
    if native != qemu:
        raise SystemExit(f"response mismatch at iteration {index}")

native_times = [float(value) for value in (root / "native-times.txt").read_text().splitlines()]
qemu_times = [float(value) for value in (root / "qemu-times.txt").read_text().splitlines()]
report = {
    "status": "PASS",
    "host": host,
    "accelerator": accelerator,
    "request_count": count,
    "response_parity": True,
    "native_seconds": native_times,
    "qemu_seconds": qemu_times,
    "native_median_seconds": statistics.median(native_times),
    "qemu_median_seconds": statistics.median(qemu_times),
    "manifest_digests": {key: manifest[key]["sha256"] for key in ("kernel", "initrd", "rootfs")},
    "qemu_version": subprocess.check_output(["qemu-system-x86_64", "--version"], text=True).splitlines()[0],
}
(root / "profile.json").write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
print(json.dumps(report, sort_keys=True))
PY

printf 'DoC QEMU profile evidence: %s\n' "$run_dir/profile.json"
