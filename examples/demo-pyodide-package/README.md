# Pyodide package-shaped evaluator example

This example mirrors the package layout used by Lambda Feedback-style Python
evaluators:

```text
evaluation_function/
  __init__.py
  evaluation.py   # evaluation_function(response, answer, params)
  preview.py      # preview_function(response, params)
```

It runs through the `pyodide` compatibility backend, not the generic wazero
WASM backend:

```bash
cd examples/demo-pyodide-python
npm install
cd ../..

FUNCTION_INTERFACE=pyodide \
FUNCTION_PYODIDE_RUNNER=examples/demo-pyodide-python/runner.js \
FUNCTION_PYODIDE_ROOT=examples/demo-pyodide-package \
FUNCTION_PYODIDE_EVAL_ENTRYPOINT=evaluation_function.evaluation:evaluation_function \
FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT=evaluation_function.preview:preview_function \
FUNCTION_PYODIDE_ADAPTER=examples/lambda-feedback-adapter/lf_compat_adapter.py \
go run . serve
```

The test/demo asserts that each request runs in a fresh package namespace so
module globals such as `invocation_count` do not leak between requests.
