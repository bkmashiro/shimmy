# Python runtime routes

Shimmy's Python story is a runtime capability matrix, not three equally recommended paths. Conceptually, Python should be described as a WASM/runtime **profile or compatibility lane**, not as proof that every source language needs its own peer `FUNCTION_INTERFACE`; see [`wasm-backend-model.md`](wasm-backend-model.md) for the runtime/profile/build split.

| Route | Directory | Runtime | Use when | Trade-off |
|---|---|---|---|---|
| Plain Python | `examples/eval-python/` | `reactor-python` | Primary Python architecture for standard-library / pure-Python evaluators after a clean artifact is accepted. | Artifact replacement pending; the old pin is frozen. |
| NumPy | `examples/eval-numpy/` | `reactor-python` | Target path for warm CPython plus snapshot/restore after replacement acceptance. | Requires an accepted reactor artifact with compatible packages. |
| SciPy / heavy Python | `examples/eval-scipy/` | `pyodide` via `examples/eval-pyodide/runner.js` | SciPy/Pandas/scikit-learn style dependencies and broad package compatibility. | Slowest/heaviest path; different isolation/performance model. |
| Lambda Feedback package | `examples/lambda-feedback-fixtures/` + `examples/lambda-feedback-adapter/` | `pyodide` package mode or `reactor-python` package mode | Real package-style evaluators that import `lf_toolkit` and use `evaluation_function/...` modules. | Pyodide is the broad compatibility path; reactor runs a generated wrapper produced by `tools/lf-bundle-python` at startup. |
| Legacy resident Python | `examples/eval-python/` | `python-wasm` | Compatibility or comparison only. | Interpreter stays alive across requests; namespace cleanup cannot reset the full CPython heap, so state can leak. |

Active local compatibility smoke:

```bash
(cd examples/eval-pyodide && npm ci)
scripts/demo-python-examples.sh pyodide-only

# Real Lambda Feedback package fixtures through Pyodide package mode.
scripts/demo-lambda-feedback-fixtures.sh pyodide-boilerplate
scripts/demo-lambda-feedback-fixtures.sh pyodide-compare-boolean
```

Notes:

- `reactor-python` remains the primary Python interface design for speed plus snapshot/restore isolation, but its old artifact pin is frozen. Do not run its demo/download scripts as current acceptance evidence until a clean replacement passes the handoff.
- Pyodide package mode is the default compatibility path for real Lambda Feedback package evaluators (`evaluation_function/...`, `lf_toolkit`, entrypoint modules), especially when they need SciPy or other heavy packages.
- A replacement reactor artifact must provide immutable provenance, SHA-256, required exports and package/API manifests, no-polyfill evidence, and real script/package/state-reset/error/timeout results before the pin or automatic CI changes.
- `scipy` is intentionally out of reactor scope; use Pyodide for SciPy-heavy evaluators.
- `python-wasm` is the old resident backend and should be treated as legacy/deprecated for isolation-sensitive evaluation.
- `reactor-python` and `python-wasm` are Linux-only in this branch; `python-wasm` is a retirement candidate after replacement evidence lands.
- Pyodide is the compatibility fallback for heavy scientific Python because current CPython-WASI cannot load SciPy's compiled extension stack.
- Runtime selection is explicit (`FUNCTION_INTERFACE` and related env vars); Shimmy does not automatically choose a backend from imports or `requirements.txt`.
