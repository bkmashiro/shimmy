#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOST="127.0.0.1"
BIN="${ROOT}/bin/shimmy-demo"
LOG_DIR="${ROOT}/.demo-logs"
mkdir -p "${LOG_DIR}"

need() { command -v "$1" >/dev/null 2>&1; }

port() {
  python3 - <<'PY'
import socket
s = socket.socket(); s.bind(('127.0.0.1', 0)); print(s.getsockname()[1]); s.close()
PY
}

build_shimmy() {
  echo "==> Building shimmy demo binary"
  (cd "${ROOT}" && go build -trimpath -buildvcs=false -o "${BIN}" .)
}

run_wasm_case() {
  local name="$1" wasm="$2" response="$3" answer="$4" params_json
  if [[ $# -ge 5 ]]; then
    params_json="$5"
  else
    params_json='{}'
  fi
  local p base log pid resp count ok
  p="$(port)"
  base="http://${HOST}:${p}"
  log="${LOG_DIR}/${name}.log"
  rm -f "${log}"

  echo
  echo "==> ${name}"
  echo "    module: ${wasm}"
  printf '    sample: response=%q, answer=%q, params=%s\n' "${response}" "${answer}" "${params_json}"

  (
    cd "${ROOT}"
    exec env \
      LOG_LEVEL=error \
      FUNCTION_INTERFACE=wasm \
      FUNCTION_COMMAND="${wasm}" \
      FUNCTION_MAX_PROCS=1 \
      FUNCTION_TIMEOUT=5s \
      "${BIN}" serve --host "${HOST}" --port "${p}"
  ) >"${log}" 2>&1 &
  pid="$!"

  cleanup() {
    if kill -0 "${pid}" 2>/dev/null; then
      kill "${pid}" 2>/dev/null || true
      for _ in {1..20}; do kill -0 "${pid}" 2>/dev/null || return 0; sleep 0.1; done
      kill -KILL "${pid}" 2>/dev/null || true
      wait "${pid}" 2>/dev/null || true
    fi
  }

  ok=0
  for _ in {1..60}; do
    if ! kill -0 "${pid}" 2>/dev/null; then
      echo "    FAILED: server exited early; log: ${log}" >&2
      sed -n '1,80p' "${log}" >&2 || true
      return 1
    fi
    if curl -fsS "${base}/health" >/dev/null 2>&1; then ok=1; break; fi
    sleep 0.2
  done
  if [[ "${ok}" != 1 ]]; then
    echo "    FAILED: server did not become ready; log: ${log}" >&2
    cleanup
    return 1
  fi

  resp="$(RESPONSE="${response}" ANSWER="${answer}" PARAMS_JSON="${params_json}" BASE="${base}" python3 - <<'PY'
import json, os, subprocess
payload = {
    "response": os.environ["RESPONSE"],
    "answer": os.environ["ANSWER"],
    "params": json.loads(os.environ["PARAMS_JSON"]),
}
cmd = [
    "curl", "-fsS", "-X", "POST", os.environ["BASE"] + "/",
    "-H", "Content-Type: application/json",
    "-H", "Command: eval",
    "--data", json.dumps(payload),
]
print(subprocess.check_output(cmd, text=True))
PY
)"
  echo "${resp}" | python3 -m json.tool
  RESP="${resp}" python3 - <<'PY'
import json, os, sys
body = json.loads(os.environ["RESP"])
result = body.get("result", {})
if result.get("is_correct") is not True:
    print("    FAILED: expected is_correct=true", file=sys.stderr)
    sys.exit(1)
print("    ✓ accepted sample input")
PY
  cleanup
}

build_go_wasm() {
  local dir="$1"
  (cd "${dir}" && GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o eval.wasm .)
}

main() {
  if ! need go || ! need curl || ! need python3; then
    echo "error: go, curl, and python3 are required" >&2
    exit 1
  fi

  build_shimmy

  echo "==> Building Go/WASI examples"
  build_go_wasm "${ROOT}/examples/demo-stateful"
  build_go_wasm "${ROOT}/examples/eval-go"

  run_wasm_case \
    "go-demo-stateful-snapshot" \
    "${ROOT}/examples/demo-stateful/eval.wasm" \
    "42" "42" "{}"

  run_wasm_case \
    "go-eval-simple-equality" \
    "${ROOT}/examples/eval-go/eval.wasm" \
    "hello" "hello" "{}"

  if need cargo && need rustup; then
    echo "==> Building Rust/WASI example"
    (cd "${ROOT}/examples/eval-rust" && make)
    run_wasm_case \
      "rust-eval-simple-equality" \
      "${ROOT}/examples/eval-rust/eval.wasm" \
      "rust" "rust" "{}"
  else
    echo "==> Skipping Rust example: cargo/rustup not installed"
  fi

  if [[ -x /opt/wasi-sdk/bin/clang ]]; then
    echo "==> Building C/WASI example"
    (cd "${ROOT}/examples/eval-c" && make)
    run_wasm_case \
      "c-eval-simple-equality" \
      "${ROOT}/examples/eval-c/eval.wasm" \
      "c" "c" "{}"
  else
    echo "==> Skipping C example: /opt/wasi-sdk/bin/clang not installed"
  fi

  if [[ -x /opt/wasi-sdk/bin/clang++ ]]; then
    echo "==> Building C++/WASI example"
    (cd "${ROOT}/examples/eval-cpp" && make)
    run_wasm_case \
      "cpp-eval-simple-equality" \
      "${ROOT}/examples/eval-cpp/eval.wasm" \
      "cpp" "cpp" "{}"
  else
    echo "==> Skipping C++ example: /opt/wasi-sdk/bin/clang++ not installed"
  fi

  echo
  echo "✅ Scenario demo completed. Logs: ${LOG_DIR}"
  echo "Note: Python reactor demos require Linux in this branch; see docs/demo-scenarios.md."
}

main "$@"
