# Deployment recipes

This page is the short version of the current runtime/profile/env surface. Use it when launching Shimmy or wiring Lambda smoke tests.

## Rule of thumb

`FUNCTION_INTERFACE` names the execution boundary. It is not the evaluator language.

- Use `FUNCTION_INTERFACE=wasm` for in-process wazero execution.
- Use `FUNCTION_WASM_PROFILE` only when a WASM module needs profile-specific setup.
- Use fixed artifact versions and SHA256 checks for large runtime images.
- Keep `FUNCTION_INTERFACE=reactor-python` only as a compatibility alias; new deployments should use `wasm` + `FUNCTION_WASM_PROFILE=python-reactor`.
- Environment variables select a path after its runtime artifacts are deployed. They do not install Node/Pyodide, DynamoRIO clients, or the reactor artifact automatically.

## Recommended recipes

| Scenario | Use when | Required env | Optional env | Notes |
|---|---|---|---|---|
| Generic WASM | Evaluator is already a WASI module exposing Shimmy ABI. | `FUNCTION_INTERFACE=wasm`; `FUNCTION_COMMAND=/path/to/eval.wasm` or `FUNCTION_WASM_MODULE=/path/to/eval.wasm` | `FUNCTION_WASM_PROFILE=generic`; `FUNCTION_MAX_PROCS`; `FUNCTION_WORKER_SEND_TIMEOUT`; `FUNCTION_WASM_COMPILE_CACHE` | Runtime expects `alloc(len)` + `evaluate(ptr, len)` returning `[uint32 length][JSON]`. Source language is irrelevant. |
| Python-reactor script | Plain Python / NumPy/SymPy-compatible evaluator script, fast in-process WASM path. | `FUNCTION_INTERFACE=wasm`; `FUNCTION_WASM_PROFILE=python-reactor`; `FUNCTION_WASM_MODULE=/path/to/python-reactor.wasm`; `FUNCTION_WASM_PYTHON_SCRIPT=/path/to/eval.py` | `FUNCTION_MAX_PROCS`; `FUNCTION_WORKER_SEND_TIMEOUT`; `FUNCTION_WASM_COMPILE_CACHE`; `FUNCTION_WASM_PYTHON_PRELOAD` | Preferred Python fast path. The current pin is release `v1.0.14` from `bkmashiro/webassembly-language-runtimes`; default evaluator preload requires its `py_prepare` export. |
| Python-reactor Lambda Feedback package | LF package-style evaluator that can run with packages already in the reactor artifact plus bundled pure-Python deps. | `FUNCTION_INTERFACE=wasm`; `FUNCTION_WASM_PROFILE=python-reactor`; `FUNCTION_WASM_MODULE=/path/to/python-reactor.wasm`; `FUNCTION_LF_ROOT=/path/to/package`; `FUNCTION_LF_EVAL_ENTRYPOINT=module:function` | `FUNCTION_LF_CONFIG`; `FUNCTION_LF_PREVIEW_ENTRYPOINT`; `FUNCTION_LF_ADAPTER_ROOT`; `FUNCTION_LF_BUNDLER`; `FUNCTION_LF_INCLUDE_ROOTS`; `FUNCTION_LF_SYS_PATH`; `FUNCTION_LF_BUNDLE_OUT` | Shimmy runs the bundler once at startup, then executes the generated script through python-reactor. |
| Pyodide compatibility | Heavy Python package stack, especially SciPy/Pandas or Emscripten/Pyodide-only packages. | `FUNCTION_INTERFACE=pyodide`; `FUNCTION_PYODIDE_RUNNER=/path/to/runner.js`; plus either `FUNCTION_PYODIDE_SCRIPT=/path/to/eval.py` or package-mode envs | `FUNCTION_PYODIDE_PACKAGES`; `FUNCTION_PYODIDE_ROOT`; `FUNCTION_PYODIDE_EVAL_ENTRYPOINT`; `FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT`; `FUNCTION_PYODIDE_ADAPTER` | This is a Node/Pyodide subprocess lane, not the same in-process wazero pool as `wasm`. |
| Legacy RPC/file | Existing non-WASM workers or fallback integrations. | `FUNCTION_INTERFACE=rpc` or `FUNCTION_INTERFACE=file`; `FUNCTION_COMMAND=...` | RPC transport/file-mode worker options | Keep for compatibility and comparison. |

