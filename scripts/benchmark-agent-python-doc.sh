#!/usr/bin/env bash
set -euo pipefail

GATEWAY="${SHIMMY_DOC_GATEWAY:-gpucluster2}"
SSH=(ssh -o BatchMode=yes "$GATEWAY")

for name in CI GITHUB_ACTIONS GITLAB_CI BUILDKITE CIRCLECI JENKINS_URL; do
  value="${!name-}"
  case "$value" in
    ""|0|false|FALSE|no|NO|off|OFF) ;;
    *)
      printf 'agent-python DoC benchmark is manual-only; refusing CI environment (%s)\n' "$name" >&2
      exit 2
      ;;
  esac
done

validate_run_id() {
  local run_id="${1-}"
  if [[ ! "$run_id" =~ ^agent-python-[0-9]{8}t[0-9]{6}z-[a-f0-9]{8}$ ]]; then
    printf 'invalid run id: %q\n' "$run_id" >&2
    return 2
  fi
}

validate_job_id() {
  local job_id="${1-}"
  if [[ ! "$job_id" =~ ^[0-9]+$ ]]; then
    printf 'invalid Slurm job id: %q\n' "$job_id" >&2
    return 2
  fi
}

gateway_root() {
  local run_id="$1"
  validate_run_id "$run_id"
  printf '/tmp/shimmy-agent-python-controller-%s' "$run_id"
}

render_sbatch() {
  local run_id="$1"
  local job_script="${2:-$(gateway_root "$run_id")/job.sh}"
  validate_run_id "$run_id"
  case "$job_script" in
    /tmp/shimmy-agent-python-controller-"$run_id"/job.sh) ;;
    *) printf 'unsafe remote job script: %q\n' "$job_script" >&2; return 2 ;;
  esac
  printf '%s\n' \
    "sbatch" \
    "--parsable" \
    "--partition=a16" \
    "--nodelist=gpuvm36" \
    "--nodes=1" \
    "--ntasks=1" \
    "--cpus-per-task=6" \
    "--mem=48G" \
    "--gres=gpu:nvidia_a16:1" \
    "--time=2-12:00:00" \
    "--export=NIL" \
    "--chdir=/tmp" \
    "--output=/tmp/shimmy-agent-python-%j-slurm.out" \
    "--job-name=${run_id}" \
    "$job_script"
}

submit_job() {
  local run_id="$1"
  local root
  root="$(gateway_root "$run_id")"
  "${SSH[@]}" \
    sbatch --parsable \
      --partition=a16 --nodelist=gpuvm36 --nodes=1 --ntasks=1 \
      --cpus-per-task=6 --mem=48G --gres=gpu:nvidia_a16:1 \
      --time=2-12:00:00 --export=NIL --chdir=/tmp \
      --output=/tmp/shimmy-agent-python-%j-slurm.out \
      --job-name="$run_id" "$root/job.sh"
}

stage_job() {
  local run_id="$1"
  local job_id="$2"
  validate_run_id "$run_id"
  validate_job_id "$job_id"
  local root
  root="$(gateway_root "$run_id")"
  "${SSH[@]}" bash -s -- "$root" "$job_id" <<'REMOTE'
set -euo pipefail
root="$1"
job_id="$2"
case "$root" in /tmp/shimmy-agent-python-controller-agent-python-*) ;; *) exit 2 ;; esac
for _ in $(seq 1 600); do
  state="$(squeue -h -j "$job_id" -o '%T')"
  case "$state" in
    RUNNING) break ;;
    PENDING|CONFIGURING) sleep 1 ;;
    *) printf 'job %s entered state %s before stage\n' "$job_id" "$state" >&2; exit 3 ;;
  esac
done
[[ "${state-}" == RUNNING ]]
sleep 5
broadcast_output="$({
  sbcast -v --force --jobid="$job_id.batch" "$root/input.tar.zst" "/tmp/shimmy-agent-python-$job_id/input.tar.zst"
  sbcast -v --force --jobid="$job_id.batch" "$root/input.sha256" "/tmp/shimmy-agent-python-$job_id/input.sha256"
} 2>&1)"
printf '%s\n' "$broadcast_output"
case "$broadcast_output" in
  *"jobid      = $job_id.batch"*) ;;
  *) printf 'sbcast did not confirm the batch step credential\n' >&2; exit 4 ;;
