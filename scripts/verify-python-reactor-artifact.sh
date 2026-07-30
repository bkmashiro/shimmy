#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/python-reactor-artifact.env
source "${ROOT}/scripts/python-reactor-artifact.env"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  printf 'usage: %s [artifact.wasm [manifest.json [expected-commit]]]\n' "$0"
  exit 0
fi

wasm="${1:-${SHIMMY_REACTOR_WASM}}"
manifest="${2:-${SHIMMY_REACTOR_MANIFEST_PATH}}"
expected_commit="${3:-${SHIMMY_REACTOR_EXPECTED_COMMIT}}"
bundle_dir="$(cd "$(dirname "${manifest}")" && pwd)"

(
  cd "${bundle_dir}"
  sha256sum -c SHA256SUMS
)

python3 - "${ROOT}" "${wasm}" "${manifest}" "${expected_commit}" <<'PY'
import hashlib
import importlib.util
import json
import pathlib
import re
import sys

root, wasm_path, manifest_path = map(pathlib.Path, sys.argv[1:4])
expected_commit = sys.argv[4]
if not re.fullmatch(r"[0-9a-f]{40}", expected_commit):
    raise SystemExit("expected commit must be full lowercase hex")
for path in (wasm_path, manifest_path):
    if not path.is_file() or path.is_symlink():
        raise SystemExit(f"unsafe or missing bundle file: {path}")
manifest = json.loads(manifest_path.read_text())
data = wasm_path.read_bytes()
digest = hashlib.sha256(data).hexdigest()
artifact = manifest["artifact"]
producer = manifest["producer"]
checks = {
    "schema": manifest.get("schema") == "shimmy-python-runtime-manifest/v1",
    "contract": manifest.get("artifact_contract") == "shimmy-python-runtime/v1",
    "profile": manifest.get("profile") in {"base", "numpy-core"},
    "target": manifest.get("target") == "wasm32-wasip1",
    "execution_model": manifest.get("execution_model") == "reactor",
    "project": producer.get("project") == "shimmy",
    "commit": producer.get("commit") == expected_commit,
    "artifact_name": artifact.get("name") == wasm_path.name,
    "artifact_size": artifact.get("size") == len(data),
    "artifact_sha256": artifact.get("sha256") == digest,
}
failed = sorted(name for name, ok in checks.items() if not ok)
if failed:
    raise SystemExit("manifest binding failed: " + ", ".join(failed))
module_path = root / "build/python-reactor/producer/tools/wasm_contract.py"
spec = importlib.util.spec_from_file_location("shimmy_wasm_contract", module_path)
if spec is None or spec.loader is None:
    raise SystemExit("unable to load Wasm verifier")
module = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = module
spec.loader.exec_module(module)
shape = module.inspect_wasm(data)
contract = json.loads((root / "build/python-reactor/producer/contract/shimmy-python-runtime-v1.json").read_text())
module.verify_shape(
    shape,
    required_exports=contract["required_exports"],
    allowed_import_modules=contract["allowed_import_modules"],
)
if shape != manifest["wasm"]:
    raise SystemExit("actual Wasm shape differs from manifest")
print(f"artifact: {wasm_path}")
print(f"profile:  {manifest['profile']}")
print(f"commit:   {producer['commit']}")
print(f"sha256:   {digest}")
print("PASS: Shimmy Python runtime bundle verified")
PY
