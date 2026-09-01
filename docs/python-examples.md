# Python evaluator examples

Shimmy keeps runtime selection explicit. Use the existing persistent RPC worker
interface for the Pyodide fallback:

| Example | Worker | Pyodide packages | Purpose |
|---|---|---|---|
| `examples/eval-scipy/` | Node + Pyodide | `scipy` | SciPy statistics unavailable in the CPython-WASI example route |
| `examples/eval-matplotlib/` | Node + Pyodide | `matplotlib` | Real headless `matplotlib.pyplot` PNG output |
| Lambda Feedback bundle | Node + Pyodide | `gensim,nltk` | Package-mode `shortTextAnswer`; adapter comes from stacked PR 1 |

## Direct RPC worker configuration

```bash
ROOT="$PWD"
FUNCTION_INTERFACE=rpc \
FUNCTION_RPC_TRANSPORT=stdio \
FUNCTION_COMMAND=node \
FUNCTION_ARGS="$ROOT/examples/eval-pyodide/runner.js,$ROOT/examples/eval-scipy/eval.py" \
FUNCTION_PYODIDE_PACKAGES=scipy \
FUNCTION_MAX_PROCS=1 \
  go run . serve --host 127.0.0.1 --port 18080
```

Use `Command: eval` and Shimmy's normal request body. Replace the script and
package list for the matplotlib example. `examples/eval-pyodide/package.json`
pins the runner to Pyodide 0.27.7; run `npm ci` in that directory on the demo
machine before starting the worker.

## Real PNG example

`examples/eval-matplotlib/eval.py` selects `matplotlib.use("Agg")`, imports
`matplotlib.pyplot`, calls `figure.savefig(..., format="png")`, and returns the
rendered byte count. It does not use a placeholder result.

## Data-bearing shortTextAnswer bundle

The preparation script copies the evaluator's runtime files and populates the
required NLTK data directories:

```bash
scripts/prepare-short-text-pyodide-bundle.py \
  --source .demo-lambda-sources/shortTextAnswer/app \
  --out .demo-pyodide-bundles/short-text-answer
```

Package mode then points `FUNCTION_PYODIDE_ROOT` at that output and supplies the
adapter from `tools/lf-bundle-python/adapter/` when PR 1 is stacked. See the
Pyodide README for the complete environment block.

For a SciPy + matplotlib HTTP smoke test:

```bash
scripts/demo-python-examples.sh pyodide-only
```
