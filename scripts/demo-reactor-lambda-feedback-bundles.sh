#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARTIFACT_DIR="${SHIMMY_REACTOR_ARTIFACT_DIR:-/tmp/shimmy-reactor-artifacts}"
REACTOR_VERSION="${SHIMMY_REACTOR_VERSION:-v1.0.11}"
REACTOR_WASM="${PYTHON_REACTOR_WASM:-${ARTIFACT_DIR}/python-reactor-${REACTOR_VERSION}.wasm}"
EXPECTED_SHA256="${SHIMMY_REACTOR_SHA256:-c3e2de93090544a47b1b63fa5f97060cfcd4bd87d4dff96d79a9c5bfa94589a3}"
PURE_DEPS="${SHIMMY_LF_PURE_PY_DEPS:-${ARTIFACT_DIR}/pure-python-deps}"
POLYFILLS="${ROOT}/tools/lf-bundle-python/polyfills/reactor"
GO_IMAGE="${SHIMMY_REACTOR_GO_IMAGE:-golang:1.24}"

need() {
  command -v "$1" >/dev/null 2>&1
}

sha256_file() {
  if need sha256sum; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

fetch_reactor() {
  mkdir -p "${ARTIFACT_DIR}"
  if [[ -f "${REACTOR_WASM}" ]]; then
    return 0
  fi

  local url="https://github.com/bkmashiro/webassembly-language-runtimes/releases/download/${REACTOR_VERSION}/python-reactor.wasm"
  echo "==> downloading ${url}"
  if need curl; then
    curl -fL "${url}" -o "${REACTOR_WASM}"
  elif need wget; then
    wget -O "${REACTOR_WASM}" "${url}"
  else
    echo "error: curl or wget is required to download ${url}" >&2
    exit 1
  fi
}

verify_reactor() {
  fetch_reactor
  local actual
  actual="$(sha256_file "${REACTOR_WASM}")"
  echo "==> reactor artifact: ${REACTOR_WASM}"
  echo "    sha256: ${actual}"
  if [[ "${actual}" != "${EXPECTED_SHA256}" ]]; then
    echo "error: reactor sha256 mismatch; expected ${EXPECTED_SHA256}" >&2
    exit 1
  fi
}

prepare_pure_deps() {
  if [[ -d "${PURE_DEPS}/mpmath" ]]; then
    echo "==> pure Python deps already present: ${PURE_DEPS}"
    return 0
  fi

  rm -rf "${PURE_DEPS}"
  mkdir -p "${PURE_DEPS}"
  echo "==> installing pure Python deps for bundle: mpmath -> ${PURE_DEPS}"
  if need uv; then
    uv pip install --target "${PURE_DEPS}" mpmath
  elif python3 -m pip --version >/dev/null 2>&1; then
    python3 -m pip install --target "${PURE_DEPS}" mpmath
  else
    echo "error: uv or python3 -m pip is required to install pure Python bundle deps" >&2
    exit 1
  fi
}

run_direct() {
  echo "==> running reactor bundle matrix directly"
  PYTHON_REACTOR_WASM="${REACTOR_WASM}" \
  SHIMMY_LF_PURE_PY_DEPS="${PURE_DEPS}" \
  SHIMMY_LF_REACTOR_POLYFILLS="${POLYFILLS}" \
  go test ./internal/execution/wasm -run TestReactorPythonRunner_LambdaFeedbackBundleMatrix -v
}

run_docker() {
  if ! need docker; then
    echo "error: docker is required for docker mode" >&2
    exit 1
  fi
  echo "==> running reactor bundle matrix in Docker (${GO_IMAGE})"
  docker run --rm \
    -v "${ROOT}":/repo \
    -v "${ARTIFACT_DIR}":/artifacts \
    -w /repo \
    "${GO_IMAGE}" \
    bash -lc 'set -euo pipefail; export PATH=/usr/local/go/bin:$PATH; PYTHON_REACTOR_WASM=/artifacts/'"$(basename "${REACTOR_WASM}")"' SHIMMY_LF_PURE_PY_DEPS=/artifacts/'"$(basename "${PURE_DEPS}")"' SHIMMY_LF_REACTOR_POLYFILLS=/repo/tools/lf-bundle-python/polyfills/reactor go test ./internal/execution/wasm -run TestReactorPythonRunner_LambdaFeedbackBundleMatrix -v'
}

main() {
  local mode="${1:-docker}"
  verify_reactor
  prepare_pure_deps

  case "${mode}" in
    docker)
      run_docker
      ;;
    direct)
      run_direct
      ;;
    *)
      echo "usage: $0 [docker|direct]" >&2
      exit 1
      ;;
  esac
}

main "$@"
