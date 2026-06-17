#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/python-reactor-artifact.env
source "${ROOT}/scripts/python-reactor-artifact.env"

need() { command -v "$1" >/dev/null 2>&1; }

sha256_file() {
  if need sha256sum; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

usage() {
  cat >&2 <<EOF
usage: $0 [path/to/python-reactor.wasm]

Verifies the pinned python-reactor.wasm artifact hash and required exports.
If no path is provided, verifies internal/execution/wasm/testdata/python-reactor.wasm.
If the target file is missing, downloads ${SHIMMY_REACTOR_URL} first.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

wasm="${1:-${ROOT}/internal/execution/wasm/testdata/${SHIMMY_REACTOR_ASSET}}"
if [[ ! -f "${wasm}" ]]; then
  echo "==> ${wasm} missing; downloading ${SHIMMY_REACTOR_URL}"
  mkdir -p "$(dirname "${wasm}")"
  if need curl; then
    curl -fL "${SHIMMY_REACTOR_URL}" -o "${wasm}"
  elif need wget; then
    wget -O "${wasm}" "${SHIMMY_REACTOR_URL}"
  else
    echo "error: curl or wget is required to download ${SHIMMY_REACTOR_URL}" >&2
    exit 1
  fi
fi

actual="$(sha256_file "${wasm}")"
echo "==> artifact: ${wasm}"
echo "    release: ${SHIMMY_REACTOR_REPO}@${SHIMMY_REACTOR_VERSION}"
echo "    sha256:  ${actual}"
if [[ "${actual}" != "${SHIMMY_REACTOR_SHA256}" ]]; then
  echo "error: sha256 mismatch; expected ${SHIMMY_REACTOR_SHA256}" >&2
  exit 1
fi

python3 - "${wasm}" <<'PY'
import sys
from pathlib import Path

path = Path(sys.argv[1])
data = path.read_bytes()
required = {"py_init", "evaluate", "py_exec", "alloc", "dealloc", "resp_buf", "resp_len"}

pos = 8
if data[:4] != b"\0asm":
    raise SystemExit(f"error: {path} is not a WebAssembly module")

def read_u32(i):
    shift = 0
    result = 0
    while True:
        b = data[i]
        i += 1
        result |= (b & 0x7F) << shift
        if not (b & 0x80):
            return result, i
        shift += 7

def read_name(i):
    n, i = read_u32(i)
    s = data[i:i+n].decode("utf-8", "replace")
    return s, i+n

exports = set()
while pos < len(data):
    sec_id = data[pos]
    pos += 1
    sec_len, pos = read_u32(pos)
    end = pos + sec_len
    if sec_id == 7:
        count, pos = read_u32(pos)
        for _ in range(count):
            name, pos = read_name(pos)
            kind = data[pos]
            pos += 1
            _, pos = read_u32(pos)
            if kind == 0:
                exports.add(name)
        break
    pos = end

missing = sorted(required - exports)
print("    exports: ", ", ".join(sorted(exports & required)))
if missing:
    raise SystemExit("error: missing required exports: " + ", ".join(missing))
PY

echo "    ✓ python-reactor artifact verified"
