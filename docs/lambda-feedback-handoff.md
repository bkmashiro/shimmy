# Python-reactor Lambda Feedback handoff

This is the operator handoff for the Python fast path after the runtime/profile split.

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
  "include_roots": ["/opt/lf-puredeps", "/opt/shimmy/polyfills/reactor"],
  "sys_path": ["/opt/lf-puredeps.zip"]
}
```

Explicit `FUNCTION_LF_*` environment variables override config-file values.

## Artifact policy

`python-reactor.wasm` is pinned by release tag and SHA256. Do not use a floating `latest` asset for handoff or Lambda smoke.

Current pin lives in `scripts/python-reactor-artifact.env`:

```text
repo:    bkmashiro/webassembly-language-runtimes
version: v1.0.14
asset:   python-reactor.wasm
sha256:  78dcbb6d673351c0d3b776c42d2fb93b6f638cdc714d58072dece4b115edaa72
exports: py_init, py_prepare, evaluate, py_exec, alloc, dealloc, resp_buf, resp_len
```

Update procedure:

1. Change/build the artifact in `bkmashiro/webassembly-language-runtimes`.
2. Publish a tagged release.
3. Run `scripts/verify-python-reactor-artifact.sh /path/to/python-reactor.wasm`.
4. Update `scripts/python-reactor-artifact.env` with the new tag/SHA.
5. Let CI/smoke download the pinned release into its cache; do not upload a new
   200+ MiB LFS fixture merely to move the deployment pin.
6. Replace a checked-in compatibility fixture only when a test explicitly needs
   the new ABI, and record why.
7. Run `scripts/smoke-python-reactor-handoff.sh docker` on a Linux-capable machine.

## Smoke commands

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

The Docker mode verifies the pinned artifact, checks shell syntax, runs dispatcher routing tests, cross-compiles the WASM package test binary for Linux, then executes the Lambda Feedback bundle matrix through `scripts/demo-reactor-lambda-feedback-bundles.sh docker`.

## Compatibility notes

- `FUNCTION_INTERFACE=reactor-python` still works as a compatibility alias, but do not use it in new deployment docs.
- New artifacts should export `evaluate`; the host still supports old artifacts without `evaluate` via the `py_exec + resp_buf + resp_len` fallback test fixture.
- Heavy Python package stacks that need Pyodide/Emscripten should use `FUNCTION_INTERFACE=pyodide`, not the python-reactor profile.
