# Python-reactor Lambda Feedback handoff

**Status:** pending a clean replacement artifact. The interface/configuration below
is the target contract, not approval to deploy or automatically test the frozen
legacy pin.

## Deployment shape

Use `wasm` as the execution boundary and `python-reactor` as the WASM profile:

```bash
FUNCTION_INTERFACE=wasm
FUNCTION_WASM_PROFILE=python-reactor
FUNCTION_WASM_MODULE=/opt/python-reactor.wasm
FUNCTION_LF_ROOT=/var/task
```

For non-standard package layouts, prefer one config file over a long env list:

```bash
FUNCTION_INTERFACE=wasm
FUNCTION_WASM_PROFILE=python-reactor
FUNCTION_WASM_MODULE=/opt/python-reactor.wasm
FUNCTION_LF_CONFIG=/var/task/shimmy-lf.json
```

Example `shimmy-lf.json`:

```json
{
  "root": "/var/task",
  "eval": "evaluation_function.evaluation:evaluation_function",
  "preview": "evaluation_function.preview:preview_function",
  "include_roots": ["/opt/lf-puredeps"],
  "sys_path": ["/opt/lf-puredeps.zip"]
}
```

Explicit `FUNCTION_LF_*` environment variables override config-file values.

## Artifact policy

`python-reactor.wasm` must be pinned by immutable release URL/tag and SHA-256. Do
not use a floating `latest` asset for handoff or Lambda smoke.

The existing entry in `scripts/python-reactor-artifact.env` is frozen historical
evidence and must not be used for a new deployment:

```text
repo:    bkmashiro/webassembly-language-runtimes
version: v1.0.14
asset:   python-reactor.wasm
sha256:  78dcbb6d673351c0d3b776c42d2fb93b6f638cdc714d58072dece4b115edaa72
exports: py_init, py_prepare, evaluate, py_exec, alloc, dealloc, resp_buf, resp_len
```

Replacement acceptance procedure:

1. Build the replacement in `bkmashiro/webassembly-language-runtimes` and publish
   an immutable tagged asset.
2. Record producer commit, exact URL/tag, SHA-256, CPython/WASI versions, exports,
   package/API manifest, and known unsupported features.
3. Prove the required host ABI without legacy `py_exec`/response-buffer polyfills.
4. Run real script and Lambda Feedback package smokes, repeated state-reset
   canaries, recoverable-error follow-up, timeout/discard/replacement, and
   multi-runner prepared-baseline checks on Linux.
5. Review the raw evidence. Only then update `scripts/python-reactor-artifact.env`,
   deployment docs, and automatic CI.
6. Replace a 200+ MiB checked-in compatibility fixture only when a test explicitly
   requires the new ABI; remove old LFS objects in a separate auditable step.

## Smoke commands after a replacement candidate lands

Do not run these against the frozen legacy pin as current acceptance evidence.

Fast local smoke, safe on macOS:

```bash
scripts/smoke-python-reactor-handoff.sh artifact-only
```

Linux host smoke:

```bash
scripts/smoke-python-reactor-handoff.sh direct
```

Docker smoke for handoff evidence:

```bash
scripts/smoke-python-reactor-handoff.sh docker
```

The Docker mode verifies the candidate artifact, checks shell syntax, runs
dispatcher routing tests, cross-compiles the WASM package test binary for Linux,
then executes the Lambda Feedback bundle matrix through
`scripts/demo-reactor-lambda-feedback-bundles.sh docker`.

## Compatibility notes

- `FUNCTION_INTERFACE=reactor-python` still works as a compatibility alias, but do not use it in new deployment docs.
- Replacement artifacts must export `evaluate`; the legacy `py_exec + resp_buf + resp_len` fallback is frozen compatibility code, not an accepted replacement contract.
- Heavy Python package stacks that need Pyodide/Emscripten should use `FUNCTION_INTERFACE=pyodide`, not the python-reactor profile.
