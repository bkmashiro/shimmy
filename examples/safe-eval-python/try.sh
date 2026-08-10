#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
REQUESTS="${ROOT}/examples/safe-eval-python/requests"
PROFILE="${1:-base}"
BASE_URL="${2:-http://127.0.0.1:8080}"
CURL_TIMEOUT=15
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

case "${PROFILE}" in
  base|numpy-core) ;;
  sympy) CURL_TIMEOUT=45 ;;
  *) echo "profile must be base, numpy-core, or sympy" >&2; exit 2 ;;
esac

echo "Waiting for ${BASE_URL} ..."
python3 - "${BASE_URL}" <<'PY'
import socket
import sys
import time
import urllib.parse

url = urllib.parse.urlsplit(sys.argv[1])
if url.scheme != "http" or not url.hostname:
    raise SystemExit("quick start URL must be an http:// URL with a host")
port = url.port or 80
deadline = time.monotonic() + 90
while True:
    try:
        with socket.create_connection((url.hostname, port), timeout=0.5):
            break
    except OSError as error:
        if time.monotonic() >= deadline:
            raise SystemExit(f"server did not become ready within 90 seconds: {error}")
        time.sleep(0.5)
PY

post() {
  local label="$1" command="$2" request="$3" assertion="$4"
  local response="${TMP}/${assertion}.json"
  echo
  echo "== ${label} =="
  echo "request: ${request#"${ROOT}"/}"
  curl --fail-with-body --max-time "${CURL_TIMEOUT}" -sS \
    -X POST "${BASE_URL}/" \
    -H 'Content-Type: application/json' \
    -H "Command: ${command}" \
    --data-binary "@${request}" \
    -o "${response}"
  python3 - "${response}" "${assertion}" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
assertion = sys.argv[2]
value = json.loads(path.read_text())
result = value.get("result", value)

checks = {
    "demo": lambda r: r.get("stdout") == "42\n" and r.get("is_correct") is False,
    "io-pass": lambda r: r.get("is_correct") is True and r.get("passed") == 2,
    "io-fail": lambda r: r.get("is_correct") is False and r.get("passed") == 1,
    "unit": lambda r: r.get("is_correct") is True and r.get("passed") == 2,
    "preview": lambda r: r.get("valid") is False and "socket" in r.get("preview", ""),
    "profile": lambda r: r.get("is_correct") is True and r.get("passed") == 1,
}
if assertion not in checks or not checks[assertion](result):
    print(json.dumps(value, indent=2, sort_keys=True))
    raise SystemExit(f"unexpected response for {assertion}")
print(json.dumps(value, indent=2, sort_keys=True))
PY
}

post "demo" eval "${REQUESTS}/demo.json" demo
post "passing I/O tests (including one hidden test)" eval "${REQUESTS}/io-tests-pass.json" io-pass
post "failing I/O test feedback" eval "${REQUESTS}/io-tests-fail.json" io-fail
post "unit tests" eval "${REQUESTS}/unit-tests.json" unit
post "preview rejects a blocked host capability" preview "${REQUESTS}/preview-blocked.json" preview

case "${PROFILE}" in
  numpy-core)
    post "NumPy profile" eval "${REQUESTS}/numpy-core.json" profile
    ;;
  sympy)
    post "SymPy profile" eval "${REQUESTS}/sympy.json" profile
    ;;
esac

echo
echo "Quick start completed for profile: ${PROFILE}"
