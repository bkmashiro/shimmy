#!/usr/bin/env bash
set -euo pipefail

for name in CI GITHUB_ACTIONS GITLAB_CI BUILDKITE CIRCLECI JENKINS_URL; do
  value="${!name-}"
  case "$value" in
    ""|0|false|FALSE|no|NO|off|OFF)
      ;;
    *)
      printf 'agent-python ultimate benchmark is manual-only; refusing CI environment (%s)\n' "$name" >&2
      exit 2
      ;;
  esac
done

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

if [[ -n "${AGENT_PYTHON_ULTIMATE_BINARY-}" ]]; then
  exec "$AGENT_PYTHON_ULTIMATE_BINARY" "$@"
fi

exec go run ./experiments/agent-python-ultimate "$@"
