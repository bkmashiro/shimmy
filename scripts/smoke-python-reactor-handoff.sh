#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="${1:-artifact-only}"

usage() {
  cat >&2 <<'EOF'
usage: scripts/smoke-python-reactor-handoff.sh [artifact-only|direct|docker]

artifact-only  Verify pinned artifact + compile/routing tests. Fast and macOS-safe.
direct         Also run the Linux host Lambda Feedback bundle matrix directly.
docker         Also run the Lambda Feedback bundle matrix inside golang:1.24 Docker.
EOF
}

case "${MODE}" in
  artifact-only|direct|docker) ;;
  -h|--help) usage; exit 0 ;;
  *) usage; exit 1 ;;
esac

cd "${ROOT}"

echo "==> verifying pinned python-reactor artifact"
scripts/verify-python-reactor-artifact.sh

echo "==> shell syntax checks"
bash -n scripts/demo-python-examples.sh \
  scripts/demo-reactor-lambda-feedback-bundles.sh \
  scripts/verify-python-reactor-artifact.sh

echo "==> dispatcher routing tests"
go test ./internal/execution -run 'TestNewDispatcher_.*Wasm|TestNewDispatcher_.*Reactor|TestScriptRouting' -v

echo "==> linux compile gate for wasm package"
GOOS=linux GOARCH=amd64 go test -c ./internal/execution/wasm -o /tmp/shimmy-wasm.test

if [[ "${MODE}" == "artifact-only" ]]; then
  echo "==> artifact-only smoke complete"
  exit 0
fi

echo "==> Lambda Feedback reactor bundle matrix (${MODE})"
scripts/demo-reactor-lambda-feedback-bundles.sh "${MODE}"
