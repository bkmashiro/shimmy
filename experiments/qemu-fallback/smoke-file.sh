#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/../.." && pwd)
ARTIFACT_DIR=${ARTIFACT_DIR:-"$REPO_ROOT/artifacts/qemu-fallback"}
BIN_DIR=${BIN_DIR:-"$REPO_ROOT/.cache/qemu-fallback-smoke"}
NATIVE_PORT=${NATIVE_PORT:-18080}
QEMU_PORT=${QEMU_PORT:-18081}
EVALUATOR_DIR=/opt/evaluator
EVALUATOR_PATH="$EVALUATOR_DIR/file-evaluator"

mkdir -p "$BIN_DIR"
go build -trimpath -o "$BIN_DIR/shimmy" .
go build -trimpath -o "$BIN_DIR/shimmy-qemu-runner" ./cmd/shimmy-qemu-runner
go build -trimpath -o "$BIN_DIR/file-evaluator" ./experiments/qemu-fallback/file-evaluator
sudo mkdir -p "$EVALUATOR_DIR"
sudo install -m 0755 "$BIN_DIR/file-evaluator" "$EVALUATOR_PATH"

for path in "$ARTIFACT_DIR/manifest.json" "$ARTIFACT_DIR/vmlinuz" "$ARTIFACT_DIR/initramfs.img" "$ARTIFACT_DIR/evaluator.squashfs"; do
  if [[ ! -f "$path" ]]; then
    printf 'missing QEMU smoke artifact: %s\n' "$path" >&2
    exit 1
  fi
done
QEMU_BINARY=$(command -v qemu-system-x86_64)

native_log="$BIN_DIR/native.log"
qemu_log="$BIN_DIR/qemu.log"
native_pid=""
qemu_pid=""
cleanup() {
  if [[ -n "$native_pid" ]]; then kill "$native_pid" 2>/dev/null || true; fi
  if [[ -n "$qemu_pid" ]]; then kill "$qemu_pid" 2>/dev/null || true; fi
  wait "$native_pid" 2>/dev/null || true
  wait "$qemu_pid" 2>/dev/null || true
}
trap cleanup EXIT

common_env=(
  FUNCTION_INTERFACE=file
  FUNCTION_COMMAND="$EVALUATOR_PATH"
  FUNCTION_WORKING_DIR="$EVALUATOR_DIR"
  FUNCTION_MAX_PROCS=1
  FUNCTION_WORKER_SEND_TIMEOUT=90s
  LOG_LEVEL=info
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
  FUNCTION_QEMU_BOOT_TIMEOUT=120s \
  "$BIN_DIR/shimmy" serve --port "$QEMU_PORT" >"$qemu_log" 2>&1 &
qemu_pid=$!

wait_for_health() {
  local port=$1
  local pid=$2
  local log=$3
  for _ in $(seq 1 60); do
    if curl -fsS "http://127.0.0.1:${port}/health" >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      printf 'shimmy process for port %s exited early\n' "$port" >&2
      cat "$log" >&2
      return 1
    fi
    sleep 1
  done
  printf 'shimmy process for port %s did not become healthy\n' "$port" >&2
  cat "$log" >&2
  return 1
}

wait_for_health "$NATIVE_PORT" "$native_pid" "$native_log"
wait_for_health "$QEMU_PORT" "$qemu_pid" "$qemu_log"

post_eval() {
  local port=$1
  local label=$2
  local server_log=$3
  local response_file="$BIN_DIR/${label}-response.json"
  local status
  status=$(curl --max-time 150 -sS -o "$response_file" -w '%{http_code}' -X POST "http://127.0.0.1:${port}/" \
    -H 'Content-Type: application/json' \
    -H 'Command: eval' \
    -d "$payload")
  if [[ "$status" != 200 ]]; then
    printf '%s evaluation returned HTTP %s\n' "$label" "$status" >&2
    cat "$response_file" >&2
    cat "$server_log" >&2
    return 1
  fi
  cat "$response_file"
}

payload='{"response":"same","answer":"same","params":{"nested":{"value":42}}}'
native_response=$(post_eval "$NATIVE_PORT" native "$native_log")
qemu_response=$(post_eval "$QEMU_PORT" qemu "$qemu_log")

NATIVE_RESPONSE="$native_response" QEMU_RESPONSE="$qemu_response" python3 - <<'PY'
import json
import os
native = json.loads(os.environ["NATIVE_RESPONSE"])
qemu = json.loads(os.environ["QEMU_RESPONSE"])
assert native == qemu, {"native": native, "qemu": qemu}
assert native["command"] == "eval", native
assert native["result"]["is_correct"] is True, native
assert native["result"]["echo"]["params"]["nested"]["value"] == 42, native
print(json.dumps({"status": "pass", "native": native, "qemu": qemu}, sort_keys=True))
PY
