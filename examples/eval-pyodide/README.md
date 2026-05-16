# eval-pyodide — Pyodide/Node.js fallback runner

A Node.js-based runner that executes Python eval functions using
[Pyodide](https://pyodide.org) (Python compiled to WebAssembly for Node.js).

## Why this exists

CPython-WASI cannot load scipy or pandas because those packages depend on
LAPACK/BLAS via f2c-compiled Fortran code that requires native shared objects.
Pyodide ships its own WebAssembly builds of numpy, scipy, and pandas, so all
three work inside Node.js without any native libraries.

shimmy uses the existing `rpc` dispatcher + subprocess mode:

```
FUNCTION_INTERFACE=rpc
FUNCTION_COMMAND=node /path/to/runner.js /path/to/eval.py
```

No new dispatcher is needed. The runner speaks the same JSON-RPC 2.0 protocol
over stdio that shimmy already uses for subprocess workers.

## State isolation

Each request runs `evaluation_function` in a fresh Python namespace via
`exec(source, {})`. This gives per-request state isolation without memory
snapshots (Pyodide's JS-side state cannot be snapshot-restored).

## Prerequisites

- Node.js ≥ 18
- `npm install pyodide` (or `yarn add pyodide`)

```bash
cd examples/eval-pyodide
npm install pyodide
```

Pyodide's npm package (~100 MB) ships the Python runtime and a package index.
The `runner.js` loads `scipy` on startup via `loadPackage`, which fetches it
from the CDN on first run (or from a local mirror if `PYODIDE_PACKAGE_URL` is
set).

## Running

```bash
# Direct:
node runner.js eval.py

# Via shimmy:
FUNCTION_INTERFACE=rpc \
FUNCTION_RPC_TRANSPORT=stdio \
FUNCTION_COMMAND="node $(pwd)/runner.js $(pwd)/eval.py" \
  shimmy
```

## Wire protocol

The runner speaks JSON-RPC 2.0 framed with LSP-style `Content-Length` headers
(same as shimmy's built-in rpc subprocess adapter):

```
Content-Length: <N>\r\n
\r\n
{"jsonrpc":"2.0","id":1,"method":"evaluate","params":[{"response":"42","answer":"42"}]}
```

shimmy sends `method` as the configured function interface method name
(typically `"evaluate"`). The runner always dispatches to
`evaluation_function(response, answer, params)` in the loaded script,
regardless of the method name.

## eval.py contract

The loaded Python script must define:

```python
def evaluation_function(response, answer, params=None) -> dict:
    ...
    return {
        "is_correct": bool,
        "feedback": str,
        # any additional fields are returned to the caller
    }
```

## Example eval.py

`eval.py` in this directory demonstrates:

- **numeric mode** (default): exact floating-point comparison using
  `math.isclose` with configurable absolute tolerance.
- **ttest mode**: one-sample t-test via `scipy.stats.ttest_1samp` to check
  whether a set of student samples is consistent with the reference mean.

```python
# numeric
{"response": "3.14159", "answer": "3.14159", "params": {"test": "numeric"}}

# t-test
{"response": null, "answer": "5.0", "params": {
    "test": "ttest",
    "samples": [4.9, 5.1, 5.0, 5.2, 4.8]
}}
```

## Offline / air-gapped deployments

To avoid CDN fetches, pre-install Pyodide packages and point the runner at a
local mirror:

```bash
# Download scipy wheel into a local dir
node -e "
const { loadPyodide } = require('pyodide');
loadPyodide().then(py => py.loadPackage(['scipy']));
"
```

Or set `PYODIDE_PACKAGE_URL` to a local HTTP server hosting the Pyodide
package index.
