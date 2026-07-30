#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="${1:-artifact-only}"
# shellcheck source=scripts/python-reactor-artifact.env
source "${ROOT}/scripts/python-reactor-artifact.env"

case "${MODE}" in
  artifact-only|direct) ;;
  -h|--help)
    printf 'usage: %s [artifact-only|direct]\n' "$0"
    exit 0
    ;;
  *) exit 2 ;;
esac

cd "${ROOT}"
scripts/verify-python-reactor-artifact.sh
bash -n scripts/smoke-python-reactor-handoff.sh scripts/verify-python-reactor-artifact.sh
go test ./internal/execution/wasm \
  -run='^(TestVerifyShimmyPython|TestShimmyPythonRequest)' -count=1
go test ./internal/execution \
  -run='TestNewDispatcher_.*Wasm|TestNewDispatcher_.*Reactor|TestScriptRouting' -count=1
GOOS=linux GOARCH=amd64 go test -c ./internal/execution/wasm -o /tmp/shimmy-wasm-python.test
rm -f /tmp/shimmy-wasm-python.test

if [[ "${MODE}" == "direct" ]]; then
  SHIMMY_PYTHON_RUNTIME_ARTIFACT="${SHIMMY_REACTOR_WASM}" \
  SHIMMY_PYTHON_RUNTIME_MANIFEST="${SHIMMY_REACTOR_MANIFEST_PATH}" \
  SHIMMY_PYTHON_EXPECTED_COMMIT="${SHIMMY_REACTOR_EXPECTED_COMMIT}" \
    go test ./internal/execution/wasm \
      -run='^TestShimmyPythonArtifactE2E$' -count=1 -v -timeout=15m
fi

printf 'PASS: Shimmy Python runtime smoke (%s)\n' "${MODE}"