esac
sleep 5
srun --jobid="$job_id" --overlap -N1 -n1 test -s "/tmp/shimmy-agent-python-$job_id/input.tar.zst"
srun --jobid="$job_id" --overlap -N1 -n1 test -s "/tmp/shimmy-agent-python-$job_id/input.sha256"
REMOTE
}

job_status() {
  local job_id="$1"
  validate_job_id "$job_id"
  "${SSH[@]}" bash -s -- "$job_id" <<'REMOTE'
set -euo pipefail
job_id="$1"
squeue -j "$job_id" -o '%.18i %.12T %.20S %.20e %.8M %.9l %.6D %R'
sacct -j "$job_id" --starttime now-7days -X -n -P -o JobID,State,Elapsed,Timelimit,NodeList,ExitCode 2>/dev/null || true
if [[ "$(squeue -h -j "$job_id" -o '%T')" == RUNNING ]]; then
  if srun --jobid="$job_id" --overlap -N1 -n1 test -f "/tmp/shimmy-agent-python-$job_id/RESULT.READY" 2>/dev/null; then
    printf 'RESULT_READY=yes\n'
  else
    printf 'RESULT_READY=no\n'
  fi
fi
REMOTE
}

pull_result() {
  local job_id="$1"
  local local_dir="$2"
  validate_job_id "$job_id"
  mkdir -p -m 0700 -- "$local_dir"
  local archive="$local_dir/result.tar.zst"
  local checksum="$local_dir/result.sha256"
  local temp_archive="$archive.partial"
  local temp_checksum="$checksum.partial"
  rm -f -- "$temp_archive" "$temp_checksum"
  "${SSH[@]}" srun --jobid="$job_id" --overlap -N1 -n1 \
    cat "/tmp/shimmy-agent-python-$job_id/result.sha256" >"$temp_checksum"
  "${SSH[@]}" srun --jobid="$job_id" --overlap -N1 -n1 \
    cat "/tmp/shimmy-agent-python-$job_id/result.tar.zst" >"$temp_archive"
  mv -f -- "$temp_checksum" "$checksum"
  mv -f -- "$temp_archive" "$archive"
  (
    cd "$local_dir"
    checksum_line="$(cat result.sha256)"
    if [[ ! "$checksum_line" =~ ^[0-9a-f]{64}[[:space:]][[:space:]]result.tar.zst$ ]]; then
      printf 'invalid result checksum manifest\n' >&2
      exit 2
    fi
    sha256sum -c result.sha256
  )
}

ack_result() {
  local job_id="$1"
  validate_job_id "$job_id"
  "${SSH[@]}" srun --jobid="$job_id" --overlap -N1 -n1 \
    sh -c 'test -f "$1/RESULT.READY" && touch "$1/ACK"' sh "/tmp/shimmy-agent-python-$job_id"
}

cleanup_controller() {
  local run_id="$1"
  local root
  root="$(gateway_root "$run_id")"
  "${SSH[@]}" bash -s -- "$root" <<'REMOTE'
set -euo pipefail
root="$1"
case "$root" in /tmp/shimmy-agent-python-controller-agent-python-*) rm -rf -- "$root" ;; *) exit 2 ;; esac
REMOTE
}

usage() {
  printf 'usage: %s COMMAND ...\ncommands: validate-run-id RUN_ID | render-sbatch RUN_ID | submit RUN_ID | stage RUN_ID JOB_ID | status JOB_ID | pull JOB_ID LOCAL_DIR | ack JOB_ID | cleanup-controller RUN_ID\n' "$0" >&2
  exit 2
}

command_name="${1-}"
case "$command_name" in
  validate-run-id) [[ $# -eq 2 ]] || usage; validate_run_id "$2" ;;
  render-sbatch) [[ $# -eq 2 ]] || usage; render_sbatch "$2" ;;
  submit) [[ $# -eq 2 ]] || usage; submit_job "$2" ;;
  stage) [[ $# -eq 3 ]] || usage; stage_job "$2" "$3" ;;
  status) [[ $# -eq 2 ]] || usage; job_status "$2" ;;
  pull) [[ $# -eq 3 ]] || usage; pull_result "$2" "$3" ;;
  ack) [[ $# -eq 2 ]] || usage; ack_result "$2" ;;
  cleanup-controller) [[ $# -eq 2 ]] || usage; cleanup_controller "$2" ;;
  *) usage ;;
esac
