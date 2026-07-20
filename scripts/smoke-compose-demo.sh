#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="${ROOT}/demo/compose/compose.yaml"
GENERIC_PORT="${SHIMMY_DEMO_GENERIC_PORT:-18081}"
REACTOR_PORT="${SHIMMY_DEMO_REACTOR_PORT:-18082}"
PYODIDE_PORT="${SHIMMY_DEMO_PYODIDE_PORT:-18083}"
DBI_PORT="${SHIMMY_DEMO_DBI_PORT:-18084}"
NO_BUILD=0
KEEP="${SHIMMY_DEMO_KEEP:-0}"

usage() {
  printf 'usage: %s [--no-build]\n' "$0" >&2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-build) NO_BUILD=1 ;;
    -h|--help) usage; exit 0 ;;
    *) usage; exit 2 ;;
  esac
  shift
done

for command in docker curl python3; do
  command -v "${command}" >/dev/null || { echo "missing command: ${command}" >&2; exit 1; }
done
docker compose version >/dev/null

compose=(docker compose -f "${COMPOSE_FILE}")
services=(generic python-reactor pyodide-scipy)
server_arch="$(docker info --format '{{.Architecture}}')"
include_dbi="${SHIMMY_DEMO_INCLUDE_DBI:-auto}"
if [[ "${include_dbi}" == "auto" ]]; then
  case "${server_arch}" in
    amd64|x86_64) include_dbi=1 ;;
    *) include_dbi=0 ;;
  esac
fi
if [[ "${include_dbi}" == "1" ]]; then
  services+=(dbi-lean)
else
  echo "==> DBI + Lean skipped on Docker server architecture ${server_arch}; run it on native linux/amd64 or GitHub Actions."
fi

cleanup() {
  status=$?
  if [[ ${status} -ne 0 ]]; then
    echo "==> compose logs after failure" >&2
    "${compose[@]}" logs --no-color --tail=200 "${services[@]}" >&2 || true
  fi
  if [[ "${KEEP}" != "1" ]]; then
    "${compose[@]}" down --remove-orphans >/dev/null 2>&1 || true
  fi
  exit "${status}"
}
trap cleanup EXIT

export SHIMMY_DEMO_COMMIT="${SHIMMY_DEMO_COMMIT:-$(git -C "${ROOT}" rev-parse --short HEAD 2>/dev/null || printf local)}"

if [[ ${NO_BUILD} -eq 0 ]]; then
  echo "==> building compact demo images: ${services[*]}"
  "${compose[@]}" build "${services[@]}"
fi

echo "==> starting compact demo stack"
"${compose[@]}" up -d --no-build "${services[@]}"

echo "==> image sizes"
for service in "${services[@]}"; do
  image_id="$("${compose[@]}" images -q "${service}")"
  image_bytes="$(docker image inspect "${image_id}" --format '{{.Size}}')"
  SERVICE="${service}" IMAGE_BYTES="${image_bytes}" python3 - <<'PY'
import os
size = int(os.environ["IMAGE_BYTES"]) / (1024 * 1024)
print(f"    {os.environ['SERVICE']}: {size:.1f} MiB")
PY
done

echo "==> offline Pyodide/SciPy dependency probe"
pyodide_image_id="$("${compose[@]}" images -q pyodide-scipy)"
docker run --rm --network none --read-only --tmpfs /tmp:size=256m,mode=1777 \
  --entrypoint node "${pyodide_image_id}" -e \
  "(async()=>{const {loadPyodide}=require('/opt/pyodide/node_modules/pyodide');const p=await loadPyodide({indexURL:'/opt/pyodide/node_modules/pyodide/'});await p.loadPackage(['scipy']);const v=p.runPython('from scipy import stats; float(stats.ttest_1samp([4.9,5.1,5.0,5.2,4.8],5.0).pvalue)');if(!(v>0))process.exit(1);console.log('    offline-scipy: PASS p='+v);})().catch(e=>{console.error(e);process.exit(1)})"

wait_health() {
  local name="$1" port="$2" attempts="$3"
  local url="http://127.0.0.1:${port}/health"
  for ((i=1; i<=attempts; i++)); do
    if curl -fsS "${url}" >/dev/null 2>&1; then
      echo "    ${name}: healthy"
      return 0
    fi
    sleep 2
  done
  echo "${name} did not become healthy at ${url}" >&2
  return 1
}

post_eval() {
  local port="$1" payload="$2"
  curl -fsS -X POST "http://127.0.0.1:${port}/" \
    -H 'Content-Type: application/json' \
    -H 'Command: eval' \
    --data "${payload}"
}

assert_response() {
  local lane="$1" payload="$2"
  LANE="${lane}" PAYLOAD="${payload}" python3 - <<'PY'
import json
import os
import sys
lane = os.environ["LANE"]
body = json.loads(os.environ["PAYLOAD"])
result = body.get("result")
if not isinstance(result, dict):
    raise SystemExit(f"{lane}: missing result object: {body}")
if lane == "generic-correct":
    checks = [result.get("is_correct") is True, result.get("guest_invocation_count") == 1, result.get("snapshot_isolation_ok") is True]
elif lane == "generic-wrong":
    checks = [result.get("is_correct") is False, result.get("guest_invocation_count") == 1, result.get("snapshot_isolation_ok") is True]
elif lane in {"python-reactor", "dbi-lean"}:
    checks = [result.get("is_correct") is True]
elif lane == "pyodide-scipy":
    checks = [result.get("is_correct") is True, result.get("n_samples") == 5, isinstance(result.get("p_value"), (int, float))]
else:
    raise SystemExit(f"unknown assertion lane: {lane}")
if not all(checks):
    raise SystemExit(f"{lane}: assertion failed: {body}")
print(f"    {lane}: PASS")
PY
}

wait_health generic "${GENERIC_PORT}" 90
wait_health python-reactor "${REACTOR_PORT}" 180
wait_health pyodide-scipy "${PYODIDE_PORT}" 180
if [[ "${include_dbi}" == "1" ]]; then
  wait_health dbi-lean "${DBI_PORT}" 120
fi

resp="$(post_eval "${GENERIC_PORT}" '{"response":"42","answer":"42","params":{}}')"
assert_response generic-correct "${resp}"
resp="$(post_eval "${GENERIC_PORT}" '{"response":"41","answer":"42","params":{}}')"
assert_response generic-wrong "${resp}"

resp="$(post_eval "${REACTOR_PORT}" '{"response":"3.14159","answer":"3.1416","params":{"tolerance":0.001}}')"
assert_response python-reactor "${resp}"

resp="$(post_eval "${PYODIDE_PORT}" '{"response":"","answer":"5.0","params":{"test":"ttest","samples":[4.9,5.1,5.0,5.2,4.8],"alpha":0.05}}')"
assert_response pyodide-scipy "${resp}"

if [[ "${include_dbi}" == "1" ]]; then
  resp="$(post_eval "${DBI_PORT}" '{"response":"shimmy-lean","answer":"shimmy-lean","params":{"correct_response_feedback":"Correct from Lean"}}')"
  assert_response dbi-lean "${resp}"
  dbi_marker="$("${compose[@]}" exec -T dbi-lean cat /tmp/shimmy-dbi-client-init.marker)"
  [[ "${dbi_marker}" == "initialized" ]]
  echo "    dbi-client-marker: PASS"
fi

echo "✅ compact Shimmy demo stack passed (${services[*]})"
