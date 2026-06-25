# demo-pyodide-python

A small Python evaluator that runs through Shimmy's `FUNCTION_INTERFACE=pyodide`
path.

Unlike the generic WASM backend, this path intentionally reuses Shimmy's existing
JSON-RPC stdio worker machinery: Shimmy starts `node runner.js eval.py`, and the
runner loads Pyodide's CPython WebAssembly runtime inside Node.js.

This is useful for Python evaluators that depend on packages available through
Pyodide. The bundled example is pure Python to keep the smoke test fast, but it
uses the same Pyodide runtime path.

## Install dependencies

```bash
cd examples/demo-pyodide-python
npm install
```

## Run through Shimmy

```bash
FUNCTION_INTERFACE=pyodide \
FUNCTION_PYODIDE_RUNNER=$(pwd)/examples/demo-pyodide-python/runner.js \
FUNCTION_PYODIDE_SCRIPT=$(pwd)/examples/demo-pyodide-python/eval.py \
./shimmy serve
```

The demo script from the repository root does this end-to-end:

```bash
./scripts/demo-pyodide-python.sh
```

The example evaluator keeps a global `invocation_count`. Legacy script mode
executes the evaluator source in a fresh Python namespace for every request, so
two requests should both report `guest_invocation_count: 1`.
