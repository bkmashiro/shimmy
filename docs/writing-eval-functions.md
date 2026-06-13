# Writing Evaluation Functions for shimmy-wasm

This guide is for question authors who write the Python scripts that grade student responses in Lambda Feedback. Your script runs inside a **WASM sandbox** — a secure, isolated CPython interpreter. This page explains the interface, the available libraries, and the constraints you need to know.

---

## Quick Start

```python
def evaluation_function(response, answer, params=None):
    """
    Evaluate a student response.

    Args:
        response: the student's answer (string, from the submission form)
        answer:   the correct answer (string, set by the question author)
        params:   optional dict with extra config from the question settings

    Returns:
        dict with keys:
            is_correct (bool)   — whether the response is accepted
            feedback   (str)    — message shown to the student
    """
    try:
        r = float(response)
        a = float(answer)
    except ValueError:
        return {"is_correct": False, "feedback": "Please enter a number."}

    tol = float((params or {}).get("tolerance", 1e-9))
    if abs(r - a) <= tol:
        return {"is_correct": True, "feedback": f"Correct! {r} matches {a}."}
    return {
        "is_correct": False,
        "feedback": f"Incorrect. Got {r}, expected {a}. Error: {abs(r - a):.3g}",
    }
```

Save this as `eval.py` and test it locally with `shimmy-eval` (see below).

---

## Function Signatures

The sandbox calls one of two functions depending on the request type:

| Method    | Function looked up in script | Purpose |
|-----------|------------------------------|---------|
| `eval`    | `evaluation_function`        | Grade the student's response |
| `preview` | `preview_function` (falls back to `evaluation_function`) | Show a formatted preview |

Both functions receive the same three arguments:

```python
def evaluation_function(response, answer, params=None):
    ...

def preview_function(response, answer, params=None):
    ...
```

- **`response`** (`str`) — raw string from the student's submission
- **`answer`** (`str`) — correct answer string configured by the question author
- **`params`** (`dict | None`) — question-level settings (e.g. `{"tolerance": 0.01}`)

`preview_function` is optional. If not defined, `evaluation_function` is used for preview requests too.

### Return value

`evaluation_function` must return a dict with at least:
```python
{"is_correct": bool, "feedback": str}
```

You may include any additional keys (e.g. `absolute_error`, `relative_error`). They are passed through to the frontend.

`preview_function` must return:
```python
{"preview": str}   # LaTeX or plain text shown to the student
```

### Error handling

If your function raises an exception the sandbox catches it and returns a structured error. The full HTTP response looks like:

```json
{
  "command": "eval",
  "result": {
    "error":      "invalid literal for int() with base 10: 'abc'",
    "error_type": "ValueError",
    "lineno":     3,
    "traceback":  "Traceback (most recent call last):\n  File \"<eval>\", line 3, ..."
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `error` | string | Exception message (`str(e)`) |
| `error_type` | string | Exception class name (e.g. `ValueError`, `ImportError`) |
| `lineno` | int | Line number in your script where the exception originated |
| `traceback` | string | Full formatted traceback from `traceback.format_exc()` |

This means an unhandled exception never crashes the evaluator. You may also catch exceptions yourself and return `{"is_correct": false, "feedback": "..."}` for cleaner student-facing UX.

---

## Available Libraries

| Library | Status | Notes |
|---------|--------|-------|
| Python 3.14 stdlib | ✅ Available | `math`, `decimal`, `fractions`, `re`, `json`, `itertools`, … |
| `numpy` | ✅ Available | 1.26 (C extensions statically linked) |
| `sympy` | ✅ Available | Pure Python — CAS, symbolic maths |
| `scipy` | ❌ Not available | Requires C extensions — use `numpy` or `sympy` instead |
| `pandas` | ❌ Not available | Requires C extensions |
| `matplotlib` | ❌ Not available | No display backend in WASI |
| `torch` / `tensorflow` | ❌ Not available | Too large / requires GPU drivers |
| `requests` / `httpx` | ❌ Not available | **Network access is disabled** |

Importing an unavailable package gives a clear error:

```
ImportError: 'scipy' is not available in the shimmy-wasm sandbox.
Reason: scipy requires compiled C extensions; use a Pyodide-based runtime instead.
Available packages: numpy, sympy, and the Python standard library (no network).
```

---

## Sandbox Constraints

### Per-request isolation

Every submission runs in a **clean Python interpreter state** — no shared globals, no leftover imports, no side effects from previous requests. This is guaranteed by WASM memory snapshot/restore, not just `exec` namespace cleanup.

Practical implication: you cannot cache expensive results between requests. If your grader imports a large module, it will be re-imported for each request.

### No randomness between requests

`numpy.random` and `random` module state is reset on every request. Do not rely on stochastic grading — the same input will always produce the same output.

### Timeout

Each request has a **30-second timeout** (configurable via `FUNCTION_TIMEOUT`). Scripts that enter an infinite loop are killed after this time and the student receives a timeout error. Keep evaluation logic well under 1 second for normal workloads.

### No filesystem writes

The sandbox mounts the filesystem read-only. You can read files packaged with the binary (stdlib, numpy, sympy) but cannot write to disk.

### No network

All network syscalls are blocked. `import requests` will fail at import time.

---

## Numeric Grading Patterns

### Float comparison with tolerance

```python
import math

