#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
QEMU_BINARY="${SHIMMY_QEMU_BINARY:?set SHIMMY_QEMU_BINARY}"
ROOTFS="${SHIMMY_QEMU_ROOTFS:?set SHIMMY_QEMU_ROOTFS}"
MANIFEST="${SHIMMY_QEMU_MANIFEST:?set SHIMMY_QEMU_MANIFEST}"
GUEST_EVALUATOR="${SHIMMY_QEMU_GUEST_EVALUATOR:-/opt/evaluator/evaluator}"
ACCELERATOR="${SHIMMY_QEMU_ACCELERATOR:-tcg}"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/shimmy-qemu-e2e.XXXXXX")"
BIN="${SHIMMY_E2E_BIN:-${TMP}/shimmy}"
RUNNER="${SHIMMY_QEMU_RUNNER_BIN:-${TMP}/shimmy-qemu-runner}"
PORT="${SHIMMY_E2E_PORT:-}"
SERVER_PID=""

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  if [[ -n "${SHIMMY_E2E_SERVER_LOG:-}" && -f "${TMP}/server.log" ]]; then
    cp "${TMP}/server.log" "${SHIMMY_E2E_SERVER_LOG}"
  fi
  rm -rf "${TMP}"
}
trap cleanup EXIT

for cmd in curl python3; do
  command -v "${cmd}" >/dev/null 2>&1 || { echo "missing required command: ${cmd}" >&2; exit 1; }
done
[[ "$(uname -s)" == Linux ]] || { echo "QEMU E2E requires Linux" >&2; exit 1; }
[[ -x "${QEMU_BINARY}" ]] || { echo "QEMU binary must be executable" >&2; exit 1; }
[[ -r "${ROOTFS}" && -r "${MANIFEST}" ]] || { echo "QEMU rootfs and manifest must be readable" >&2; exit 1; }

if [[ -z "${SHIMMY_E2E_BIN:-}" || -z "${SHIMMY_QEMU_RUNNER_BIN:-}" ]]; then
  [[ -z "${SHIMMY_E2E_BIN:-}" && -z "${SHIMMY_QEMU_RUNNER_BIN:-}" ]] || {
    echo "set both SHIMMY_E2E_BIN and SHIMMY_QEMU_RUNNER_BIN" >&2
    exit 1
  }
  command -v go >/dev/null 2>&1 || { echo "missing required command: go" >&2; exit 1; }
  (
    cd "${ROOT}"
    go build -trimpath -buildvcs=true -o "${BIN}" .
    go build -trimpath -buildvcs=true -o "${RUNNER}" ./cmd/shimmy-qemu-runner
  )
fi
[[ -x "${BIN}" && -x "${RUNNER}" ]] || { echo "Shimmy and QEMU runner binaries must be executable" >&2; exit 1; }

if [[ -z "${PORT}" ]]; then
  PORT="$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
fi
BASE_URL="http://127.0.0.1:${PORT}"

(
  cd "${ROOT}"
  exec env \
    LOG_LEVEL=info \
    FUNCTION_INTERFACE=file \
    FUNCTION_COMMAND="${GUEST_EVALUATOR}" \
    FUNCTION_WORKING_DIR=/ \
    FUNCTION_MAX_PROCS=1 \
    FUNCTION_WORKER_SEND_TIMEOUT=90s \
    FUNCTION_QEMU_ENABLED=true \
    FUNCTION_QEMU_RUNNER="${RUNNER}" \
    FUNCTION_QEMU_BINARY="${QEMU_BINARY}" \
    FUNCTION_QEMU_ROOTFS="${ROOTFS}" \
    FUNCTION_QEMU_IMAGE_MANIFEST="${MANIFEST}" \
    FUNCTION_QEMU_ACCELERATOR="${ACCELERATOR}" \
    FUNCTION_QEMU_RESET_POLICY=lazy \
    FUNCTION_QEMU_NETWORK_PROFILE=none \
    "${BIN}" serve --host 127.0.0.1 --port "${PORT}"
) >"${TMP}/server.log" 2>&1 &
SERVER_PID="$!"

ready=false
for _ in $(seq 1 150); do
  if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
    echo "Shimmy exited during QEMU startup" >&2
    sed -n '1,240p' "${TMP}/server.log" >&2
    exit 1
  fi
  if curl -fsS "${BASE_URL}/health" >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.2
done
[[ "${ready}" == true ]] || { echo "Shimmy did not become ready" >&2; exit 1; }

boot_ids=()
for value in one two; do
  response="$(curl --fail-with-body --max-time 90 -sS -X POST "${BASE_URL}/" \
    -H 'Content-Type: application/json' -H 'Command: eval' \
    --data "{\"response\":\"${value}\",\"answer\":\"${value}\",\"params\":{}}")"
  boot_id="$(python3 - "${response}" <<'PY'
import json, sys
actual = json.loads(sys.argv[1])
if actual.get("command") != "eval" or actual.get("result", {}).get("is_correct") is not True:
    raise SystemExit(f"unexpected response: {actual!r}")
if actual["result"].get("feedback") != "qemu-e2e" or not actual["result"].get("boot_id"):
    raise SystemExit(f"missing QEMU evidence: {actual!r}")
print(actual["result"]["boot_id"])
PY
  )"
  boot_ids+=("${boot_id}")
done
[[ "${boot_ids[0]}" != "${boot_ids[1]}" ]] || { echo "lazy reset reused QEMU boot ${boot_ids[0]}" >&2; exit 1; }

if grep -Eqi 'fall(ing)? back|automatic.*(tcg|qemu)' "${TMP}/server.log"; then
  echo "server log implies an automatic fallback" >&2
  exit 1
fi
printf 'qemu_file_e2e=PASS\nqemu_reset_policy=lazy\nqemu_distinct_boots=PASS\nqemu_boot_1=%s\nqemu_boot_2=%s\nqemu_network_profile=none\nqemu_accelerator=%s\n' \
  "${boot_ids[0]}" "${boot_ids[1]}" "${ACCELERATOR}"
