#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="${1:-pyodide-only}"
PORT="${SHIMMY_PYODIDE_DEMO_PORT:-18080}"
HOST="${SHIMMY_PYODIDE_DEMO_HOST:-127.0.0.1}"
RUNNER="${ROOT}/examples/eval-pyodide/runner.js"
PYODIDE_DIR="${ROOT}/examples/eval-pyodide/node_modules/pyodide"
SERVER_PID=""
LOG_FILE=""

usage() {
  cat >&2 <<'EOF'
Usage: scripts/demo-python-examples.sh [pyodide-only]

The pinned Pyodide npm package must already be installed:
  (cd examples/eval-pyodide && npm ci)
EOF
  exit 2
}

[[ "${MODE}" == "pyodide-only" ]] || usage
[[ -d "${PYODIDE_DIR}" ]] || {
  echo "Pyodide is not installed; run '(cd examples/eval-pyodide && npm ci)' first" >&2
  exit 2
}
command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is required" >&2; exit 2; }
command -v node >/dev/null 2>&1 || { echo "node is required" >&2; exit 2; }

if [[ -n "${SHIMMY_BIN:-}" ]]; then
  [[ -x "${SHIMMY_BIN}" ]] || { echo "Shimmy binary is not executable: ${SHIMMY_BIN}" >&2; exit 2; }
  SHIMMY_CMD=("${SHIMMY_BIN}")
else
  command -v go >/dev/null 2>&1 || { echo "go is required unless SHIMMY_BIN is set" >&2; exit 2; }
  SHIMMY_CMD=(go run .)
fi

stop_server() {
  if [[ -n "${SERVER_PID}" ]]; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
    SERVER_PID=""
  fi
}

cleanup() {
  stop_server
  if [[ -n "${LOG_FILE}" ]]; then
    rm -f "${LOG_FILE}"
  fi
}
trap cleanup EXIT

start_server() {
  local script="$1"
  local packages="$2"
  LOG_FILE="$(mktemp "${TMPDIR:-/tmp}/shimmy-pyodide.XXXXXX")"
  (
    cd "${ROOT}"
    exec env \
      FUNCTION_INTERFACE=rpc \
      FUNCTION_RPC_TRANSPORT=stdio \
      FUNCTION_COMMAND=node \
      FUNCTION_ARGS="${RUNNER},${script}" \
      FUNCTION_PYODIDE_PACKAGES="${packages}" \
      FUNCTION_MAX_PROCS=1 \
      FUNCTION_WORKER_SEND_TIMEOUT=120s \
      "${SHIMMY_CMD[@]}" serve --host "${HOST}" --port "${PORT}"
  ) >"${LOG_FILE}" 2>&1 &
  SERVER_PID=$!

  for _ in {1..120}; do
    if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
      cat "${LOG_FILE}" >&2
      return 1
    fi
    if curl -fsS "http://${HOST}:${PORT}/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done

  cat "${LOG_FILE}" >&2
  echo "Shimmy did not become healthy" >&2
  return 1
}

run_case() {
  local name="$1"
  local script="$2"
  local packages="$3"
  local payload="$4"

  echo "== ${name} =="
  start_server "${script}" "${packages}"
  response="$(curl -fsS -X POST "http://${HOST}:${PORT}/" \
    -H 'Content-Type: application/json' \
    -H 'Command: eval' \
    --data "${payload}")"
  RESPONSE="${response}" CASE_NAME="${name}" python3 - <<'PY'
import json
import os

body = json.loads(os.environ["RESPONSE"])
result = body["result"]
name = os.environ["CASE_NAME"]
assert result["is_correct"] is True, result
if name == "matplotlib":
    assert result["png_bytes"] > 1000, result
print(json.dumps(result, sort_keys=True))
PY
  stop_server
}

run_case "scipy" \
  "${ROOT}/examples/eval-scipy/eval.py" \
  "scipy" \
  '{"response":"","answer":"5.0","params":{"test":"ttest","samples":[4.9,5.1,5.0,5.2,4.8],"alpha":0.05}}'

run_case "matplotlib" \
  "${ROOT}/examples/eval-matplotlib/eval.py" \
  "matplotlib" \
  '{"response":"","answer":"","params":{"values":[0,1,4,9]}}'

echo "Pyodide RPC examples passed."
