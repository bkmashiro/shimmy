# Python runtime routes

Shimmy's Python story is a capability matrix, not several equally recommended
interfaces. Runtime selection remains explicit.

| Route | Directory | Runtime | Use when | Boundary |
|---|---|---|---|---|
| Plain Python | `examples/eval-python/` | `shimmy-python` | Standard-library and bundled pure-Python evaluators | Fresh single-use instance; no Host filesystem/network. |
| NumPy core | `examples/eval-numpy/` | `shimmy-python` | Array, reshape, matmul, reduction, and other `numpy._core` workloads | NumPy 2.2.6; optional compiled subpackages such as `numpy.linalg` are absent; WASI long-double parsing falls back to binary64. |
| SciPy/heavy Python | `examples/eval-scipy/` | Pyodide | SciPy/Pandas/scikit-learn and broad Emscripten packages | Heavier subprocess compatibility lane. |
| Lambda Feedback package | fixtures + adapter | Shimmy Python or Pyodide | Package-style `evaluation_function` modules | Boilerplate is Shimmy-qualified; other rows follow `capability-matrix.json`. |
| Legacy resident Python | `examples/eval-python/` | `python-wasm` | Historical comparison only | Persistent interpreter state can leak. |

Shimmy Python smoke:

```bash
scripts/smoke-python-reactor-handoff.sh direct
scripts/demo-python-examples.sh reactor-only
```

Pyodide smoke:

```bash
(cd examples/eval-pyodide && npm ci)
scripts/demo-python-examples.sh pyodide-only
scripts/demo-lambda-feedback-fixtures.sh pyodide-boilerplate
```

Notes:

- `shimmy-python` is the canonical profile. `python-reactor`, `reactor-python`, and
  the legacy interface spelling are aliases.
- The bundle is manifest- and checksum-bound under
  `dist/shimmy-python/numpy-core/`.
- The guest imports only WASI; no custom Host capability is granted.
- Lambda Feedback pure-Python dependencies must be embedded at startup with
  `FUNCTION_LF_INCLUDE_ROOTS`. Guest `sys.path` mounts are rejected.
- SciPy remains a Pyodide route.
- `python-wasm` remains an independent legacy resident path and is not evidence
  for Shimmy Python isolation.
