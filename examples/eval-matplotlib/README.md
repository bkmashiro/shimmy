# Matplotlib evaluator example

This evaluator proves the Pyodide route can render a real PNG with
`matplotlib.pyplot`. It selects the headless `Agg` backend before importing
pyplot and returns the rendered byte count.

Run it through Shimmy's existing RPC/stdio interface:

```bash
ROOT="$PWD"
FUNCTION_INTERFACE=rpc \
FUNCTION_RPC_TRANSPORT=stdio \
FUNCTION_COMMAND=node \
FUNCTION_ARGS="$ROOT/examples/eval-pyodide/runner.js,$ROOT/examples/eval-matplotlib/eval.py" \
FUNCTION_PYODIDE_PACKAGES=matplotlib \
FUNCTION_MAX_PROCS=1 \
  go run . serve --host 127.0.0.1 --port 18080
```
