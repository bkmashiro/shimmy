# Configuration Reference

All configuration is read from environment variables. The table below lists
every supported variable, its default, which backend(s) it applies to, and
what it does.

---

## Core Routing

| Variable | Default | Backend | Description |
|---|---|---|---|
| `FUNCTION_INTERFACE` | _(empty)_ | all | Selects the execution backend. Values: `wasm`, `python-wasm`, `reactor-python`, `rpc`, `pyodide`. Empty or unrecognised values use the pooled subprocess backend. |
| `FUNCTION_COMMAND` | _(required)_ | all | Path to the executable or WASM module. For `wasm`: path to the `.wasm` file (overridden by `FUNCTION_WASM_MODULE`). For `rpc` / subprocess: the command to spawn. |
| `FUNCTION_MAX_PROCS` | `runtime.NumCPU()` | all | Maximum number of concurrent pool instances. For reactor-python: additionally capped at 4. For python-wasm: additionally capped at 8. |
| `FUNCTION_TIMEOUT` | `30s` | all | Per-request deadline. WASM backends apply this as a `context.WithTimeout` before calling the guest. Subprocess backends pass it as the send timeout. |

---

## WASM Sandbox

These variables are read by `wasm.Config.applyEnv()` and apply only to the
three WASM backends (`wasm`, `python-wasm`, `reactor-python`).

| Variable | Default | Backend | Description |
|---|---|---|---|
| `FUNCTION_WASM_MODULE` | _(unset)_ | `wasm`, `python-wasm`, `reactor-python` | Path to the `.wasm` file. Takes precedence over `FUNCTION_COMMAND` when set. Useful when `FUNCTION_COMMAND` is used for something else by the deployment framework. |
| `FUNCTION_WASM_MAX_MEMORY_PAGES` | `256` (16 MB) | `wasm`, `python-wasm`, `reactor-python` | Maximum WASM linear memory in 64 KB pages. Passed to `wazero.NewRuntimeConfig().WithMemoryLimitPages(N)`. A guest that calls `memory.grow` beyond this limit gets a WASM trap. Set to `0` to use the module's own declared maximum without a host-imposed cap. For `python-reactor.wasm` the recommended value is `8192` (512 MB) because the CPython heap after pre-warming numpy is ~100 MB. |
| `FUNCTION_WASM_ALLOWED_PATHS` | _(empty, no FS access)_ | `wasm`, `reactor-python` | Comma-separated list of host filesystem paths to mount read-only into the WASM sandbox. Example: `/var/data/datasets,/usr/local/lib/python3.14`. For `reactor-python`, paths are also appended to `PYTHONPATH` so Python can import from them. |
| `FUNCTION_WASM_ALLOWED_ENV` | _(empty, no env access)_ | `wasm` | Comma-separated list of host environment variable names to expose to the guest. Only listed variables are passed; all others remain invisible. Example: `GRADING_API_KEY,MODEL_VERSION`. |

---

## Snapshot Strategy

| Variable | Default | Backend | Description |
|---|---|---|---|
| `FUNCTION_WASM_SNAPSHOT_MODE` | `"memcpy"` | `wasm`, `reactor-python` | Memory snapshot/restore strategy. Values: `memcpy` (default, always available), `soft-dirty` (Linux only, MaxInstances=1 only), `mprotect` (Linux + CGO, MaxInstances=1 only), `uffd` (Linux + userfaultfd WP, any MaxInstances). Falls back to `memcpy` if the requested strategy is unavailable. See `docs/snapshot-restore.md` for details. |
| `FUNCTION_WASM_USE_UFFD` | `false` | `wasm`, `reactor-python` | Deprecated alias for `FUNCTION_WASM_SNAPSHOT_MODE=uffd`. `FUNCTION_WASM_SNAPSHOT_MODE` takes precedence if both are set. |

---

## Compilation Cache

| Variable | Default | Backend | Description |
|---|---|---|---|
| `FUNCTION_WASM_COMPILE_CACHE` | _(empty, no cache)_ | `wasm`, `python-wasm`, `reactor-python` | Directory path for wazero's on-disk compilation cache. When set, wazero serialises the compiled native code for each `.wasm` module to this directory. On repeat starts with the same module bytes, compilation is skipped entirely. Critical for `python-reactor.wasm` (cold compilation: 1-3 min; warm: < 1 s). The directory is created automatically if it does not exist. Multiple processes can share the same cache directory safely. |

