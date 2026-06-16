# Python runtime routes

Shimmy's Python story is a runtime capability matrix, not three equally recommended paths. Conceptually, Python should be described as a WASM/runtime **profile or compatibility lane**, not as proof that every source language needs its own peer `FUNCTION_INTERFACE`; see [`wasm-backend-model.md`](wasm-backend-model.md) for the runtime/profile/build split.

| Route | Directory | Runtime | Use when | Trade-off |
|---|---|---|---|---|
| Plain Python | `examples/eval-python/` | `reactor-python` | Standard-library / pure-Python evaluators; fastest Python path with clean per-request state. | Linux-only in this branch; package support limited by CPython-WASI. |
| NumPy | `examples/eval-numpy/` | `reactor-python` | Array/vector comparison and the main "warm CPython + snapshot/restore" demo. | Requires a `python-reactor.wasm` built with compatible packages. |
| SciPy / heavy Python | `examples/eval-scipy/` | `pyodide` via `examples/eval-pyodide/runner.js` | SciPy/Pandas/scikit-learn style dependencies and broad package compatibility. | Slowest/heaviest path; different isolation/performance model. |
| Lambda Feedback package | `examples/lambda-feedback-fixtures/` + `examples/lambda-feedback-adapter/` | `pyodide` package mode or `reactor-python` package mode | Real package-style evaluators that import `lf_toolkit` and use `evaluation_function/...` modules. | Pyodide is the broad compatibility path; reactor runs a generated wrapper produced by `tools/lf-bundle-python` at startup. |
| Legacy resident Python | `examples/eval-python/` | `python-wasm` | Compatibility or comparison only. | Interpreter stays alive across requests; namespace cleanup cannot reset the full CPython heap, so state can leak. |

One command:

```bash
scripts/demo-python-examples.sh

# Real Lambda Feedback package fixtures + adapter/Pyodide package mode.
scripts/demo-lambda-feedback-fixtures.sh all

# Reactor fast-path bundle matrix (downloads/verifies v1.0.13 artifact, Docker/Linux).
scripts/demo-reactor-lambda-feedback-bundles.sh docker
```

Notes:

- `reactor-python` is the recommended Python backend for speed plus snapshot/restore isolation when the evaluator is a single script or a Lambda Feedback package configured with `FUNCTION_LF_ROOT` + `FUNCTION_LF_EVAL_ENTRYPOINT`.
- Pyodide package mode is the default compatibility path for real Lambda Feedback package evaluators (`evaluation_function/...`, `lf_toolkit`, entrypoint modules), especially when they need SciPy or other heavy packages.
- The verified `python-reactor.wasm` v1.0.13 artifact includes NumPy/SymPy and exports both the generic `evaluate` ABI and legacy `py_exec`/`resp_buf`/`resp_len` compatibility ABI; extra pure-Python deps such as `mpmath` can be bundled with `FUNCTION_LF_INCLUDE_ROOTS` or supplied as ZIP/directory payloads with `FUNCTION_LF_SYS_PATH`, and small WASI gaps can be handled with narrow polyfills.
- `scipy` is intentionally out of reactor scope; use Pyodide for SciPy-heavy evaluators.
- `python-wasm` is the old resident backend and should be treated as legacy/deprecated for isolation-sensitive evaluation.
- `reactor-python` and `python-wasm` are Linux-only in this branch; on macOS the script validates evaluator files directly when possible, then skips backend-specific parts.
- Pyodide is the compatibility fallback for heavy scientific Python because current CPython-WASI cannot load SciPy's compiled extension stack.
- Runtime selection is explicit (`FUNCTION_INTERFACE` and related env vars); Shimmy does not automatically choose a backend from imports or `requirements.txt`.