## Verification status

| Path | Local/config verification | Target/integration evidence |
|---|---|---|
| Python-reactor | Dispatcher/profile tests, package-bundling startup tests, pinned artifact hash/export verification | CodeBuild Linux schema-v2/v3 workload runs through the real Shimmy HTTP path with `v1.0.14` |
| Pyodide | Dispatcher/script/package-mode routing tests and the checked-in Node runner contract | GitHub Actions 17-row HTTP E2E plus CodeBuild schema-v2/v3 Pure/NumPy/SymPy/SciPy runs |
| Native + DBI | Wrapper argv/config/fail-closed unit tests for both `file` and `rpc` | Real Lambda x86_64 Python and Lean HTTP E2E plus the bounded seven-rule policy matrix |

Canonical raw JSON and interpretation reports are indexed by the companion
`shimmy-docs/docs/current/evidence/README.md`. The evidence verifies the listed
paths and tested boundaries, not automatic dependency installation, arbitrary
package compatibility, or a complete production DBI sandbox.

## Artifact policy for `python-reactor.wasm`

Source of truth: `bkmashiro/webassembly-language-runtimes`, workflow `build-python-reactor.yml`. The local pin used by scripts lives in `scripts/python-reactor-artifact.env`; verify it with `scripts/verify-python-reactor-artifact.sh`.

Current artifact used by this branch:

```text
release: https://github.com/bkmashiro/webassembly-language-runtimes/releases/tag/v1.0.14
asset:   python-reactor.wasm
sha256:  78dcbb6d673351c0d3b776c42d2fb93b6f638cdc714d58072dece4b115edaa72
exports: py_init, py_prepare, evaluate, py_exec, alloc, dealloc, resp_buf, resp_len
```

The verification script downloads the pinned release into the artifact cache when
needed. Historical checked-in LFS fixtures are compatibility test inputs, not the
deployment source of truth and need not be replaced for each release. For Lambda
Feedback handoff smoke, use [lambda-feedback-handoff.md](lambda-feedback-handoff.md).

## Examples

### Generic WASM

```bash
FUNCTION_INTERFACE=wasm \
FUNCTION_WASM_MODULE=examples/demo-stateful/eval.wasm \
FUNCTION_MAX_PROCS=1 \
./shimmy serve
```

### Python-reactor script

```bash
FUNCTION_INTERFACE=wasm \
FUNCTION_WASM_PROFILE=python-reactor \
FUNCTION_WASM_MODULE=internal/execution/wasm/testdata/python-reactor.wasm \
FUNCTION_WASM_PYTHON_SCRIPT=examples/eval-python/eval.py \
FUNCTION_MAX_PROCS=1 \
./shimmy serve
```

### Python-reactor LF package

```bash
FUNCTION_INTERFACE=wasm \
FUNCTION_WASM_PROFILE=python-reactor \
FUNCTION_WASM_MODULE=internal/execution/wasm/testdata/python-reactor.wasm \
FUNCTION_LF_ROOT=examples/lambda-feedback-fixtures/boilerplate-python \
FUNCTION_LF_EVAL_ENTRYPOINT=evaluation_function.evaluation:evaluation_function \
FUNCTION_LF_PREVIEW_ENTRYPOINT=evaluation_function.preview:preview_function \
FUNCTION_LF_ADAPTER_ROOT=examples/lambda-feedback-adapter \
FUNCTION_LF_BUNDLER=tools/lf-bundle-python/lf_bundle_python.py \
./shimmy serve
```

### Pyodide compatibility

```bash
FUNCTION_INTERFACE=pyodide \
FUNCTION_PYODIDE_RUNNER=examples/eval-pyodide/runner.js \
FUNCTION_PYODIDE_SCRIPT=examples/eval-pyodide/eval.py \
FUNCTION_PYODIDE_PACKAGES=scipy,numpy \
./shimmy serve
```

## Compatibility aliases

These still work but should not be used for new deployment docs:

- `FUNCTION_INTERFACE=reactor-python` — compatibility alias for the python-reactor profile.
- `FUNCTION_COMMAND=/path/to/module.wasm` — still accepted for WASM module paths, but `FUNCTION_WASM_MODULE` is clearer in deployment recipes.
- `FUNCTION_INTERFACE=python-wasm` — older resident Python/WASM path; keep only for comparison/legacy tests.
