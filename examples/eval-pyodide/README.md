# Pyodide evaluator fallback

This directory contains a small Node.js runner for Python evaluators that need
Pyodide packages unavailable in Shimmy's CPython-WASI route. It uses Shimmy's
existing persistent `rpc`/stdio worker interface; it does not add a dispatcher
or a new wire protocol.

## Install the pinned runtime

The npm package is pinned to Pyodide **0.27.7**. Install it on the machine that
will run the demo (not as part of Shimmy startup):

```bash
cd examples/eval-pyodide
npm ci
```

Pyodide 0.27.7 is retained because its package index includes `gensim`, which
is needed by the data-bearing Lambda Feedback `shortTextAnswer` example.

## Legacy script mode

Pass a Python file as the first runner argument, or set
`FUNCTION_PYODIDE_SCRIPT`. The file may expose either API:

```python
# Existing evaluator API.
def evaluation_function(response, answer, params=None):
    return {"is_correct": True, "feedback": "ok"}

# Or the evaluator-owned RPC API.
def dispatch(method, payload):
    if method == "eval":
        return evaluation_function(**payload)
    raise LookupError(method)
```

Legacy requests execute the source in a fresh Python namespace. This keeps
state from one request out of the next request without taking a Pyodide memory
snapshot.

Run it through Shimmy with the existing RPC settings:

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

A request uses Shimmy's normal `Command: eval` header and the usual
`{"response": ..., "answer": ..., "params": ...}` JSON body.

## Lambda Feedback package mode

Package mode is for an evaluator root prepared by the stacked Lambda Feedback
adapter slice. It keeps the evaluator import alive between requests and
mirrors the package and its data files into Pyodide's virtual filesystem.

```bash
ROOT="$PWD"
FUNCTION_INTERFACE=rpc \
FUNCTION_RPC_TRANSPORT=stdio \
FUNCTION_COMMAND=node \
FUNCTION_ARGS="$ROOT/examples/eval-pyodide/runner.js" \
FUNCTION_PYODIDE_ROOT="$ROOT/.demo-pyodide-bundles/short-text-answer" \
FUNCTION_PYODIDE_EVAL_ENTRYPOINT=evaluation_function.evaluation:evaluation_function \
FUNCTION_PYODIDE_ADAPTER="$ROOT/tools/lf-bundle-python/adapter/lf_compat_adapter.py" \
FUNCTION_PYODIDE_PACKAGES="gensim,nltk" \
FUNCTION_MAX_PROCS=1 \
  go run . serve --host 127.0.0.1 --port 18080
```

`FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT` is optional. If it is absent, preview
requests use the evaluation entrypoint. The adapter path is intentionally an
external input: this slice does not duplicate the adapter.

## Examples and bundle preparation

- `../eval-scipy/eval.py` runs numeric checks and a real
  `scipy.stats.ttest_1samp`.
- `../eval-matplotlib/eval.py` uses `matplotlib.pyplot` with the `Agg` backend
  and returns the size of a real PNG rendered into memory.
- `scripts/prepare-short-text-pyodide-bundle.py` copies the shortTextAnswer
  runtime files and downloads the NLTK data markers it needs.

From the repository root, after installing the pinned npm package:

```bash
scripts/demo-python-examples.sh pyodide-only
scripts/prepare-short-text-pyodide-bundle.py \
  --source .demo-lambda-sources/shortTextAnswer/app \
  --out .demo-pyodide-bundles/short-text-answer
```

The demo script uses only `FUNCTION_INTERFACE=rpc`, `FUNCTION_COMMAND=node`,
and `FUNCTION_ARGS`; the Pyodide runner is just the RPC worker process.

## Framing

The runner reads and writes JSON-RPC 2.0 messages with LSP-style
`Content-Length: <bytes>\r\n\r\n` framing. Logs go to stderr so stdout remains the RPC stream.
