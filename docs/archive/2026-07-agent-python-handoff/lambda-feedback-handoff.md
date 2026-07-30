# Lambda Feedback on Agent Python

**Status:** integrated and covered by local/remote consumer gates. This page does
not claim that a deployment or image has been published.

The runtime and artifact evidence is documented in
[Agent Python runtime integration](python-runtime-handoff.md).

## Deployment shape

Use the explicit `agent-python` profile, the pinned Wasm, its manifest, and a
Lambda Feedback package root:

```bash
FUNCTION_INTERFACE=wasm
FUNCTION_WASM_PROFILE=agent-python
FUNCTION_WASM_MODULE=/opt/agent-python/agent-python-runtime-numpy-core.wasm
FUNCTION_WASM_MANIFEST=/opt/agent-python/manifest.json
FUNCTION_LF_ROOT=/var/task
```

For a non-standard layout, use one config file:

```bash
FUNCTION_LF_CONFIG=/var/task/shimmy-lf.json
```

```json
{
  "root": "/var/task",
  "eval": "evaluation_function.evaluation:evaluation_function",
  "preview": "evaluation_function.preview:preview_function",
  "include_roots": ["/opt/lf-puredeps"]
}
```

Explicit `FUNCTION_LF_*` variables override config-file values.

At startup Shimmy runs `tools/lf-bundle-python/lf_bundle_python.py` once and
passes the generated trusted script to `runtime_prepare`. Bundling is not done
per request. Each request still receives a fresh, single-use runtime instance.

## Filesystem and dependency boundary

Agent Python exposes no Host filesystem paths to guest code. Consequently:

- `FUNCTION_LF_INCLUDE_ROOTS` is supported: Shimmy reads and embeds those
  pure-Python dependencies before sandbox startup;
- `FUNCTION_LF_SYS_PATH` and `sys_path` in `FUNCTION_LF_CONFIG` are rejected;
- no `ctypes`, NumPy random, FFT, filesystem, or import polyfills are injected;
- NumPy core and linear algebra come from the pinned runtime artifact;
- SciPy-heavy evaluators remain on the Pyodide route.

The guest imports only `agent_runtime_v1.host_call` beyond WASI. Shimmy currently
rejects every capability call, so package evaluators cannot obtain network or
credential access through the runtime.

## Artifact identity

```text
file:     agent-python-runtime-numpy-core.wasm
size:     63,626,531 bytes
sha256:   90c27951b2d8c2c7a8b42705b365cb4231c6dad207aad5260d55d2f9a85f1034
commit:   76b49158cc6c4824491561531bfe7e34872cb820
ABI:      v1
profile:  numpy-core
```

The complete bundle is checked in through Git LFS under
`build/python-reactor/artifacts/`. `SHA256SUMS`, `manifest.json`, SBOM, notices,
and extension selection are verified before E2E execution.

## Smoke commands

Fast artifact/protocol/routing gate:

```bash
scripts/smoke-python-reactor-handoff.sh artifact-only
```

Real runtime compatibility, NumPy binary128, capability denial, and timeout
recovery:

```bash
scripts/smoke-python-reactor-handoff.sh direct
```

Real HTTP examples for plain Python and NumPy:

```bash
scripts/demo-python-examples.sh reactor-only
```

The new runtime is cross-platform under wazero; these commands do not require a
Linux-only loader or a Docker fallback.

## Compatibility names

`FUNCTION_WASM_PROFILE=python-reactor`, `reactor-python`, and the legacy
`FUNCTION_INTERFACE=reactor-python` are accepted as configuration aliases. All
of them route to the same Agent Python v1 implementation. New configurations
should use `FUNCTION_INTERFACE=wasm` and `FUNCTION_WASM_PROFILE=agent-python`.
