#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUNDLER="${ROOT}/tools/lf-bundle-python/lf_bundle_python.py"
ADAPTER="${ROOT}/tools/lf-bundle-python/adapter"
BOILERPLATE="${SHIMMY_LF_BOILERPLATE_ROOT:?set SHIMMY_LF_BOILERPLATE_ROOT}"
BOILERPLATE_SHA="${SHIMMY_LF_BOILERPLATE_SHA:?set SHIMMY_LF_BOILERPLATE_SHA}"
ARRAY_EQUAL="${SHIMMY_LF_ARRAY_EQUAL_ROOT:?set SHIMMY_LF_ARRAY_EQUAL_ROOT}"
ARRAY_EQUAL_SHA="${SHIMMY_LF_ARRAY_EQUAL_SHA:?set SHIMMY_LF_ARRAY_EQUAL_SHA}"
COMPARE_BOOLEAN="${SHIMMY_LF_COMPARE_BOOLEAN_ROOT:?set SHIMMY_LF_COMPARE_BOOLEAN_ROOT}"
COMPARE_BOOLEAN_SHA="${SHIMMY_LF_COMPARE_BOOLEAN_SHA:?set SHIMMY_LF_COMPARE_BOOLEAN_SHA}"
UTILS_WHEEL="${SHIMMY_LF_EVALUATION_UTILS_WHEEL:?set SHIMMY_LF_EVALUATION_UTILS_WHEEL}"
UTILS_SHA256="${SHIMMY_LF_EVALUATION_UTILS_SHA256:?set SHIMMY_LF_EVALUATION_UTILS_SHA256}"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/shimmy-lf-package-e2e.XXXXXX")"
trap 'rm -rf "${TMP}"' EXIT

for cmd in git python3 sha256sum; do
  command -v "${cmd}" >/dev/null 2>&1 || { echo "missing required command: ${cmd}" >&2; exit 1; }
done

verify_checkout() {
  local path="$1"
  local expected="$2"
  local actual
  [[ -d "${path}/.git" || -f "${path}/.git" ]] || { echo "not a Git checkout: ${path}" >&2; exit 1; }
  actual="$(git -C "${path}" rev-parse HEAD)"
  [[ "${actual}" == "${expected}" ]] || { echo "checkout ${path} is ${actual}, expected ${expected}" >&2; exit 1; }
  [[ -z "$(git -C "${path}" status --porcelain)" ]] || { echo "checkout is dirty: ${path}" >&2; exit 1; }
}

verify_checkout "${BOILERPLATE}" "${BOILERPLATE_SHA}"
verify_checkout "${ARRAY_EQUAL}" "${ARRAY_EQUAL_SHA}"
verify_checkout "${COMPARE_BOOLEAN}" "${COMPARE_BOOLEAN_SHA}"
printf '%s  %s\n' "${UTILS_SHA256}" "${UTILS_WHEEL}" | sha256sum -c -

UTILS_ROOT="${TMP}/evaluation-function-utils"
mkdir -p "${UTILS_ROOT}"
python3 -m zipfile -e "${UTILS_WHEEL}" "${UTILS_ROOT}"

BOILERPLATE_BUNDLE="${TMP}/boilerplate.bundle.py"
python3 "${BUNDLER}" \
  --root "${BOILERPLATE}" \
  --adapter-root "${ADAPTER}" \
  --eval-entrypoint evaluation_function.evaluation:evaluation_function \
  --preview-entrypoint evaluation_function.preview:preview_function \
  --out "${BOILERPLATE_BUNDLE}"

SHIMMY_E2E_EVALUATOR="${BOILERPLATE_BUNDLE}" \
SHIMMY_E2E_EXPECTATION=boilerplate \
  "${ROOT}/scripts/e2e-python-reactor.sh"

ARRAY_BUNDLE="${TMP}/array-equal.bundle.py"
python3 "${BUNDLER}" \
  --root "${ARRAY_EQUAL}/app" \
  --adapter-root "${ADAPTER}" \
  --include-root "${UTILS_ROOT}" \
  --runtime-module numpy \
  --eval-entrypoint evaluation:evaluation_function \
  --out "${ARRAY_BUNDLE}"

SHIMMY_E2E_EVALUATOR="${ARRAY_BUNDLE}" \
SHIMMY_E2E_EXPECTATION=array-equal \
  "${ROOT}/scripts/e2e-python-reactor.sh"

COMPARE_BUNDLE="${TMP}/compare-boolean.bundle.py"
set +e
COMPARE_OUTPUT="$(python3 "${BUNDLER}" \
  --root "${COMPARE_BOOLEAN}" \
  --adapter-root "${ADAPTER}" \
  --eval-entrypoint evaluation_function.evaluation:evaluation_function \
  --preview-entrypoint evaluation_function.preview:preview_function \
  --out "${COMPARE_BUNDLE}" 2>&1)"
COMPARE_STATUS=$?
set -e
[[ ${COMPARE_STATUS} -ne 0 ]] || { echo "compareBoolean unexpectedly bundled without SymPy" >&2; exit 1; }
[[ ! -e "${COMPARE_BUNDLE}" ]] || { echo "failed dependency check still wrote a bundle" >&2; exit 1; }
case "${COMPARE_OUTPUT}" in
  *"unresolved imports: sympy"*) ;;
  *) printf '%s\n' "${COMPARE_OUTPUT}" >&2; echo "compareBoolean did not report missing SymPy" >&2; exit 1 ;;
esac
printf '%s\n' "${COMPARE_OUTPUT}"

printf 'PASS: real Lambda Feedback package E2E\n'
printf 'sources: boilerplate=%s array-equal=%s compare-boolean=%s\n' \
  "${BOILERPLATE_SHA}" "${ARRAY_EQUAL_SHA}" "${COMPARE_BOOLEAN_SHA}"