---

## Python

| Variable | Default | Backend | Description |
|---|---|---|---|
| `FUNCTION_WASM_PYTHON_SCRIPT` | _(required for WASM Python script backends)_ | `python-wasm`, `reactor-python` | Path to a **single-file** Python evaluation script (`eval.py`) or a generated Lambda Feedback bundle from `tools/lf-bundle-python`. The file must define `evaluation_function(response, answer, params=None)`. Optionally defines `preview_function(response, answer, params=None)`. Read once at startup and cached in memory. Reactor still does not consume Pyodide-style package env vars directly; package evaluators should use Pyodide package mode or be converted to a single-file bundle first. |
| `FUNCTION_WASM_PYTHON_PATH` | `/usr/lib/python3.14/site-packages` | `reactor-python` | Override for the `PYTHONPATH` environment variable passed to the reactor Python module. Used when additional site-packages are packed into the WASM binary at a non-standard path. Also combined with `FUNCTION_WASM_ALLOWED_PATHS` when extra package directories are mounted from the host. |
| `FUNCTION_PYODIDE_SCRIPT` | _(required in legacy Pyodide script mode)_ | `pyodide` | Path to a single `eval.py` script. Used when `FUNCTION_PYODIDE_ROOT` + `FUNCTION_PYODIDE_EVAL_ENTRYPOINT` are not set. |
| `FUNCTION_PYODIDE_ROOT` | _(required in Pyodide package mode)_ | `pyodide` | Evaluator package root to mirror into the Pyodide runtime. Use this for Lambda Feedback package layouts such as `evaluation_function/evaluation.py`. |
| `FUNCTION_PYODIDE_EVAL_ENTRYPOINT` | _(required in Pyodide package mode)_ | `pyodide` | Eval entrypoint in `module:function` form, e.g. `evaluation_function.evaluation:evaluation_function`. |
| `FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT` | _(empty)_ | `pyodide` | Optional preview entrypoint in `module:function` form. If omitted, preview RPCs fall back to the eval entrypoint. |
| `FUNCTION_PYODIDE_ADAPTER` | _(required in package mode)_ | `pyodide` | Path to `examples/lambda-feedback-adapter/lf_compat_adapter.py`. The runner mirrors the adapter directory too so the minimal `lf_toolkit` shim is importable. |
| `FUNCTION_PYODIDE_PACKAGES` | _(empty in package mode; `scipy` in legacy script mode)_ | `pyodide` | Comma-separated Pyodide packages to install before loading the evaluator, e.g. `sympy` or `scipy`. |

---

## Example: Docker / `docker run`

**General WASM evaluation function:**

```bash
docker run --rm \
  -e FUNCTION_INTERFACE=wasm \
  -e FUNCTION_COMMAND=/app/eval.wasm \
  -e FUNCTION_MAX_PROCS=4 \
  -e FUNCTION_TIMEOUT=10s \
  -e FUNCTION_WASM_MAX_MEMORY_PAGES=512 \
  -p 8080:8080 \
  ghcr.io/lambda-feedback/shimmy-wasm:latest
```

**Legacy resident Python (compatibility only; weaker isolation):**

```bash
docker run --rm \
  -e FUNCTION_INTERFACE=python-wasm \
  -e FUNCTION_COMMAND=/app/python.wasm \
  -e FUNCTION_WASM_PYTHON_SCRIPT=/app/eval.py \
  -e FUNCTION_WASM_COMPILE_CACHE=/cache/wazero \
  -e FUNCTION_MAX_PROCS=2 \
  -v $(pwd)/eval.py:/app/eval.py:ro \
  -v /tmp/wazero-cache:/cache/wazero \
  -p 8080:8080 \
  ghcr.io/lambda-feedback/shimmy-wasm:latest
```

**Reactor Python (snapshot isolation, numpy available):**

