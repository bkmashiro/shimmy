#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ADAPTER_DIR="$ROOT_DIR/examples/lambda-feedback-adapter"
FIXTURE_ROOT="$ROOT_DIR/examples/lambda-feedback-fixtures/boilerplate-python"
WORKER="$ADAPTER_DIR/lf_file_worker.py"
PYTHON_BIN="${PYTHON_BIN:-$(command -v python3)}"

usage() {
  cat <<'USAGE'
Usage: scripts/demo-lambda-feedback-file-worker.sh [direct|http|all]

Runs a Lambda Feedback-compatible Python evaluator through Shimmy's existing
file worker interface. No production runtime server is hidden in the toolkit shim:
the worker reads Shimmy file-IO JSON, calls module:function, and writes Shimmy's
schema-compatible response envelope.
USAGE
}

run_direct() {
  local tmp req res
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  req="$tmp/request.json"
  res="$tmp/response.json"
  cat >"$req" <<JSON
{"command":"eval","params":{"response":"42","answer":"42","params":{"root":"$FIXTURE_ROOT","entrypoint":"evaluation_function.main:evaluation_function"}}}
JSON
  EVAL_FILE_NAME_REQUEST="$req" EVAL_FILE_NAME_RESPONSE="$res" "$PYTHON_BIN" "$WORKER" >/dev/null
  cat "$res"
}

run_http() {
  local port server_pid response preview
  if [[ -n "${PORT:-}" ]]; then
    port="$PORT"
  else
    port="$($PYTHON_BIN -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
  fi
  (
    cd "$ROOT_DIR"
    exec go run . \
      --log-level error \
      --interface file \
      --command "$PYTHON_BIN" \
      --arg "$WORKER" \
      --env "FUNCTION_LF_ROOT=$FIXTURE_ROOT" \
      --env "FUNCTION_LF_ENTRYPOINT=evaluation_function.main:evaluation_function" \
      --env "FUNCTION_LF_PREVIEW_ENTRYPOINT=evaluation_function.main:preview_function" \
      serve \
      --host 127.0.0.1 \
      --port "$port"
  ) &
  server_pid=$!
  trap 'kill "$server_pid" 2>/dev/null || true' RETURN

  for _ in $(seq 1 80); do
    if curl -fsS "http://127.0.0.1:$port/health" >/dev/null 2>&1; then
      break
    fi
    sleep 0.25
  done

  response="$(curl -fsS \
    -H 'content-type: application/json' \
    -d '{"response":"42","answer":"42","params":{}}' \
    "http://127.0.0.1:$port/")"
  preview="$(curl -fsS \
    -H 'content-type: application/json' \
    -H 'command: preview' \
    -d '{"response":"draft","params":{"expected":"42"}}' \
    "http://127.0.0.1:$port/")"

  printf '%s\n%s\n' "$response" "$preview"
}

mode="${1:-all}"
case "$mode" in
  direct) run_direct ;;
  http) run_http ;;
  all) run_direct; run_http ;;
  -h|--help|help) usage ;;
  *) usage >&2; exit 2 ;;
esac
