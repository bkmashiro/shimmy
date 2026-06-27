# python-reactor package-shaped evaluator example

This example mirrors a Lambda Feedback-style Python evaluator package:

```text
evaluation_function/
  __init__.py
  evaluation.py   # evaluation_function(response, answer, params)
  preview.py      # preview_function(response, params)
```

It runs through the `FUNCTION_INTERFACE=wasm` backend with
`FUNCTION_WASM_PROFILE=python-reactor`. Unlike the Pyodide example, this path
loads a reactor-mode CPython WASM artifact directly in wazero and uses
full-memory snapshot/restore between requests.

The large `python-reactor.wasm` artifact is intentionally not committed to this
repository. Provide it at runtime:

```bash
PYTHON_REACTOR_WASM=/path/to/python-reactor.wasm \
  scripts/demo-python-reactor-package.sh
```

Or configure manually:

```bash
FUNCTION_INTERFACE=wasm \
FUNCTION_WASM_PROFILE=python-reactor \
FUNCTION_WASM_MODULE=/path/to/python-reactor.wasm \
FUNCTION_WASM_MAX_MEMORY_PAGES=8192 \
FUNCTION_LF_ROOT=$(pwd)/examples/demo-python-reactor-package \
FUNCTION_LF_EVAL_ENTRYPOINT=evaluation_function.evaluation:evaluation_function \
FUNCTION_LF_PREVIEW_ENTRYPOINT=evaluation_function.preview:preview_function \
shimmy serve
```

If `FUNCTION_WASM_PYTHON_SCRIPT` is not set, `FUNCTION_LF_ROOT` package mode
generates a tiny bundle script that imports the named entrypoints and mounts the
package root read-only into WASI. The demo asserts that module globals such as
`invocation_count` reset to `1` on every warm request.

This is an optional Linux/reference smoke. Without `PYTHON_REACTOR_WASM`, the
demo script exits successfully with a skip message so ordinary CI does not need
to download or store the large artifact.
