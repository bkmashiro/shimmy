#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WASM="${1:-${SHIMMY_PYTHON_REACTOR_WASM:-}}"
MANIFEST="${2:-${SHIMMY_PYTHON_REACTOR_MANIFEST:-}}"
PORT="${SHIMMY_SAFE_EVAL_PORT:-8080}"
HOST="${SHIMMY_SAFE_EVAL_HOST:-127.0.0.1}"
EVALUATOR="${SHIMMY_SAFE_EVAL_EVALUATOR:-${ROOT}/examples/safe-eval-python/safe_eval.py}"

usage() {
  cat >&2 <<'EOF'
Usage:
  examples/safe-eval-python/serve.sh /path/to/runtime.wasm /path/to/manifest.json

Or set:
  SHIMMY_PYTHON_REACTOR_WASM=/path/to/runtime.wasm
  SHIMMY_PYTHON_REACTOR_MANIFEST=/path/to/manifest.json

Optional:
  SHIMMY_SAFE_EVAL_HOST=127.0.0.1
  SHIMMY_SAFE_EVAL_PORT=8080
  SHIMMY_SAFE_EVAL_TIMEOUT=10s  # optional; profile-aware default otherwise
  SHIMMY_BIN=/path/to/shimmy
  SHIMMY_ARTIFACT_CHECK_BIN=/path/to/shimmy-artifact-check
EOF
  exit 2
}

[[ -n "${WASM}" && -n "${MANIFEST}" ]] || usage
[[ -r "${WASM}" ]] || { echo "runtime artifact is not readable: ${WASM}" >&2; exit 2; }
[[ -r "${MANIFEST}" ]] || { echo "runtime manifest is not readable: ${MANIFEST}" >&2; exit 2; }
[[ -r "${EVALUATOR}" ]] || { echo "evaluator is not readable: ${EVALUATOR}" >&2; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "Python 3 is required by the quick-start launcher" >&2; exit 2; }

if [[ -n "${SHIMMY_ARTIFACT_CHECK_BIN:-}" ]]; then
  [[ -x "${SHIMMY_ARTIFACT_CHECK_BIN}" ]] || { echo "artifact checker is not executable: ${SHIMMY_ARTIFACT_CHECK_BIN}" >&2; exit 2; }
  CHECK_CMD=("${SHIMMY_ARTIFACT_CHECK_BIN}")
else
  command -v go >/dev/null 2>&1 || { echo "Go is required unless SHIMMY_ARTIFACT_CHECK_BIN is set" >&2; exit 2; }
  CHECK_CMD=(go run ./cmd/shimmy-artifact-check)
fi

if [[ -n "${SHIMMY_BIN:-}" ]]; then
  [[ -x "${SHIMMY_BIN}" ]] || { echo "Shimmy binary is not executable: ${SHIMMY_BIN}" >&2; exit 2; }
  SHIMMY_CMD=("${SHIMMY_BIN}")
else
  command -v go >/dev/null 2>&1 || { echo "Go is required unless SHIMMY_BIN is set" >&2; exit 2; }
  SHIMMY_CMD=(go run .)
fi

cd "${ROOT}"
echo "Validating Python Reactor artifact and manifest..."
"${CHECK_CMD[@]}" -profile python-reactor -module "${WASM}" -manifest "${MANIFEST}"
ARTIFACT_PROFILE="$(python3 - "${MANIFEST}" <<'PY'
import json
import pathlib
import sys

print(json.loads(pathlib.Path(sys.argv[1]).read_text()).get("profile", ""))
PY
)"
case "${ARTIFACT_PROFILE}" in
  base|numpy-core) DEFAULT_TIMEOUT=5s ;;
  sympy) DEFAULT_TIMEOUT=30s ;;
  *) echo "unsupported Python Reactor profile in manifest: ${ARTIFACT_PROFILE:-<missing>}" >&2; exit 2 ;;
esac
WORKER_TIMEOUT="${SHIMMY_SAFE_EVAL_TIMEOUT:-${DEFAULT_TIMEOUT}}"

echo
echo "safe-eval-python (${ARTIFACT_PROFILE}) is starting at http://${HOST}:${PORT}"
echo "onboarding worker deadline: ${WORKER_TIMEOUT}"
echo "In another terminal run:"
echo "  examples/safe-eval-python/try.sh ${ARTIFACT_PROFILE} http://${HOST}:${PORT}"
echo

exec env \
  FUNCTION_INTERFACE=wasm \
  FUNCTION_WASM_PROFILE=python-reactor \
  FUNCTION_WASM_MODULE="${WASM}" \
  FUNCTION_WASM_MANIFEST="${MANIFEST}" \
  FUNCTION_WASM_PYTHON_SCRIPT="${EVALUATOR}" \
  FUNCTION_WASM_PYTHON_LIFECYCLE=snapshot \
  FUNCTION_WASM_SNAPSHOT_MODE=memcpy \
  FUNCTION_WASM_ALLOWED_PATHS= \
  FUNCTION_MAX_PROCS=1 \
  FUNCTION_WORKER_SEND_TIMEOUT="${WORKER_TIMEOUT}" \
  "${SHIMMY_CMD[@]}" --worker-send-timeout "${WORKER_TIMEOUT}" serve --host "${HOST}" --port "${PORT}"
