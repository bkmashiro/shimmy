#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUNNER="${ROOT}/examples/lambda-feedback-adapter/run_lf_eval.py"
FIXTURES="${ROOT}/examples/lambda-feedback-fixtures"
PYTHON="${PYTHON:-python3}"

usage() {
  cat <<'EOF'
Usage: scripts/demo-lambda-feedback-fixtures.sh [list|local|all]

Runs small Lambda Feedback compatibility fixtures through the backend-independent
local adapter. These demos validate evaluator package shapes before wiring the
same adapter into Pyodide or a Python reactor runtime.
EOF
}

case "${1:-all}" in
  list)
    find "${FIXTURES}" -mindepth 1 -maxdepth 1 -type d -print | sort | sed "s#${FIXTURES}/##"
    ;;
  local|all)
    echo "==> Boilerplate eval fixture"
    "${PYTHON}" "${RUNNER}" \
      --root "${FIXTURES}/boilerplate-python" \
      --entrypoint evaluation_function.main:evaluation_function \
      --method eval \
      --response " 42 " \
      --answer "42" \
      --params-json '{}'

    echo "==> Boilerplate preview fixture"
    "${PYTHON}" "${RUNNER}" \
      --root "${FIXTURES}/boilerplate-python" \
      --entrypoint evaluation_function.main:preview_function \
      --method preview \
      --response "draft" \
      --answer "answer" \
      --params-json '{"expected":"answer"}'

    echo "==> Relative import + two-argument preview fixture"
    "${PYTHON}" "${RUNNER}" \
      --root "${FIXTURES}/relative-preview" \
      --entrypoint evaluation_function.evaluation:preview_function \
      --method preview \
      --response " Foo " \
      --params-json '{"mode":"set"}'

    echo "==> Relative import set-compare eval fixture"
    "${PYTHON}" "${RUNNER}" \
      --root "${FIXTURES}/relative-preview" \
      --entrypoint evaluation_function.evaluation:evaluation_function \
      --method eval \
      --response "a,b" \
      --answer "b, a" \
      --params-json '{"mode":"set"}'

    echo "✅ Lambda Feedback local compatibility fixtures passed"
    ;;
  -h|--help|help)
    usage
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
