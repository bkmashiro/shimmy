# lf-bundle-python

`lf-bundle-python` converts an evaluator-owned Python package into the single
trusted script consumed by the Python Reactor Host profile:

```python
def dispatch(method: str, payload: dict) -> dict: ...
```

It is a deployment-time tool. Shimmy does not invoke it, inspect package
structure, install dependencies, or read `FUNCTION_LF_*` settings at runtime.

## Package an evaluator

```bash
python3 tools/lf-bundle-python/lf_bundle_python.py \
  --root /src/evaluation-function \
  --adapter-root tools/lf-bundle-python/adapter \
  --eval-entrypoint evaluation_function.evaluation:evaluation_function \
  --preview-entrypoint evaluation_function.preview:preview_function \
  --out /tmp/evaluator.bundle.py
```

The source checkout stays external to this repository. Bind it to an exact Git
commit in the surrounding build or test workflow.

Run the caller-produced bundle with:

```bash
FUNCTION_INTERFACE=wasm \
FUNCTION_WASM_PROFILE=python-reactor \
FUNCTION_WASM_MODULE=/opt/runtime/agent-python-runtime-numpy-core.wasm \
FUNCTION_WASM_MANIFEST=/opt/runtime/manifest.json \
FUNCTION_WASM_PYTHON_SCRIPT=/tmp/evaluator.bundle.py \
./shimmy serve
```

## Dependency contract

The bundler follows imports reachable from the configured entrypoints and fails
before writing output when a required third-party module is unresolved.

Pure-Python packages can be embedded from a prepared dependency root:

```bash
python3 tools/lf-bundle-python/lf_bundle_python.py \
  ... \
  --include-root /tmp/pure-python-dependencies
```

Modules supplied by the selected Reactor artifact must be declared explicitly:

```bash
python3 tools/lf-bundle-python/lf_bundle_python.py \
  ... \
  --runtime-module numpy
```

`--runtime-module` is a build assertion, not an installer. The Linux E2E must
still import and execute that module from the real artifact. Native CPython
wheels are not accepted as WASI dependencies merely because they appear in an
evaluator's `requirements.txt`.

The adapter provides the evaluator-facing `lf_toolkit` result/parameter surface
without starting the toolkit's process server. It normalizes toolkit objects,
dataclasses, Pydantic-style models, and scalar objects such as NumPy booleans to
JSON-compatible values.

## Tests

Synthetic package and dependency-gate tests:

```bash
python3 -m unittest tools/lf-bundle-python/test_lf_bundle_python.py -v
```

Real repositories remain external. On Linux, after checking them out at pinned
commits, run:

```bash
SHIMMY_LF_BOILERPLATE_ROOT=/src/evaluation-function-boilerplate-python \
SHIMMY_LF_BOILERPLATE_SHA=<commit> \
SHIMMY_LF_ARRAY_EQUAL_ROOT=/src/ArrayEqual \
SHIMMY_LF_ARRAY_EQUAL_SHA=<commit> \
SHIMMY_LF_COMPARE_BOOLEAN_ROOT=/src/compareBoolean \
SHIMMY_LF_COMPARE_BOOLEAN_SHA=<commit> \
SHIMMY_LF_EVALUATION_UTILS_WHEEL=/deps/evaluation_function_utils.whl \
SHIMMY_LF_EVALUATION_UTILS_SHA256=<sha256> \
SHIMMY_PYTHON_REACTOR_WASM=/opt/runtime/agent-python-runtime-numpy-core.wasm \
SHIMMY_PYTHON_REACTOR_MANIFEST=/opt/runtime/manifest.json \
  scripts/e2e-python-reactor-lf-packages.sh
```

This starts real Shimmy HTTP servers for the current boilerplate and ArrayEqual
packages. It also verifies that compareBoolean fails packaging with an explicit
missing-SymPy report rather than producing a broken bundle.