def evaluation_function(response, answer, params=None):
    tol = float((params or {}).get("tolerance", 1e-6))
    try:
        r, a = float(response), float(answer)
    except ValueError:
        return {"is_correct": False, "feedback": "Expected a number."}

    err = abs(r - a)
    if err <= tol or (a != 0 and abs(err / a) <= tol):
        return {"is_correct": True, "feedback": f"Correct! ({r})"}
    return {"is_correct": False, "feedback": f"Got {r}, expected {a}. Error: {err:.3g}"}
```

### Symbolic equality with sympy

```python
import sympy

def evaluation_function(response, answer, params=None):
    try:
        r = sympy.sympify(response)
        a = sympy.sympify(answer)
    except Exception as e:
        return {"is_correct": False, "feedback": f"Could not parse expression: {e}"}

    diff = sympy.simplify(r - a)
    if diff == 0:
        return {"is_correct": True, "feedback": "Correct!"}
    return {"is_correct": False, "feedback": f"Your answer simplifies to {sympy.latex(r)}, expected {sympy.latex(a)}."}


def preview_function(response, answer, params=None):
    try:
        r = sympy.sympify(response)
        return {"preview": f"$$\\displaystyle {sympy.latex(r)}$$"}
    except Exception:
        return {"preview": response}
```

### Vector / matrix with numpy

```python
import json
import numpy as np

def evaluation_function(response, answer, params=None):
    try:
        r = np.array(json.loads(response))
        a = np.array(json.loads(answer))
    except Exception as e:
        return {"is_correct": False, "feedback": f"Could not parse: {e}"}

    if r.shape != a.shape:
        return {"is_correct": False, "feedback": f"Wrong shape: got {r.shape}, expected {a.shape}."}

    tol = float((params or {}).get("tolerance", 1e-6))
    if np.allclose(r, a, atol=tol):
        return {"is_correct": True, "feedback": "Correct!"}
    return {"is_correct": False, "feedback": f"Max error: {np.max(np.abs(r - a)):.3g}"}
```

> **Tip**: Use `import json; r = np.array(json.loads(response))` rather than `eval()` to safely parse student-submitted arrays.

---

## Lambda Feedback package evaluators

Real Lambda Feedback Python evaluators often use a package layout rather than a
single `eval.py` file:

```text
evaluation_function/
  __init__.py
  main.py
  evaluation.py
  preview.py
```

In that layout, the evaluator usually imports `lf_toolkit` and exposes package
entrypoints such as:

```python
# evaluation_function/evaluation.py
from lf_toolkit.evaluation import Result


def evaluation_function(response, answer, params):
    return Result(is_correct=response == answer)
