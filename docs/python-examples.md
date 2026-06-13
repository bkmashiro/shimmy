# Python runtime routes

Shimmy's Python story is a runtime capability matrix, not three equally recommended paths.

| Route | Directory | Runtime | Use when | Trade-off |
|---|---|---|---|---|
| Plain Python | `examples/eval-python/` | `reactor-python` | Standard-library / pure-Python evaluators; fastest Python path with clean per-request state. | Linux-only in this branch; package support limited by CPython-WASI. |
| NumPy | `examples/eval-numpy/` | `reactor-python` | Array/vector comparison and the main "warm CPython + snapshot/restore" demo. | Requires a `python-reactor.wasm` built with compatible packages. |
| SciPy / heavy Python | `examples/eval-scipy/` | `pyodide` via `examples/eval-pyodide/runner.js` | SciPy/Pandas/scikit-learn style dependencies and broad package compatibility. | Slowest/heaviest path; different isolation/performance model. |
| Legacy resident Python | `examples/eval-python/` | `python-wasm` | Compatibility or comparison only. | Interpreter stays alive across requests; namespace cleanup cannot reset the full CPython heap, so state can leak. |

One command:

```bash
scripts/demo-python-examples.sh
```

Notes:

- `reactor-python` is the recommended Python backend for speed plus snapshot/restore isolation.
- `python-wasm` is the old resident backend and should be treated as legacy/deprecated for isolation-sensitive evaluation.
- `reactor-python` and `python-wasm` are Linux-only in this branch; on macOS the script validates evaluator files directly when possible, then skips backend-specific parts.
- Pyodide is the compatibility fallback for heavy scientific Python because current CPython-WASI cannot load SciPy's compiled extension stack.
