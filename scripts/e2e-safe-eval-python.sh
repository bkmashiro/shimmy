#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WASM="${SHIMMY_PYTHON_REACTOR_WASM:?set SHIMMY_PYTHON_REACTOR_WASM to a Producer base artifact}"
MANIFEST="${SHIMMY_PYTHON_REACTOR_MANIFEST:?set SHIMMY_PYTHON_REACTOR_MANIFEST to its manifest.json}"
EVALUATOR="${SHIMMY_SAFE_EVAL_EVALUATOR:-${ROOT}/examples/safe-eval-python/safe_eval.py}"
HOST=127.0.0.1
TMP="$(mktemp -d "${TMPDIR:-/tmp}/shimmy-safe-eval-python-e2e.XXXXXX")"
PORT="${SHIMMY_E2E_PORT:-}"
BIN="${SHIMMY_E2E_BIN:-${TMP}/shimmy}"
CHECK="${SHIMMY_E2E_ARTIFACT_CHECK_BIN:-${TMP}/shimmy-artifact-check}"
SERVER_PID=""

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
[[ "$(uname -s)" == "Linux" ]] || { echo "safeEvalPython Reactor E2E requires Linux" >&2; exit 1; }
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
if [[ -z "${SHIMMY_E2E_BIN:-}" || -z "${SHIMMY_E2E_ARTIFACT_CHECK_BIN:-}" ]]; then
  [[ -z "${SHIMMY_E2E_BIN:-}" && -z "${SHIMMY_E2E_ARTIFACT_CHECK_BIN:-}" ]] || {
    echo "set both SHIMMY_E2E_BIN and SHIMMY_E2E_ARTIFACT_CHECK_BIN" >&2
    exit 1
  }
  command -v go >/dev/null 2>&1 || { echo "missing required command: go" >&2; exit 1; }
  (
    cd "${ROOT}"
    go build -trimpath -buildvcs=true -o "${BIN}" .
    go build -trimpath -buildvcs=true -o "${CHECK}" ./cmd/shimmy-artifact-check
  )
fi
[[ -x "${BIN}" && -x "${CHECK}" ]] || { echo "Shimmy binaries must be executable" >&2; exit 1; }

"${CHECK}" -profile python-reactor -module "${WASM}" -manifest "${MANIFEST}" -json >"${TMP}/artifact-check.json"
LOG="${TMP}/server.log"
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
    FUNCTION_WASM_ALLOWED_PATHS= \
    FUNCTION_MAX_PROCS=1 \
    FUNCTION_WORKER_SEND_TIMEOUT=2s \
    "${BIN}" serve --host "${HOST}" --port "${PORT}"
) >"${LOG}" 2>&1 &
SERVER_PID="$!"
BASE_URL="http://${HOST}:${PORT}"
ready=false
for _ in $(seq 1 300); do
  if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
    echo "Shimmy exited during Reactor startup" >&2
    python3 - "${LOG}" <<'PY' >&2
import pathlib, sys
print(pathlib.Path(sys.argv[1]).read_text(errors="replace"))
PY
    exit 1
  fi
  if curl -fsS "${BASE_URL}/health" >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.2
done
[[ "${ready}" == true ]] || { echo "Shimmy did not become ready" >&2; exit 1; }

request() {
  local output
  if ! output="$(curl --fail-with-body -sS -X POST "${BASE_URL}/" \
    -H 'Content-Type: application/json' -H "Command: $1" --data "$2")"; then
    printf '%s\n' "${output}" >&2
    python3 - "${LOG}" <<'PY' >&2
import pathlib, sys
print(pathlib.Path(sys.argv[1]).read_text(errors="replace"))
PY
    return 1
  fi
  printf '%s' "${output}"
}

DEMO="$(request eval '{"response":"print(6 * 7)","answer":"","params":{"mode":"demo"}}')"
IO="$(request eval '{"response":"try:\n    n\nexcept NameError:\n    n = int(input())\nprint(n * n)","answer":"","params":{"mode":"io_test","tests":[{"input":"5\n","expected_output":"25\n"},{"inject":{"n":3},"expected_output":"9\n","hidden":true}]}}')"
UNIT="$(request eval '{"response":"def square(n):\n    return n * n","answer":"","params":{"mode":"unit_test","test_code":"def test_square():\n    assert square(5) == 25"}}')"
PREVIEW="$(request preview '{"response":"import socket","params":{}}')"
BLOCKED="$(request eval '{"response":"import socket","answer":"","params":{"mode":"demo"}}')"

python3 - "${DEMO}" "${IO}" "${UNIT}" "${PREVIEW}" "${BLOCKED}" <<'PY'
import json, sys

def unwrap(raw):
    value = json.loads(raw)
    return value.get("result", value)

demo, io_result, unit, preview, blocked = map(unwrap, sys.argv[1:])
assert demo["stdout"] == "42\n" and demo["is_correct"] is False
assert io_result["is_correct"] is True and io_result["passed"] == 2
assert io_result["tests"][1]["hidden"] is True and io_result["tests"][1]["passed"] is True
assert "actual" not in io_result["tests"][1] and "expected" not in io_result["tests"][1]
assert unit["is_correct"] is True and unit["tests"] == [{"name": "test_square", "passed": True}]
assert preview["valid"] is False and "socket" in preview["preview"]
assert blocked["is_correct"] is False and blocked["kind"] == "validation"
print(json.dumps({"demo": demo, "io_test": io_result, "unit_test": unit, "preview": preview}, sort_keys=True))
PY

TIMEOUT_BODY="${TMP}/timeout.json"
TIMEOUT_STATUS="$(curl -sS -o "${TIMEOUT_BODY}" -w '%{http_code}' -X POST "${BASE_URL}/" \
  -H 'Content-Type: application/json' -H 'Command: eval' \
  --data '{"response":"while True:\n    pass","answer":"","params":{"mode":"demo"}}')"
case "${TIMEOUT_STATUS}" in
  5??) ;;
  *) echo "expected timeout 5xx, got ${TIMEOUT_STATUS}" >&2; exit 1 ;;
esac
echo "timeout_http_status=${TIMEOUT_STATUS}"
python3 - "${TIMEOUT_BODY}" <<'PY'
import json, pathlib, sys
body = json.loads(pathlib.Path(sys.argv[1]).read_text())
text = json.dumps(body).lower()
assert any(term in text for term in ("deadline", "timeout", "closed")), body
PY

RECOVERY="$(request eval '{"response":"print(7 * 6)","answer":"","params":{"mode":"demo"}}')"
python3 - "${RECOVERY}" <<'PY'
import json, sys
value = json.loads(sys.argv[1])
value = value.get("result", value)
assert value["stdout"] == "42\n"
PY

echo "timeout_recovery=PASS"
echo "safe_eval_python_reactor_e2e=PASS"
