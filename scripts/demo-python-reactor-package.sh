#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOST="127.0.0.1"
PORT="${PORT:-}"
if [[ -z "${PORT}" ]]; then
  PORT="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(('127.0.0.1', 0))
print(s.getsockname()[1])
s.close()
PY
)"
fi
BASE_URL="http://${HOST}:${PORT}"
BIN="${ROOT}/bin/shimmy-demo"
PACKAGE_DIR="${ROOT}/examples/demo-python-reactor-package"
LOG="${ROOT}/.demo-python-reactor-package-server.log"
WASM="${PYTHON_REACTOR_WASM:-}"
CACHE_DIR="${FUNCTION_WASM_COMPILE_CACHE:-${TMPDIR:-/tmp}/shimmy-reactor-wazero-cache}"

usage() {
  cat <<'EOF'
usage: PYTHON_REACTOR_WASM=/path/to/python-reactor.wasm scripts/demo-python-reactor-package.sh

Runs the optional python-reactor package-mode demo. The large reactor artifact is
not committed to this repository; provide it with PYTHON_REACTOR_WASM.

Set SHIMMY_REACTOR_REQUIRE=1 to fail instead of skipping when the artifact or a
Linux host is unavailable.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

skip() {
  echo "skip: $*" >&2
  if [[ "${SHIMMY_REACTOR_REQUIRE:-}" == "1" ]]; then
    exit 1
  fi
  exit 0
}

if [[ "$(uname -s)" != "Linux" ]]; then
  skip "python-reactor.wasm is currently verified on Linux only"
fi
if [[ -z "${WASM}" ]]; then
  skip "PYTHON_REACTOR_WASM is not set"
fi
if [[ ! -f "${WASM}" ]]; then
  skip "PYTHON_REACTOR_WASM=${WASM} is not a readable file"
fi

for cmd in go curl python3; do
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "error: ${cmd} is required" >&2
    exit 1
  fi
done

echo "==> Priming wazero compile cache with optional package-mode smoke"
(
  cd "${ROOT}"
  env \
    PYTHON_REACTOR_WASM="${WASM}" \
    FUNCTION_WASM_COMPILE_CACHE="${CACHE_DIR}" \
    go test ./internal/execution -run 'TestPythonReactorPackageEntrypoint_OptionalArtifactSmoke' -count=1 -v
)

echo "==> Building shimmy demo binary"
(cd "${ROOT}" && go build -trimpath -buildvcs=false -o "${BIN}" .)

rm -f "${LOG}"
server_pid=""
cleanup() {
  if [[ -n "${server_pid}" ]] && kill -0 "${server_pid}" 2>/dev/null; then
    kill "${server_pid}" 2>/dev/null || true
    for _ in {1..30}; do
      kill -0 "${server_pid}" 2>/dev/null || return 0
      sleep 0.1
    done
    kill -KILL "${server_pid}" 2>/dev/null || true
    wait "${server_pid}" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "==> Starting shimmy on ${BASE_URL} with python-reactor package mode"
(
  cd "${ROOT}"
  exec env \
    LOG_LEVEL=error \
    FUNCTION_INTERFACE=wasm \
    FUNCTION_WASM_PROFILE=python-reactor \
    FUNCTION_WASM_MODULE="${WASM}" \
    FUNCTION_WASM_MAX_MEMORY_PAGES=8192 \
    FUNCTION_WASM_COMPILE_CACHE="${CACHE_DIR}" \
    FUNCTION_LF_ROOT="${PACKAGE_DIR}" \
    FUNCTION_LF_EVAL_ENTRYPOINT="evaluation_function.evaluation:evaluation_function" \
    FUNCTION_LF_PREVIEW_ENTRYPOINT="evaluation_function.preview:preview_function" \
    FUNCTION_TIMEOUT=90s \
    FUNCTION_WORKER_SEND_TIMEOUT=90s \
    "${BIN}" --max-workers 1 serve --host "${HOST}" --port "${PORT}"
) >"${LOG}" 2>&1 &
server_pid="$!"

for _ in {1..300}; do
  if ! kill -0 "${server_pid}" 2>/dev/null; then
    echo "server exited early; log follows:" >&2
    cat "${LOG}" >&2 || true
    exit 1
  fi
  if curl -fsS "${BASE_URL}/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

if ! curl -fsS "${BASE_URL}/health" >/dev/null 2>&1; then
  echo "server did not become ready; log follows:" >&2
  cat "${LOG}" >&2 || true
  exit 1
fi

request_eval() {
  local response="$1"
  local answer="$2"
  curl -fsS \
    -X POST "${BASE_URL}/" \
    -H 'Content-Type: application/json' \
    -H 'Command: eval' \
    --data "{\"response\":\"${response}\",\"answer\":\"${answer}\",\"params\":{\"correct_response_feedback\":\"reactor package correct\",\"incorrect_response_feedback\":\"reactor package incorrect\"}}"
}

echo "==> Eval request #1: correct answer"
resp1="$(request_eval 42 42)"
echo "${resp1}" | python3 -m json.tool

echo "==> Eval request #2: wrong answer; reactor snapshot should reset package state"
resp2="$(request_eval 41 42)"
echo "${resp2}" | python3 -m json.tool

RESP1="${resp1}" RESP2="${resp2}" python3 - <<'PY'
import json, os, sys
r1 = json.loads(os.environ['RESP1'])['result']
r2 = json.loads(os.environ['RESP2'])['result']
checks = [
    (r1.get('is_correct') is True, 'eval #1 should be correct'),
    (r2.get('is_correct') is False, 'eval #2 should be incorrect'),
    (r1.get('python_reactor_runtime') is True, 'eval #1 should report python_reactor_runtime'),
    (r2.get('python_reactor_runtime') is True, 'eval #2 should report python_reactor_runtime'),
    (r1.get('package_mode') is True, 'eval #1 should report package_mode'),
    (r2.get('package_mode') is True, 'eval #2 should report package_mode'),
    (r1.get('guest_invocation_count') == 1, 'eval #1 count should be 1'),
    (r2.get('guest_invocation_count') == 1, 'eval #2 count should be reset to 1'),
    (r1.get('snapshot_isolation_ok') is True, 'eval #1 snapshot flag should be true'),
    (r2.get('snapshot_isolation_ok') is True, 'eval #2 snapshot flag should be true'),
]
failed = [msg for ok, msg in checks if not ok]
if failed:
    print('DEMO FAILED:', *failed, sep='\n- ', file=sys.stderr)
    sys.exit(1)
print('\n✅ python-reactor package demo passed: Shimmy served a package-shaped Python evaluator through reactor-mode CPython WASM with snapshot isolation.')
PY

printf '\nServer log: %s\n' "${LOG}"
