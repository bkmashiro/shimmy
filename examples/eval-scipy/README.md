# eval-scipy — SciPy statistical evaluator

This example is the **heavy Python** route for Shimmy demos.

| Example | Runtime route | Why |
|---|---|---|
| `examples/eval-python/` | `python-wasm` | Plain standard-library Python; lowest dependency footprint. |
| `examples/eval-numpy/` | `reactor-python` | NumPy inside CPython-WASI with snapshot/restore isolation. |
| `examples/eval-scipy/` | `pyodide` | SciPy needs compiled extension packages; Pyodide ships them as WASM wheels. |

## Sample request

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

Expected result: `is_correct: true`, because the sample mean is not significantly different from `5.0` at `alpha=0.05`.

## Run via the shared Pyodide runner

```bash
cd examples/eval-pyodide
npm ci

FUNCTION_INTERFACE=pyodide \
FUNCTION_PYODIDE_RUNNER=$PWD/runner.js \
FUNCTION_PYODIDE_SCRIPT=../eval-scipy/eval.py \
  ../../bin/shimmy serve --host 127.0.0.1 --port 18080
```

Or run all three Python routes:

```bash
scripts/demo-python-examples.sh
```
