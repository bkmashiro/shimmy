#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUNNER="${SHIMMY_PYODIDE_RUNNER:-${ROOT}/examples/eval-pyodide/runner.js}"
EVALUATOR="${SHIMMY_PYODIDE_EVALUATOR:-${ROOT}/tests/e2e/pyodide-scipy/evaluator.py}"
HOST=127.0.0.1
TMP="$(mktemp -d "${TMPDIR:-/tmp}/shimmy-pyodide-scipy-e2e.XXXXXX")"
PORT="${SHIMMY_E2E_PORT:-}"
BIN="${SHIMMY_E2E_BIN:-${TMP}/shimmy}"
SERVER_PID=""

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -rf "${TMP}"
}
trap cleanup EXIT

for cmd in curl node python3; do
  command -v "${cmd}" >/dev/null 2>&1 || { echo "missing required command: ${cmd}" >&2; exit 1; }
done
[[ "$(uname -s)" == Linux ]] || { echo "Pyodide SciPy E2E requires Linux" >&2; exit 1; }
[[ -r "${RUNNER}" && -r "${EVALUATOR}" ]] || { echo "runner and evaluator must be readable" >&2; exit 1; }
(
  cd "$(dirname "${RUNNER}")"
  node -e 'require.resolve("pyodide")' >/dev/null
) || { echo "install the locked Pyodide dependencies with npm ci" >&2; exit 1; }

if [[ -z "${PORT}" ]]; then
  PORT="$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
fi
if [[ -z "${SHIMMY_E2E_BIN:-}" ]]; then
  command -v go >/dev/null 2>&1 || { echo "missing required command: go" >&2; exit 1; }
  (cd "${ROOT}" && go build -trimpath -buildvcs=true -o "${BIN}" .)
fi
[[ -x "${BIN}" ]] || { echo "Shimmy binary must be executable" >&2; exit 1; }

LOG="${TMP}/server.log"
(
  cd "${ROOT}"
  exec env \
    LOG_LEVEL=error \
    FUNCTION_INTERFACE=pyodide \
    FUNCTION_PYODIDE_RUNNER="${RUNNER}" \
    FUNCTION_PYODIDE_SCRIPT="${EVALUATOR}" \
    FUNCTION_PYODIDE_PACKAGES=scipy \
    FUNCTION_MAX_PROCS=1 \
    FUNCTION_WORKER_SEND_TIMEOUT=60s \
    "${BIN}" serve --host "${HOST}" --port "${PORT}"
) >"${LOG}" 2>&1 &
SERVER_PID="$!"
BASE_URL="http://${HOST}:${PORT}"
ready=false
for _ in $(seq 1 300); do
  if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
    echo "Shimmy exited during Pyodide startup" >&2
    sed -n '1,240p' "${LOG}" >&2
    exit 1
  fi
  if curl -fsS "${BASE_URL}/health" >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.2
done
[[ "${ready}" == true ]] || { echo "Shimmy did not become ready" >&2; sed -n '1,240p' "${LOG}" >&2; exit 1; }

request() {
  curl -fsS -X POST "${BASE_URL}/" -H 'Content-Type: application/json' -H "Command: $1" --data "$2"
}
OK="$(request eval '{"response":"0","answer":0.5,"params":{"tolerance":1e-12}}')"
BAD="$(request eval '{"response":"1","answer":0.5,"params":{"tolerance":1e-12}}')"
PREVIEW="$(request preview '{"response":"0","params":{}}')"
python3 - "${OK}" "${BAD}" "${PREVIEW}" <<'PY'
import json, sys
ok, bad, preview = map(json.loads, sys.argv[1:])
assert ok["is_correct"] is True
assert abs(ok["value"] - 0.5) < 1e-12
assert bad["is_correct"] is False
assert abs(preview["preview"]["sigmoid"] - 0.5) < 1e-12
print(json.dumps({"eval_correct": ok, "eval_incorrect": bad, "preview": preview}, sort_keys=True))
PY

echo "pyodide_scipy_e2e=PASS"
