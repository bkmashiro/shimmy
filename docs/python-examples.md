# Python example routes

Shimmy now keeps the Python demo story as three separate routes instead of one overloaded example.

| Route | Directory | Runtime | Sample input | Use when |
|---|---|---|---|---|
| Plain Python | `examples/eval-python/` | `python-wasm` | `response="3.14159"`, `answer="3.1416"`, `params={"tolerance":0.001}` | Standard-library evaluators; fastest and simplest path. |
| NumPy | `examples/eval-numpy/` | `reactor-python` | `response="1,2,3.000001"`, `answer="1,2,3"`, `params={"rtol":1e-5}` | Vector / array comparison with snapshot/restore isolation. |
| SciPy | `examples/eval-scipy/` | `pyodide` via `examples/eval-pyodide/runner.js` | one-sample t-test over `params.samples` | Heavy scientific Python that CPython-WASI cannot import directly. |

One command:

```bash
scripts/demo-python-examples.sh
```

Notes:

- `python-wasm` and `reactor-python` are Linux-only in this branch; on macOS the script validates the plain/NumPy evaluator files directly when possible, then skips the backend-specific part.
- CI runs on Ubuntu, so it exercises the real `python-wasm` and `reactor-python` routes.
- SciPy uses Pyodide because current CPython-WASI cannot load SciPy's compiled extension stack.