```

Shimmy's compatibility adapter can run these packages without changing the
author-facing function names. Configure Pyodide package mode explicitly:

```bash
FUNCTION_INTERFACE=pyodide \
FUNCTION_PYODIDE_RUNNER=$PWD/examples/eval-pyodide/runner.js \
FUNCTION_PYODIDE_ROOT=$PWD/examples/lambda-feedback-fixtures/boilerplate-python \
FUNCTION_PYODIDE_EVAL_ENTRYPOINT=evaluation_function.evaluation:evaluation_function \
FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT=evaluation_function.preview:preview_function \
FUNCTION_PYODIDE_ADAPTER=$PWD/examples/lambda-feedback-adapter/lf_compat_adapter.py \
FUNCTION_PYODIDE_PACKAGES=sympy
```

Notes:

- `FUNCTION_INTERFACE=pyodide` is the current default compatibility path for
  package-style Lambda Feedback evaluators.
- `FUNCTION_INTERFACE=reactor-python` directly accepts a single script via
  `FUNCTION_WASM_PYTHON_SCRIPT`; it rejects Pyodide package-mode env vars with
  an explicit error.
- To use a package-style evaluator on reactor without designing a full package
  runtime, first generate a single-file bundle:

```bash
python3 tools/lf-bundle-python/lf_bundle_python.py \
  --root examples/lambda-feedback-fixtures/boilerplate-python \
  --adapter-root examples/lambda-feedback-adapter \
  --eval-entrypoint evaluation_function.evaluation:evaluation_function \
  --preview-entrypoint evaluation_function.preview:preview_function \
  --out /tmp/boilerplate.bundle.py

FUNCTION_INTERFACE=reactor-python \
FUNCTION_COMMAND=/path/to/python-reactor.wasm \
FUNCTION_WASM_PYTHON_SCRIPT=/tmp/boilerplate.bundle.py \
./shimmy serve
```

- The bundle embeds evaluator package modules and the minimal `lf_toolkit` shim,
  but it does not vendor third-party dependencies. `numpy`/`sympy` must already
  be available in the chosen reactor artifact, or use Pyodide package mode.
- Runtime selection is explicit. Shimmy does not infer the backend from imports
  or `requirements.txt`.
- Try the local fixture demo with:

```bash
scripts/demo-lambda-feedback-fixtures.sh all
```

---

## Local Testing with `shimmy-eval`

`shimmy-eval` lets you run your script against the real WASM sandbox on your laptop before deploying.

### Installation

```bash
# requires Go 1.22+ and Linux (WASM sandbox is Linux-only)
git clone https://github.com/bkmashiro/shimmy-wasm-go
cd shimmy-wasm-go
go build -o shimmy-eval ./cmd/eval

# download the binary
curl -fsSL https://github.com/bkmashiro/webassembly-language-runtimes/releases/download/v1.0.9/python-reactor.wasm \
    -o python-reactor.wasm
```

### Usage

```bash
shimmy-eval --wasm python-reactor.wasm eval.py \
    --input '{"response": "3.14", "answer": "3.14159", "params": {"tolerance": 0.01}}'
```

Output:

```json
{
  "is_correct": true,
  "feedback": "Correct! (3.14)"
}
```

### Options

```
--wasm PATH      Path to python-reactor.wasm (or set PYTHON_REACTOR_WASM env var)
--input JSON     Input object with response/answer/params keys
--method STRING  "eval" (default) or "preview"
--timeout INT    Timeout in seconds (default: 120)
--pretty         Pretty-print output (default: true)
```

Exit code is `0` on success, `1` if the result contains an `"error"` key or if the subprocess fails.

---

## Common Errors

| Error | Cause | Fix |
|-------|-------|-----|
| `ImportError: 'scipy' is not available` | Library not in sandbox | Use numpy/sympy equivalent |
| `py_exec timed out after 30s` | Infinite loop or very slow computation | Add early exit, reduce complexity |
| `name 'eval' is not defined` | Script missing `eval()` function | Add the function |
| `JSONDecodeError` | `response` is not valid JSON | Validate input before parsing |
| `py_exec: _handle_request raised an unhandled exception` | Exception escaped the C-level catch (should not happen in normal use) | Check for syntax errors in the except block itself |

---

## Deployment

Set these environment variables in your evaluator container:

| Variable | Value | Description |
|----------|-------|-------------|
| `FUNCTION_INTERFACE` | `reactor-python` | Select the WASM reactor backend |
| `FUNCTION_WASM_MODULE` | `/app/python-reactor.wasm` | Path to the WASM binary |
| `FUNCTION_WASM_PYTHON_SCRIPT` | `/app/eval.py` | Path to your eval script |
| `FUNCTION_TIMEOUT` | `30` | Per-request timeout in seconds (default: 30) |
| `FUNCTION_MAX_PROCS` | `2` | Runner pool size (default: NumCPU, max 4) |
| `FUNCTION_WASM_COMPILE_CACHE` | `/var/cache/wazero` | **Recommended.** On-disk JIT cache for the WASM binary. Eliminates the ~2-minute cold-compile on first start; subsequent starts are instant. The directory is created automatically. |
