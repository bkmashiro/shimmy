#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="${DEST:-${ROOT}/.demo-lambda-sources}"
REPOS=(
  IsSimilar
  ArrayEqual
  SymbolicEqual
  compareBoolean
  compareSets
  shortTextAnswer
  evaluatePython
  evaluation-function-boilerplate-python
  wolframIsSimilar
  evaluation-function-boilerplate-lean
)

mkdir -p "${DEST}"

echo "==> Fetching public Lambda Feedback evaluation-function repositories"
for repo in "${REPOS[@]}"; do
  target="${DEST}/${repo}"
  if [[ -d "${target}/.git" ]]; then
    echo "    update ${repo}"
    git -C "${target}" pull --ff-only --quiet || true
  else
    echo "    clone  ${repo}"
    git clone --depth 1 "https://github.com/lambda-feedback/${repo}.git" "${target}" --quiet
  fi
done

SUMMARY="${DEST}/SUMMARY.md"
python3 - "${DEST}" "${SUMMARY}" <<'PY'
from pathlib import Path
import sys
base = Path(sys.argv[1])
out = Path(sys.argv[2])
rows = []
for repo in sorted(p for p in base.iterdir() if p.is_dir() and (p/'.git').exists()):
    files = [p.relative_to(repo).as_posix() for p in repo.rglob('*') if p.is_file()]
    evals = [f for f in files if f.endswith(('.py', '.wl', '.lean')) and any(part in f.lower() for part in ['eval', 'main', 'function'])]
    reqs = [f for f in files if f.lower().endswith(('requirements.txt','pyproject.toml','lakefile.lean','dockerfile')) or f == 'Dockerfile']
    rows.append((repo.name, ', '.join(evals[:5]) or '—', ', '.join(reqs[:5]) or '—'))
with out.open('w') as f:
    f.write('# Lambda Feedback real evaluation-function sources\n\n')
    f.write('Fetched from public `lambda-feedback/*` GitHub repositories. These are used as reference material for realistic Shimmy-WASM demo scenarios; they are not vendored into the repo.\n\n')
    f.write('| Repo | Likely evaluator files | Runtime/dependency files |\n')
    f.write('|---|---|---|\n')
    for name, evals, reqs in rows:
        f.write(f'| `{name}` | {evals} | {reqs} |\n')
print(out)
PY

echo "==> Wrote ${SUMMARY}"
