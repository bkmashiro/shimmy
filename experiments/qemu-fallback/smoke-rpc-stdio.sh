#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/../.." && pwd)
ARTIFACT_DIR=${ARTIFACT_DIR:-"$REPO_ROOT/artifacts/qemu-fallback"}
BIN_DIR=${BIN_DIR:-"$REPO_ROOT/.cache/qemu-fallback-rpc-smoke"}
NATIVE_PORT=${NATIVE_PORT:-18082}
QEMU_PORT=${QEMU_PORT:-18083}
EVALUATOR_DIR=/opt/evaluator
EVALUATOR_PATH="$EVALUATOR_DIR/rpc-evaluator"

mkdir -p "$BIN_DIR"
go build -trimpath -o "$BIN_DIR/shimmy" .
go build -trimpath -o "$BIN_DIR/shimmy-qemu-runner" ./cmd/shimmy-qemu-runner
go build -trimpath -o "$BIN_DIR/rpc-evaluator" ./experiments/qemu-fallback/rpc-evaluator
sudo mkdir -p "$EVALUATOR_DIR"
sudo install -m 0755 "$BIN_DIR/rpc-evaluator" "$EVALUATOR_PATH"

for path in "$ARTIFACT_DIR/manifest.json" "$ARTIFACT_DIR/evaluator.qcow2"; do
  if [[ ! -f "$path" ]]; then
    printf 'missing QEMU RPC smoke artifact: %s\n' "$path" >&2
    exit 1
  fi
done
QEMU_BINARY=$(command -v qemu-system-x86_64)

native_log="$BIN_DIR/native-rpc.log"
qemu_log="$BIN_DIR/qemu-rpc.log"
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
  FUNCTION_INTERFACE=rpc
  FUNCTION_RPC_TRANSPORT=stdio
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
  FUNCTION_QEMU_ROOTFS="$ARTIFACT_DIR/evaluator.qcow2" \
  FUNCTION_QEMU_IMAGE_MANIFEST="$ARTIFACT_DIR/manifest.json" \
  FUNCTION_QEMU_ACCELERATOR=tcg \
  FUNCTION_QEMU_MEMORY_MB=512 \
  FUNCTION_QEMU_VCPUS=1 \
  FUNCTION_QEMU_NETWORK_PROFILE=none \
  FUNCTION_QEMU_BOOT_TIMEOUT=60s \
  "$BIN_DIR/shimmy" serve --port "$QEMU_PORT" >"$qemu_log" 2>&1 &
qemu_pid=$!

wait_for_health() {
  local port=$1
  local pid=$2
  local server_log=$3
  for _ in $(seq 1 60); do
    if curl -fsS "http://127.0.0.1:${port}/health" >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      printf 'RPC shimmy process for port %s exited early\n' "$port" >&2
      cat "$server_log" >&2
      return 1
    fi
    sleep 1
  done
  printf 'RPC shimmy process for port %s did not become healthy\n' "$port" >&2
  cat "$server_log" >&2
  return 1
}

post_eval() {
  local port=$1
  local label=$2
  local sequence=$3
  local server_log=$4
  local response_file="$BIN_DIR/${label}-${sequence}.json"
  local status
  status=$(curl -sS -o "$response_file" -w '%{http_code}' -X POST "http://127.0.0.1:${port}/" \
    -H 'Content-Type: application/json' \
    -H 'Command: eval' \
    -d "{\"response\":\"same-${sequence}\",\"answer\":\"same-${sequence}\",\"params\":{\"sequence\":${sequence}}}")
  if [[ "$status" != 200 ]]; then
    printf '%s RPC evaluation %s returned HTTP %s\n' "$label" "$sequence" "$status" >&2
    cat "$response_file" >&2
    cat "$server_log" >&2
    return 1
  fi
  cat "$response_file"
}

wait_for_health "$NATIVE_PORT" "$native_pid" "$native_log"
wait_for_health "$QEMU_PORT" "$qemu_pid" "$qemu_log"

for sequence in 1 2; do
  native_response=$(post_eval "$NATIVE_PORT" native "$sequence" "$native_log")
  qemu_response=$(post_eval "$QEMU_PORT" qemu "$sequence" "$qemu_log")
  NATIVE_RESPONSE="$native_response" QEMU_RESPONSE="$qemu_response" SEQUENCE="$sequence" python3 - <<'PY'
import json
import os
native = json.loads(os.environ["NATIVE_RESPONSE"])
qemu = json.loads(os.environ["QEMU_RESPONSE"])
sequence = int(os.environ["SEQUENCE"])
assert native == qemu, {"native": native, "qemu": qemu}
assert qemu["command"] == "eval", qemu
assert qemu["result"]["is_correct"] is True, qemu
assert qemu["result"]["echo"]["params"]["sequence"] == sequence, qemu
print(json.dumps({"status": "pass", "sequence": sequence, "response": qemu}, sort_keys=True))
PY
done
