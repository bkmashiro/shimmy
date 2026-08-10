#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WASM="${SHIMMY_PYTHON_REACTOR_WASM:?set SHIMMY_PYTHON_REACTOR_WASM to a Producer artifact}"
MANIFEST="${SHIMMY_PYTHON_REACTOR_MANIFEST:?set SHIMMY_PYTHON_REACTOR_MANIFEST to its manifest.json}"
EVALUATOR="${SHIMMY_E2E_EVALUATOR:-${ROOT}/tests/e2e/python-reactor/evaluator.py}"
HOST="127.0.0.1"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/shimmy-python-reactor-e2e.XXXXXX")"
PORT="${SHIMMY_E2E_PORT:-}"
SERVER_PID=""
PREBUILT_BIN="${SHIMMY_E2E_BIN:-}"
PREBUILT_CHECK="${SHIMMY_E2E_ARTIFACT_CHECK_BIN:-}"

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -rf "${TMP}"
}
trap cleanup EXIT

for cmd in curl python3; do
  command -v "${cmd}" >/dev/null 2>&1 || { echo "missing required command: ${cmd}" >&2; exit 1; }
done
[[ "$(uname -s)" == "Linux" ]] || { echo "Python Reactor E2E requires Linux" >&2; exit 1; }
[[ -r "${WASM}" && -r "${MANIFEST}" && -r "${EVALUATOR}" ]] || { echo "artifact, manifest, and evaluator must be readable" >&2; exit 1; }

if [[ -z "${PORT}" ]]; then
  PORT="$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
fi
BASE_URL="http://${HOST}:${PORT}"
BIN="${PREBUILT_BIN:-${TMP}/shimmy}"
CHECK="${PREBUILT_CHECK:-${TMP}/shimmy-artifact-check}"
LOG="${TMP}/server.log"

if [[ -n "${PREBUILT_BIN}" || -n "${PREBUILT_CHECK}" ]]; then
  [[ -n "${PREBUILT_BIN}" && -n "${PREBUILT_CHECK}" ]] || {
    echo "set both SHIMMY_E2E_BIN and SHIMMY_E2E_ARTIFACT_CHECK_BIN" >&2
    exit 1
  }
  [[ -x "${BIN}" && -x "${CHECK}" ]] || { echo "prebuilt Linux binaries must be executable" >&2; exit 1; }
else
  command -v go >/dev/null 2>&1 || { echo "missing required command: go" >&2; exit 1; }
  (
    cd "${ROOT}"
    go build -trimpath -buildvcs=true -o "${BIN}" .
    go build -trimpath -buildvcs=true -o "${CHECK}" ./cmd/shimmy-artifact-check
  )
fi

"${CHECK}" -profile python-reactor -module "${WASM}" -manifest "${MANIFEST}" -json >"${TMP}/artifact-check.json"

(
  cd "${ROOT}"
  exec env \
    LOG_LEVEL=error \
    FUNCTION_INTERFACE=wasm \
    FUNCTION_WASM_PROFILE=python-reactor \
    FUNCTION_WASM_MODULE="${WASM}" \
    FUNCTION_WASM_MANIFEST="${MANIFEST}" \
    FUNCTION_WASM_PYTHON_SCRIPT="${EVALUATOR}" \
    FUNCTION_WASM_PYTHON_LIFECYCLE=snapshot \
    FUNCTION_WASM_SNAPSHOT_MODE=memcpy \
    FUNCTION_MAX_PROCS=1 \
    FUNCTION_WORKER_SEND_TIMEOUT=30s \
    "${BIN}" serve --host "${HOST}" --port "${PORT}"
) >"${LOG}" 2>&1 &
SERVER_PID="$!"

ready=false
for _ in $(seq 1 150); do
  if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
    echo "Shimmy exited during startup" >&2
    sed -n '1,200p' "${LOG}" >&2
    exit 1
  fi
  if curl -fsS "${BASE_URL}/health" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.2
done
[[ "${ready}" == true ]] || { echo "Shimmy did not become ready" >&2; sed -n '1,200p' "${LOG}" >&2; exit 1; }

request() {
  local command="$1"
  local body="$2"
  curl -fsS -X POST "${BASE_URL}/" \
    -H 'Content-Type: application/json' \
    -H "Command: ${command}" \
    --data "${body}"
}

EVAL_OK="$(request eval '{"response":"42","answer":"42","params":{"tolerance":0}}')"
EVAL_BAD="$(request eval '{"response":"41","answer":"42","params":{"tolerance":0}}')"
PREVIEW="$(request preview '{"response":"41","params":{}}')"

EVAL_OK="${EVAL_OK}" EVAL_BAD="${EVAL_BAD}" PREVIEW="${PREVIEW}" python3 - <<'PY'
import json
import os

def result(name):
    body = json.loads(os.environ[name])
    if "error" in body:
        raise SystemExit(f"{name} returned an error: {body['error']}")
    return body["result"]

ok = result("EVAL_OK")
bad = result("EVAL_BAD")
preview = result("PREVIEW")
checks = [
    (ok.get("is_correct") is True, "correct eval result"),
    (bad.get("is_correct") is False, "incorrect eval result"),
    (preview.get("preview") == "submitted: 41", "preview result"),
    (ok.get("invocation_count") == 1, "first request starts from prepared state"),
    (bad.get("invocation_count") == 1, "second request is reset"),
    (preview.get("invocation_count") == 1, "preview request is reset"),
]
failed = [label for passed, label in checks if not passed]
if failed:
    raise SystemExit("failed checks: " + ", ".join(failed))
print(json.dumps({"eval_correct": ok, "eval_incorrect": bad, "preview": preview}, sort_keys=True))
PY

printf 'PASS: Linux Python Reactor HTTP E2E\n'
printf 'configuration: FUNCTION_INTERFACE=wasm FUNCTION_WASM_PROFILE=python-reactor FUNCTION_WASM_PYTHON_LIFECYCLE=snapshot FUNCTION_WASM_SNAPSHOT_MODE=memcpy\n'
