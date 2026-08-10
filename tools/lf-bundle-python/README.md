# lf-bundle-python

`lf-bundle-python` converts an evaluator-owned Python package into the single
trusted script consumed by the Python Reactor Host profile:

```python
def dispatch(method: str, payload: dict) -> dict: ...
def evaluation_function(response, answer, params): ...
def preview_function(response, params): ...  # when configured
```

It is a deployment-time tool. Shimmy does not invoke it, inspect package
structure, install dependencies, or read `FUNCTION_LF_*` settings at runtime.

## Package an evaluator

```bash
python3 tools/lf-bundle-python/lf_bundle_python.py \
  --root /src/evaluation-function \
  --adapter-root tools/lf-bundle-python/adapter \
  --runtime-manifest /opt/runtime/manifest.json \
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

Modules supplied by the selected Reactor artifact come from its bound manifest:

```bash
python3 tools/lf-bundle-python/lf_bundle_python.py \
  ... \
  --runtime-manifest /opt/runtime/numpy-core/manifest.json
```

The manifest's profile and `python_modules` must agree with the frozen Runtime
contract (`base`, `numpy-core`, or `sympy`). This is not an installer. Native
CPython wheels are not accepted as WASI dependencies merely because they appear
in an evaluator's `requirements.txt`.

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
SHIMMY_PYTHON_REACTOR_BASE_WASM=/opt/runtime/base/runtime.wasm \
SHIMMY_PYTHON_REACTOR_BASE_MANIFEST=/opt/runtime/base/manifest.json \
SHIMMY_PYTHON_REACTOR_NUMPY_WASM=/opt/runtime/numpy-core/runtime.wasm \
SHIMMY_PYTHON_REACTOR_NUMPY_MANIFEST=/opt/runtime/numpy-core/manifest.json \
SHIMMY_PYTHON_REACTOR_SYMPY_WASM=/opt/runtime/sympy/runtime.wasm \
SHIMMY_PYTHON_REACTOR_SYMPY_MANIFEST=/opt/runtime/sympy/manifest.json \
  scripts/e2e-python-reactor-lf-packages.sh
```

This starts real Shimmy HTTP servers for boilerplate, ArrayEqual, and
compareBoolean. It also verifies that the base profile rejects compareBoolean
before packaging while the SymPy profile executes it successfully.
