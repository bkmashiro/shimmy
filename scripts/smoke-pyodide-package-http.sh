#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOST="127.0.0.1"
PORT="${SHIMMY_PYODIDE_PACKAGE_PORT:-18082}"
BASE="http://${HOST}:${PORT}"
BIN="${SHIMMY_PYODIDE_PACKAGE_BIN:-${ROOT}/bin/shimmy-pyodide-package-smoke}"
LOG_DIR="${ROOT}/.demo-logs"
LOG="${LOG_DIR}/pyodide-package-http.log"
PID=""

cleanup() {
  if [[ -n "${PID}" ]] && kill -0 "${PID}" 2>/dev/null; then
    kill "${PID}" 2>/dev/null || true
    for _ in $(seq 1 20); do
      kill -0 "${PID}" 2>/dev/null || break
      sleep 0.1
    done
    if kill -0 "${PID}" 2>/dev/null; then
      kill -KILL "${PID}" 2>/dev/null || true
    fi
    wait "${PID}" 2>/dev/null || true
  fi
}
trap cleanup EXIT

for command in go node npm curl python3; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "error: ${command} is required" >&2
    exit 1
  fi
done

mkdir -p "${LOG_DIR}" "$(dirname "${BIN}")"
rm -f "${LOG}"

if [[ ! -d "${ROOT}/examples/eval-pyodide/node_modules/pyodide" ]]; then
  npm --prefix "${ROOT}/examples/eval-pyodide" ci --silent
fi

(cd "${ROOT}" && go build -trimpath -buildvcs=false -o "${BIN}" .)

(
  cd "${ROOT}"
  exec env \
    LOG_LEVEL=error \
    FUNCTION_INTERFACE=pyodide \
    FUNCTION_PYODIDE_RUNNER="${ROOT}/examples/eval-pyodide/runner.js" \
    FUNCTION_PYODIDE_ROOT="${ROOT}/examples/lambda-feedback-fixtures/boilerplate-python" \
    FUNCTION_PYODIDE_EVAL_ENTRYPOINT="evaluation_function.evaluation:evaluation_function" \
    FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT="evaluation_function.preview:preview_function" \
    FUNCTION_PYODIDE_ADAPTER="${ROOT}/examples/lambda-feedback-adapter/lf_compat_adapter.py" \
    FUNCTION_MAX_PROCS=1 \
    FUNCTION_WORKER_SEND_TIMEOUT=120s \
    "${BIN}" serve --host "${HOST}" --port "${PORT}"
) >"${LOG}" 2>&1 &
PID="$!"

ready=0
for _ in $(seq 1 480); do
  if ! kill -0 "${PID}" 2>/dev/null; then
    echo "error: Shimmy exited before becoming ready" >&2
    sed -n '1,160p' "${LOG}" >&2 || true
    exit 1
  fi
  if curl -fsS "${BASE}/health" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.5
done
if [[ "${ready}" != 1 ]]; then
  echo "error: Shimmy did not become ready" >&2
  sed -n '1,160p' "${LOG}" >&2 || true
  exit 1
fi

post() {
  local method="$1"
  local payload="$2"
  curl -fsS -X POST "${BASE}/" \
    -H 'Content-Type: application/json' \
    -H "Command: ${method}" \
    --data "${payload}"
}

assert_eval_true() {
  PAYLOAD="$1" python3 - <<'PY'
import json
import os
body = json.loads(os.environ["PAYLOAD"])
result = body.get("result", body)
assert result.get("is_correct") is True, body
PY
}

assert_preview() {
  PAYLOAD="$1" python3 - <<'PY'
import json
import os
body = json.loads(os.environ["PAYLOAD"])
result = body.get("result", body)
assert isinstance(result.get("preview"), dict), body
PY
}

EVAL_PAYLOAD='{"response":"2","answer":"2","params":{}}'
PREVIEW_PAYLOAD='{"response":"x + 1","answer":"","params":{}}'

first="$(post eval "${EVAL_PAYLOAD}")"
assert_eval_true "${first}"
echo "PASS: package eval"

preview="$(post preview "${PREVIEW_PAYLOAD}")"
assert_preview "${preview}"
echo "PASS: package preview"

error_response="${LOG_DIR}/pyodide-package-http-error.json"
status="$(curl -sS -o "${error_response}" -w '%{http_code}' \
  -X POST "${BASE}/" \
  -H 'Content-Type: application/json' \
  -H 'Command: unsupported-smoke-method' \
  --data "${EVAL_PAYLOAD}")"
if [[ "${status}" -lt 400 ]]; then
  echo "error: unsupported method unexpectedly returned HTTP ${status}" >&2
  cat "${error_response}" >&2 || true
  exit 1
fi
echo "PASS: unsupported method returned HTTP ${status}"

second="$(post eval "${EVAL_PAYLOAD}")"
assert_eval_true "${second}"
echo "PASS: runner recovered for the next package eval"
