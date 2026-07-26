# Python runtime routes

Shimmy's Python story is a capability matrix, not several equally recommended
interfaces. Runtime selection remains explicit.

| Route | Directory | Runtime | Use when | Boundary |
|---|---|---|---|---|
| Plain Python | `examples/eval-python/` | `agent-python` | Standard-library and bundled pure-Python evaluators | Fresh single-use instance; no Host filesystem/network. |
| NumPy core | `examples/eval-numpy/` | `agent-python` | NumPy core/linalg workloads covered by the pinned artifact | NumPy 2.5.1; binary128 canary covered; SciPy absent. |
| SciPy/heavy Python | `examples/eval-scipy/` | Pyodide | SciPy/Pandas/scikit-learn and broad Emscripten packages | Heavier subprocess compatibility lane. |
| Lambda Feedback package | fixtures + adapter | Agent Python or Pyodide | Package-style `evaluation_function` modules | Boilerplate is Agent-qualified; other rows follow `capability-matrix.json`. |
| Legacy resident Python | `examples/eval-python/` | `python-wasm` | Historical comparison only | Persistent interpreter state can leak. |

Agent Python smoke:

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

- `agent-python` is the canonical profile. `python-reactor`, `reactor-python`, and
  the legacy interface spelling are aliases.
- The bundle is manifest- and checksum-bound under
  `build/python-reactor/artifacts/`.
- The Host denies `agent_runtime_v1.host_call`; no capability is granted.
- Lambda Feedback pure-Python dependencies must be embedded at startup with
  `FUNCTION_LF_INCLUDE_ROOTS`. Guest `sys.path` mounts are rejected.
- SciPy remains a Pyodide route.
- `python-wasm` remains an independent legacy resident path and is not evidence
  for Agent Python isolation.
