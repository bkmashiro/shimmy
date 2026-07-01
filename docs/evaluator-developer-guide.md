# Evaluator developer guide

This guide is for authors of Lambda Feedback-style evaluators who want to run
through Shimmy without learning the runtime internals first.

## The short version

Write one or two plain functions:

```python
def evaluation_function(response, answer, params=None):
    return {"is_correct": response.strip() == answer.strip(), "feedback": ""}


def preview_function(response, answer=None, params=None):
    return {"preview": response.strip()}
```

Then point Shimmy at the Python package root and the `module:function` names:

```bash
go run . \
  --interface file \
  --command python3 \
  --arg examples/lambda-feedback-adapter/lf_file_worker.py \
  --env FUNCTION_LF_ROOT=$PWD/examples/evaluator-python-minimal \
  --env FUNCTION_LF_ENTRYPOINT=evaluator:evaluation_function \
  --env FUNCTION_LF_PREVIEW_ENTRYPOINT=evaluator:preview_function \
  serve
```

See [`examples/evaluator-python-minimal`](../examples/evaluator-python-minimal/)
for a copy-pasteable evaluator plus eval/preview requests.

## Function signatures

### Eval

Use this shape for grading:

```python
def evaluation_function(response, answer, params=None):
    ...
```

Return a JSON-compatible object with at least `is_correct`:

```python
return {
    "is_correct": True,
    "feedback": "Correct."
}
```

`feedback` may be a string, list, or object. Extra JSON-compatible fields are
allowed.

### Preview

Use either of these common shapes:

```python
def preview_function(response, answer=None, params=None):
    ...
```

or:

```python
def preview_function(response, params=None):
    ...
```

Return a JSON-compatible object with `preview`:

```python
return {"preview": "rendered or normalized response"}
```

Shimmy's compatibility adapter detects the two-argument preview form so existing
Lambda Feedback preview helpers can keep their natural signature.

## Choosing a runtime

Evaluator authors should not need to think in terms of supervisor or snapshot
implementation details. Start with this decision table:

| Evaluator shape | Recommended path | Why |
|---|---|---|
| Pure Python or small dependencies | `python-reactor` when available | Faster resident Python/WASI path with snapshot isolation. |
| Heavy Python package stack, Pyodide-supported packages | `python-pyodide` | Broad compatibility for packages that are easier in Pyodide/Emscripten. |
| Maximum compatibility / simple local development | `file` | Easy to debug; runs normal host Python; slower because it crosses process/file boundaries. |
| Custom non-Python evaluator compiled to WASI | `generic-wasm` | Small ABI, fast warm workers, explicit snapshot controls. |

If in doubt while developing a new Python evaluator, start with `file`. Move to
`python-reactor` or `python-pyodide` when the evaluator behavior is correct and
you need the production runtime profile.

## Request shape

HTTP clients send the normal Shimmy request body. The command is selected by the
`command` HTTP header; absent means `eval`.

Eval:

```json
{
  "response": "42",
  "answer": "42",
  "params": {}
}
```

Preview:

```json
{
  "response": " draft answer ",
  "params": {"prefix": "Preview"}
}
```

## Entrypoints and package roots

The Python compatibility worker loads functions by `module:function` from a root
directory:

```text
FUNCTION_LF_ROOT=/path/to/evaluator-package
FUNCTION_LF_ENTRYPOINT=evaluator:evaluation_function
FUNCTION_LF_PREVIEW_ENTRYPOINT=evaluator:preview_function
```

For dynamic package selection, per-request routing can pass these inside
`params`:

```json
{
  "response": "42",
  "answer": "42",
  "params": {
    "root": "/path/to/evaluator-package",
    "entrypoint": "evaluator:evaluation_function",
    "difficulty": "easy"
  }
}
```

`root`, `_lf_root`, `entrypoint`, and `_lf_entrypoint` are consumed by the worker
and are not forwarded to the evaluator. Use other keys for exercise-specific
options.

## State and isolation

Do not rely on module-level mutable state for correctness.

Different runtime paths have different lifecycle behavior:

- `file` may run evaluators in fresh worker processes and uses best-effort
  hygiene: temporary cwd, restored environment, restored `sys.path`, captured
  stdout/stderr, and cleanup of modules imported from the evaluator root.
- `python-reactor` and `generic-wasm` use WASM execution and can restore
  snapshots between requests.
- `snapshot=off` benchmark rows are no-isolation controls only; they are useful
  for measuring overhead, not for normal evaluator semantics.

Treat each request as independent. Put persistent data in explicit files,
databases, or request parameters only when the deployment intentionally provides
that storage.

## Common errors

| Symptom | Likely cause | Fix |
|---|---|---|
| `missing evaluator params: entrypoint, root` | The worker does not know where the evaluator lives. | Set `FUNCTION_LF_ROOT` and `FUNCTION_LF_ENTRYPOINT`, or pass `root` / `entrypoint` in request `params`. |
| `entrypoint must be 'module:function'` | Entrypoint is missing `:` or function name. | Use a value like `evaluator:evaluation_function`. |
| `entrypoint function ... not found` | Module imported, but the function name is wrong. | Check spelling and ensure the function is defined at module top level. |
| `ImportError` / `ModuleNotFoundError` | Package root or dependencies are wrong for the selected runtime. | Test locally with `file`; then ensure reactor/Pyodide dependencies are available. |
| Preview receives the wrong arguments | Preview function signature is ambiguous. | Prefer `preview_function(response, answer=None, params=None)` or `preview_function(response, params=None)`. |
| Schema validation fails for eval | Return object lacks `is_correct` or contains non-JSON values. | Return a JSON-compatible dict with boolean `is_correct`. |
| Schema validation fails for preview | Return object lacks `preview`. | Return `{"preview": ...}`. |

## Local smoke test

Run the minimal example:

```bash
go run . \
  --log-level error \
  --interface file \
  --command python3 \
  --arg examples/lambda-feedback-adapter/lf_file_worker.py \
  --env FUNCTION_LF_ROOT=$PWD/examples/evaluator-python-minimal \
  --env FUNCTION_LF_ENTRYPOINT=evaluator:evaluation_function \
  --env FUNCTION_LF_PREVIEW_ENTRYPOINT=evaluator:preview_function \
  serve \
  --host 127.0.0.1 \
  --port 8080
```

Then:

```bash
curl -fsS -H 'content-type: application/json' \
  -d @examples/evaluator-python-minimal/test_request_eval.json \
  http://127.0.0.1:8080/

curl -fsS -H 'content-type: application/json' -H 'command: preview' \
  -d @examples/evaluator-python-minimal/test_request_preview.json \
  http://127.0.0.1:8080/
```

Expected result shapes:

```json
{"command":"eval","result":{"feedback":"Correct.","is_correct":true}}
{"command":"preview","result":{"preview":"Preview: draft answer"}}
```
