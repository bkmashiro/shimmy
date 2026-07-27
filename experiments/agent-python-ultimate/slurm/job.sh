#!/usr/bin/env bash
set -euo pipefail
umask 077
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

: "${SLURM_JOB_ID:?SLURM_JOB_ID is required}"
case "$SLURM_JOB_ID" in
  *[!0-9]*)
    printf 'invalid SLURM_JOB_ID: %q\n' "$SLURM_JOB_ID" >&2
    exit 2
    ;;
esac

run_root="/tmp/shimmy-agent-python-${SLURM_JOB_ID}"
case "$run_root" in
  /tmp/shimmy-agent-python-[0-9]*) ;;
  *)
    printf 'unsafe run root: %q\n' "$run_root" >&2
    exit 2
    ;;
esac

mkdir -m 0700 -- "$run_root"
input_archive="$run_root/input.tar.zst"
input_checksum="$run_root/input.sha256"
input_tar="$run_root/input.tar"
input_dir="$run_root/input"
output_dir="$run_root/output"
result_archive="$run_root/result.tar.zst"
result_checksum="$run_root/result.sha256"

wait_for_file() {
  local path="$1"
  local timeout_seconds="$2"
  local waited=0
  while [[ ! -f "$path" ]]; do
    if (( waited >= timeout_seconds )); then
      printf 'timed out waiting for %s\n' "$path" >&2
      return 124
    fi
    sleep 1
    waited=$((waited + 1))
  done
}

# The gateway waits for RUNNING state, then sbcast writes these two files.
wait_for_file "$input_archive" 1800
wait_for_file "$input_checksum" 1800

archive_bytes="$(stat -c '%s' "$input_archive")"
if (( archive_bytes <= 0 || archive_bytes > 268435456 )); then
  printf 'input bundle size %s is outside 1..256 MiB\n' "$archive_bytes" >&2
  exit 2
fi

checksum_line_count="$(wc -l <"$input_checksum" | tr -d '[:space:]')"
checksum_hash=""
checksum_name=""
checksum_extra=""
read -r checksum_hash checksum_name checksum_extra <"$input_checksum" || true
if [[ "$checksum_line_count" != "1" || ! "$checksum_hash" =~ ^[0-9a-f]{64}$ || "$checksum_name" != "input.tar.zst" || -n "$checksum_extra" ]]; then
  printf 'input checksum file must contain exactly one lowercase SHA-256 entry for input.tar.zst\n' >&2
  exit 2
fi
printf '%s  input.tar.zst\n' "$checksum_hash" >"$run_root/input.sha256.canonical"
mv -f -- "$run_root/input.sha256.canonical" "$input_checksum"

(
  cd "$run_root"
  sha256sum -c "$(basename "$input_checksum")"
)

free_kib="$(df -Pk /tmp | sed -n '2{s/[[:space:]][[:space:]]*/ /g;p;}' | cut -d' ' -f4)"
if [[ -z "$free_kib" ]] || (( free_kib < 4194304 )); then
  printf 'compute-node /tmp has less than 4 GiB free: %s KiB\n' "${free_kib:-unknown}" >&2
  exit 2
fi

zstd -q -d -f "$input_archive" -o "$input_tar"
mkdir -m 0700 -- "$input_dir" "$output_dir"
python3 - "$input_tar" "$input_dir" <<'PY'
import pathlib
import sys
import tarfile

archive = pathlib.Path(sys.argv[1])
destination = pathlib.Path(sys.argv[2]).resolve()
with tarfile.open(archive, "r:") as bundle:
    members = bundle.getmembers()
    for member in members:
        path = pathlib.PurePosixPath(member.name)
        if path.is_absolute() or ".." in path.parts:
            raise SystemExit(f"unsafe bundle member: {member.name!r}")
        if member.issym() or member.islnk() or member.isdev():
            raise SystemExit(f"unsupported bundle member type: {member.name!r}")
    bundle.extractall(destination)
PY
rm -f -- "$input_tar"

{
  printf 'captured_at_utc='; date -u +%FT%TZ
  printf 'hostname='; hostname
  printf 'uname='; uname -srmo
  printf 'page_size='; getconf PAGESIZE
  printf 'slurm_job_id=%s\n' "$SLURM_JOB_ID"
  printf 'slurm_job_cpus_per_node=%s\n' "${SLURM_JOB_CPUS_PER_NODE-}"
  printf 'slurm_job_gpus=%s\n' "${SLURM_JOB_GPUS-}"
  printf 'cpuset='; python3 -c 'import os; print(",".join(map(str, sorted(os.sched_getaffinity(0)))))'
  printf '%s\n' '--- lscpu ---'
  lscpu
  printf '%s\n' '--- df /tmp ---'
  df -h /tmp
  printf '%s\n' '--- cgroup ---'
  cat /proc/self/cgroup
} >"$output_dir/environment.txt" 2>"$output_dir/environment.stderr"

benchmark_rc=0
"$input_dir/agent-python-ultimate" run \
  --config "$input_dir/ultimate.json" \
  --artifact "$input_dir/agent-python-runtime-numpy-core.wasm" \
  --manifest "$input_dir/manifest.json" \
  --output "$output_dir/run" \
  >"$output_dir/benchmark.stdout" \
  2>"$output_dir/benchmark.stderr" || benchmark_rc=$?
printf '%s\n' "$benchmark_rc" >"$output_dir/benchmark.exit-code"

(
  cd "$run_root"
  tar -cf - output | zstd -q -T0 -19 -o "$result_archive"
  sha256sum "$(basename "$result_archive")" >"$result_checksum"
)
result_bytes="$(stat -c '%s' "$result_archive")"
if (( result_bytes <= 0 || result_bytes > 1073741824 )); then
  printf 'result bundle size %s is outside 1..1024 MiB\n' "$result_bytes" >&2
  exit 2
fi
: >"$run_root/RESULT.READY"

ack_rc=0
wait_for_file "$run_root/ACK" 21600 || ack_rc=$?

case "$run_root" in
  /tmp/shimmy-agent-python-[0-9]*) rm -rf -- "$run_root" ;;
  *) printf 'refusing cleanup of unsafe path: %q\n' "$run_root" >&2; exit 2 ;;
esac

if (( ack_rc != 0 )); then
  exit "$ack_rc"
fi
exit "$benchmark_rc"
