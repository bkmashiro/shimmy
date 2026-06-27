#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${PORT:-}"
HOST="127.0.0.1"
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
PYODIDE_DIR="${ROOT}/examples/demo-pyodide-python"
PACKAGE_DIR="${ROOT}/examples/demo-pyodide-package"
ADAPTER="${ROOT}/examples/lambda-feedback-adapter/lf_compat_adapter.py"
LOG="${ROOT}/.demo-pyodide-package-server.log"

for cmd in go curl python3 node npm; do
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "error: ${cmd} is required" >&2
    exit 1
  fi
done

echo "==> Building shimmy demo binary"
(cd "${ROOT}" && go build -trimpath -buildvcs=false -o "${BIN}" .)

echo "==> Installing Pyodide npm dependency if needed"
if [[ ! -d "${PYODIDE_DIR}/node_modules/pyodide" ]]; then
  (cd "${PYODIDE_DIR}" && npm install)
fi

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

echo "==> Starting shimmy on ${BASE_URL} with Pyodide package mode"
(
  cd "${ROOT}"
  exec env \
    LOG_LEVEL=error \
    FUNCTION_INTERFACE=pyodide \
    FUNCTION_PYODIDE_RUNNER="${PYODIDE_DIR}/runner.js" \
    FUNCTION_PYODIDE_ROOT="${PACKAGE_DIR}" \
    FUNCTION_PYODIDE_EVAL_ENTRYPOINT="evaluation_function.evaluation:evaluation_function" \
    FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT="evaluation_function.preview:preview_function" \
    FUNCTION_PYODIDE_ADAPTER="${ADAPTER}" \
    FUNCTION_TIMEOUT=60s \
    FUNCTION_WORKER_SEND_TIMEOUT=60s \
    "${BIN}" serve --host "${HOST}" --port "${PORT}"
) >"${LOG}" 2>&1 &
server_pid="$!"

for _ in {1..180}; do
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
    --data "{\"response\":\"${response}\",\"answer\":\"${answer}\",\"params\":{\"correct_response_feedback\":\"package correct\",\"incorrect_response_feedback\":\"package incorrect\"}}"
}

echo "==> Eval request #1: correct answer"
resp1="$(request_eval 42 42)"
echo "${resp1}" | python3 -m json.tool

echo "==> Eval request #2: wrong answer; package namespace should be fresh"
resp2="$(request_eval 41 42)"
echo "${resp2}" | python3 -m json.tool

RESP1="${resp1}" RESP2="${resp2}" python3 - <<'PY'
import json, os, sys
r1 = json.loads(os.environ['RESP1'])['result']
r2 = json.loads(os.environ['RESP2'])['result']
checks = [
    (r1.get('is_correct') is True, 'eval #1 should be correct'),
    (r2.get('is_correct') is False, 'eval #2 should be incorrect'),
    (r1.get('package_mode') is True, 'eval #1 should report package_mode'),
    (r2.get('package_mode') is True, 'eval #2 should report package_mode'),
    (r1.get('guest_invocation_count') == 1, 'eval #1 count should be 1'),
    (r2.get('guest_invocation_count') == 1, 'eval #2 count should be reset to 1'),
    (r1.get('fresh_namespace_ok') is True, 'eval #1 fresh namespace flag should be true'),
    (r2.get('fresh_namespace_ok') is True, 'eval #2 fresh namespace flag should be true'),
]
failed = [msg for ok, msg in checks if not ok]
if failed:
    print('DEMO FAILED:', *failed, sep='\n- ', file=sys.stderr)
    sys.exit(1)
print('\n✅ Pyodide package demo passed: Shimmy served a package-shaped Python eval entrypoint with fresh per-request module state. Preview entrypoint coverage is in the dispatcher integration test.')
PY

printf '\nServer log: %s\n' "${LOG}"
