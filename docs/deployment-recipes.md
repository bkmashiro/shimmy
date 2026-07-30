# Deployment recipes

`FUNCTION_INTERFACE` selects the execution boundary, not the evaluator language.
Runtime selection is explicit; Shimmy never scans imports, requirements, or file
extensions.

## Runtime matrix

| Scenario | Required configuration | Notes |
|---|---|---|
| Generic WASM | `FUNCTION_INTERFACE=wasm`; `FUNCTION_WASM_PROFILE=generic`; `FUNCTION_WASM_MODULE=/path/eval.wasm` | Guest exports Shimmy `alloc + evaluate` ABI. Generic snapshot modes remain available. |
| Shimmy Python script | `FUNCTION_INTERFACE=wasm`; `FUNCTION_WASM_PROFILE=shimmy-python`; `FUNCTION_WASM_MODULE=/path/shimmy-python-runtime-numpy-core.wasm`; `FUNCTION_WASM_MANIFEST=/path/manifest.json`; `FUNCTION_WASM_PYTHON_SCRIPT=/path/eval.py` | CPython 3.14 + NumPy core. Post-prepare snapshot/memcpy by default; explicit single-use and fresh modes remain available. |
| Shimmy Python LF package | Shimmy Python artifact/manifest plus `FUNCTION_LF_ROOT=/path/package` | Shimmy bundles package modules and pure-Python include roots once at startup. |
| Pyodide compatibility | `FUNCTION_INTERFACE=pyodide`; runner plus script or package-mode variables | Compatibility lane for SciPy/Pandas and Emscripten packages. |
| RPC/file migration | `FUNCTION_INTERFACE=rpc` or `file`; `FUNCTION_COMMAND=...` | Existing subprocess protocols. |
| Full Linux via QEMU | Existing `rpc`/`file` configuration plus explicit `FUNCTION_QEMU_*` artifact and lifecycle values | Transparent terminal fallback; QEMU and DBI are mutually exclusive. |

Shared HTTP, stdio JSON-RPC, and Pyodide frames default to 4 MiB. Agent
Python's guest ABI additionally bounds each request and response to 1 MiB.

## Shimmy Python

Canonical repository paths:

```text
dist/shimmy-python/numpy-core/shimmy-python-runtime-numpy-core.wasm
dist/shimmy-python/numpy-core/manifest.json
```

Artifact identity:

```text
artifact: generated, not tracked
sha256:   bound by manifest and SHA256SUMS
commit:   exact Shimmy producer/consumer commit
ABI:      shimmy-python-runtime/v1
profile:  numpy-core
```

### Script

```bash
FUNCTION_INTERFACE=wasm \
FUNCTION_WASM_PROFILE=shimmy-python \
FUNCTION_WASM_MODULE=dist/shimmy-python/numpy-core/shimmy-python-runtime-numpy-core.wasm \
FUNCTION_WASM_MANIFEST=dist/shimmy-python/numpy-core/manifest.json \
FUNCTION_WASM_PYTHON_SCRIPT=examples/eval-python/eval.py \
FUNCTION_WASM_PYTHON_LIFECYCLE=snapshot \
FUNCTION_WASM_SNAPSHOT_MODE=memcpy \
FUNCTION_WASM_MAX_MEMORY_PAGES=8192 \
FUNCTION_MAX_PROCS=1 \
./shimmy serve
```

`FUNCTION_WASM_PYTHON_PRELOAD=evaluator` is the default and executes the trusted
script through `runtime_prepare`. `off` remains accepted for compatibility and
executes the trusted script inside each fresh request namespace.

`FUNCTION_WASM_PYTHON_LIFECYCLE` accepts `snapshot` (default), `single-use`, or
`fresh`. Snapshot mode accepts the same strategy names as generic WASM. On
Linux, `FUNCTION_WASM_SNAPSHOT_MODE=cow` uses one sealed image per Shimmy Python
slot; on other platforms it explicitly falls back to `memcpy`. Single-use uses
`FUNCTION_WASM_PYTHON_PREPARED_CAPACITY=1..4` and never returns a served module
to its ready pool.

