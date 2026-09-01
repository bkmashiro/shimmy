# SciPy evaluator example

`eval.py` is a Pyodide-only example for SciPy statistical grading. It supports
numeric comparison and a one-sample `scipy.stats.ttest_1samp`; the script also
exports the evaluator-owned `dispatch(method, payload)` API used by Shimmy's
RPC worker.

Run through the shared Node/Pyodide runner:

```bash
cd examples/eval-pyodide
npm ci
cd ../..

ROOT="$PWD"
FUNCTION_INTERFACE=rpc \
FUNCTION_RPC_TRANSPORT=stdio \
FUNCTION_COMMAND=node \
FUNCTION_ARGS="$ROOT/examples/eval-pyodide/runner.js,$ROOT/examples/eval-scipy/eval.py" \
FUNCTION_PYODIDE_PACKAGES=scipy \
FUNCTION_MAX_PROCS=1 \
  go run . serve --host 127.0.0.1 --port 18080
```

Example `eval` body:

```json
{
  "response": "",
  "answer": "5.0",
  "params": {
    "test": "ttest",
    "samples": [4.9, 5.1, 5.0, 5.2, 4.8],
    "alpha": 0.05
  }
}
```