```bash
docker run --rm \
  -e FUNCTION_INTERFACE=reactor-python \
  -e FUNCTION_COMMAND=/app/python-reactor.wasm \
  -e FUNCTION_WASM_PYTHON_SCRIPT=/app/eval.py \
  -e FUNCTION_WASM_COMPILE_CACHE=/cache/wazero \
  -e FUNCTION_WASM_MAX_MEMORY_PAGES=8192 \
  -e FUNCTION_WASM_SNAPSHOT_MODE=memcpy \
  -e FUNCTION_MAX_PROCS=2 \
  -e FUNCTION_TIMEOUT=30s \
  -v $(pwd)/eval.py:/app/eval.py:ro \
  -v /tmp/wazero-cache:/cache/wazero \
  -p 8080:8080 \
  ghcr.io/lambda-feedback/shimmy-wasm:latest
```

**Pyodide package mode (Lambda Feedback package compatibility):**

```bash
docker run --rm \
  -e FUNCTION_INTERFACE=pyodide \
  -e FUNCTION_PYODIDE_RUNNER=/app/examples/eval-pyodide/runner.js \
  -e FUNCTION_PYODIDE_ROOT=/app/evaluator \
  -e FUNCTION_PYODIDE_EVAL_ENTRYPOINT=evaluation_function.evaluation:evaluation_function \
  -e FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT=evaluation_function.preview:preview_function \
  -e FUNCTION_PYODIDE_ADAPTER=/app/examples/lambda-feedback-adapter/lf_compat_adapter.py \
  -e FUNCTION_PYODIDE_PACKAGES=sympy \
  -v $(pwd)/evaluation_function:/app/evaluator/evaluation_function:ro \
  -p 8080:8080 \
  ghcr.io/lambda-feedback/shimmy-wasm:latest
```

Use package mode for real Lambda Feedback evaluator repositories that import
`lf_toolkit` and expose package entrypoints. For the reactor fast path, generate a
single-file bundle first, then set `FUNCTION_WASM_PYTHON_SCRIPT` to that bundle:

```bash
uv pip install --target /tmp/lf-puredeps mpmath
python3 tools/lf-bundle-python/lf_bundle_python.py \
  --root examples/lambda-feedback-fixtures/compare-boolean \
  --adapter-root examples/lambda-feedback-adapter \
  --include-root /tmp/lf-puredeps \
  --include-root tools/lf-bundle-python/polyfills/reactor \
  --eval-entrypoint evaluation_function.evaluation:evaluation_function \
  --preview-entrypoint evaluation_function.preview:preview_function \
  --out /tmp/compare-boolean.bundle.py
```

`python-reactor.wasm` v1.0.11 has been verified with bundle fixtures for
boilerplate, compareBoolean/SymPy, ArrayEqual/NumPy, and IsSimilar/NumPy.
SciPy remains Pyodide-only by policy.

**With read-only dataset mount:**

```bash
docker run --rm \
  -e FUNCTION_INTERFACE=wasm \
  -e FUNCTION_COMMAND=/app/eval.wasm \
  -e FUNCTION_WASM_ALLOWED_PATHS=/data/reference \
  -v /host/reference-data:/data/reference:ro \
  -p 8080:8080 \
  ghcr.io/lambda-feedback/shimmy-wasm:latest
```

---

## Example: AWS Lambda (`lambda.go`)

```go
// Environment variables set in the Lambda function configuration:
// FUNCTION_INTERFACE=reactor-python
// FUNCTION_COMMAND=/var/task/python-reactor.wasm
// FUNCTION_WASM_PYTHON_SCRIPT=/var/task/eval.py
// FUNCTION_WASM_COMPILE_CACHE=/tmp/wazero-cache
// FUNCTION_WASM_MAX_MEMORY_PAGES=8192
// FUNCTION_MAX_PROCS=2
// FUNCTION_TIMEOUT=25s
```

Lambda's `/tmp` filesystem persists across warm invocations within the same
execution environment, so `FUNCTION_WASM_COMPILE_CACHE=/tmp/wazero-cache`
means the compilation cache survives warm starts. Cold start time with a warm
cache is dominated by `py_init()` (~7-8 s per pool slot), not compilation.