### Lambda Feedback package

```bash
FUNCTION_INTERFACE=wasm \
FUNCTION_WASM_PROFILE=shimmy-python \
FUNCTION_WASM_MODULE=dist/shimmy-python/numpy-core/shimmy-python-runtime-numpy-core.wasm \
FUNCTION_WASM_MANIFEST=dist/shimmy-python/numpy-core/manifest.json \
FUNCTION_LF_ROOT=examples/lambda-feedback-fixtures/boilerplate-python \
FUNCTION_LF_INCLUDE_ROOTS=/opt/lf-puredeps \
./shimmy serve
```

Supported startup options include `FUNCTION_LF_CONFIG`, eval/preview entrypoint
overrides, adapter/bundler paths, include roots, and bundle output. The Agent
profile exposes no Host filesystem to guest code, so `FUNCTION_LF_SYS_PATH` and
`sys_path` config entries fail closed. Embed pure-Python dependencies with
`FUNCTION_LF_INCLUDE_ROOTS` instead.

The guest imports only WASI. No network, credential, transaction, or custom Host capability is granted.

Verify locally:

```bash
scripts/smoke-python-reactor-handoff.sh artifact-only
scripts/smoke-python-reactor-handoff.sh direct
scripts/demo-python-examples.sh reactor-only
```

## Generic WASM snapshot modes

Generic `FUNCTION_WASM_PROFILE=generic` supports explicit snapshot strategies
through `FUNCTION_WASM_SNAPSHOT_MODE`: `memcpy`, `soft-dirty`, `mprotect`,
`uffd`, and `cow` where available. Full copy remains the default.

The Linux COW prototype uses a dispatcher-scoped sealed prepared-memory image
and fixed-size private mappings. It covers linear memory only, not globals,
tables, WASI/Host state, external effects, RNG, clocks, or descriptors. It must
not be used to claim whole-instance freshness. Shimmy Python uses the same
strategy implementations but gives each COW slot its own image because separate
CPython initialization carries independently randomized state.

## QEMU full-Linux fallback

QEMU wraps an existing process worker; keep `FUNCTION_INTERFACE=file` or `rpc`.
A minimal file-worker configuration is:

```bash
FUNCTION_INTERFACE=file \
FUNCTION_COMMAND=/opt/evaluator/eval \
FUNCTION_QEMU_ENABLED=true \
FUNCTION_QEMU_RUNNER=/opt/shimmy/bin/shimmy-qemu-runner \
FUNCTION_QEMU_BINARY=/usr/bin/qemu-system-x86_64 \
FUNCTION_QEMU_ROOTFS=/opt/shimmy-qemu/evaluator.squashfs \
FUNCTION_QEMU_IMAGE_MANIFEST=/opt/shimmy-qemu/manifest.json \
FUNCTION_QEMU_ACCELERATOR=tcg \
FUNCTION_QEMU_NETWORK_PROFILE=none \
FUNCTION_QEMU_MEMORY_MB=512 \
FUNCTION_QEMU_VCPUS=1 \
./shimmy serve
```

The manifest and every image component are SHA-256 verified. `file` owns one
fresh VM per request. Non-Lambda `rpc` is persistent; Lambda defaults to lazy
single-use ownership. Shimmy never retries a failed native/DBI request under
QEMU. There is no silent KVM-to-TCG fallback.

## Compatibility aliases

Accepted but not recommended for new configurations:

- `FUNCTION_WASM_PROFILE=python-reactor` and `reactor-python` route to
  `shimmy-python`;
- `FUNCTION_INTERFACE=reactor-python` routes to the same Shimmy Python path;
- `FUNCTION_COMMAND=/path/module.wasm` remains a generic module-path alias;
- `FUNCTION_INTERFACE=python-wasm` is the independent legacy resident Python
  comparison path.
