# lf-bundle-python

Bundle a Lambda Feedback package-style Python evaluator into one standalone
Python script that exports the reactor-compatible functions:

```python
def evaluation_function(response, answer, params=None): ...
def preview_function(response, answer=None, params=None): ...
```

The generated file embeds:

- evaluator package modules from `--root`
- extra pure-Python modules from repeatable `--include-root` directories
- `lf_compat_adapter.py`
- the minimal `lf_toolkit/` shim from `--adapter-root`

It can also prepend repeatable `--sys-path` entries at runtime. Use this for
pure-Python dependency ZIPs or directories that are mounted into the WASI guest,
so dependency updates do not require regenerating one very large script.

It uses an in-memory `sys.meta_path` loader rather than writing modules to a temp
directory, so the bundle survives reactor's `exec(script_src, ns)` model and does
not need the original package root mounted at request time.

## Example

```bash
python3 tools/lf-bundle-python/lf_bundle_python.py \
  --root examples/lambda-feedback-fixtures/boilerplate-python \
  --adapter-root examples/lambda-feedback-adapter \
  --eval-entrypoint evaluation_function.evaluation:evaluation_function \
  --preview-entrypoint evaluation_function.preview:preview_function \
  --out /tmp/boilerplate.bundle.py
```

Use the output with reactor-python as the normal single-file script:

```bash
FUNCTION_INTERFACE=reactor-python \
FUNCTION_COMMAND=/path/to/python-reactor.wasm \
FUNCTION_WASM_PYTHON_SCRIPT=/tmp/boilerplate.bundle.py \
./shimmy serve
```

Shimmy can also run this packaging step automatically during reactor-python
startup for Lambda Feedback package-style evaluators:

```bash
FUNCTION_INTERFACE=reactor-python \
FUNCTION_WASM_MODULE=/path/to/python-reactor.wasm \
FUNCTION_LF_ROOT=examples/lambda-feedback-fixtures/boilerplate-python \
FUNCTION_LF_EVAL_ENTRYPOINT=evaluation_function.evaluation:evaluation_function \
FUNCTION_LF_PREVIEW_ENTRYPOINT=evaluation_function.preview:preview_function \
FUNCTION_LF_ADAPTER_ROOT=examples/lambda-feedback-adapter \
FUNCTION_LF_BUNDLER=tools/lf-bundle-python/lf_bundle_python.py \
./shimmy serve
```

In this mode Shimmy invokes the bundler once at process startup, writes a wrapper
script to a temporary file (or `FUNCTION_LF_BUNDLE_OUT` if set), then executes
that generated script through reactor-python. Bundling is not done per request.

## Scope

This is a packaging helper for evaluator packages, bundled pure-Python
dependencies, and packages whose native/WASI pieces are already present in
`python-reactor.wasm`.

It can embed pure-Python third-party packages after installing them into a staging
folder:

```bash
uv pip install --target /tmp/lf-puredeps mpmath typing_extensions antlr4-python3-runtime==4.7.2
uv pip install --target /tmp/lf-puredeps --no-deps 'git+https://github.com/lambda-feedback/latex2sympy.git@master#egg=latex2sympy2'
python3 tools/lf-bundle-python/lf_bundle_python.py \
  --root examples/lambda-feedback-fixtures/compare-boolean \
  --adapter-root examples/lambda-feedback-adapter \
  --include-root /tmp/lf-puredeps \
  --include-root tools/lf-bundle-python/polyfills/reactor \
  --eval-entrypoint evaluation_function.evaluation:evaluation_function \
  --preview-entrypoint evaluation_function.preview:preview_function \
  --out /tmp/compare-boolean.bundle.py
```

For larger pure-Python dependency sets, prefer a ZIP or directory payload on
`sys.path`:

```bash
(cd /tmp/lf-puredeps && python3 -m zipfile -c /tmp/lf-puredeps.zip .)
python3 tools/lf-bundle-python/lf_bundle_python.py \
  --root examples/lambda-feedback-fixtures/compare-boolean \
  --adapter-root examples/lambda-feedback-adapter \
  --sys-path /opt/lf-puredeps.zip \
  --eval-entrypoint evaluation_function.evaluation:evaluation_function \
  --preview-entrypoint evaluation_function.preview:preview_function \
  --out /tmp/compare-boolean.wrapper.py
```

When running under WASI, the `--sys-path` value must be visible inside the guest
at the same path, for example via a read-only preopened mount or Lambda layer.
Build-time bytecode generation for ZIP payloads is preferred when practical.

The same knobs are available through Shimmy's startup integration:

- `FUNCTION_LF_INCLUDE_ROOTS=/opt/lf-puredeps,/opt/shimmy/polyfills/reactor`
- `FUNCTION_LF_SYS_PATH=/opt/lf-puredeps.zip`
- `FUNCTION_LF_BUNDLE_OUT=/tmp/evaluator.wrapper.py`
- `FUNCTION_LF_BUNDLE_PYTHON=python3`

It does **not** compile native extension packages. Those must already be available
in the target backend:

- reactor-python: built into the reactor artifact / WASI VFS, e.g. NumPy in `v1.0.11`
- Pyodide: loaded through `FUNCTION_PYODIDE_PACKAGES`

`scipy` is intentionally out of reactor scope; use Pyodide for scipy-heavy evaluators.

For old pure-Python dependencies, the bundler applies a narrow source rewrite for
Python 3.14 compatibility (`from typing.io import TextIO` -> `from typing import
TextIO`). This is needed by the Lambda Feedback `latex2sympy2`/ANTLR stack and
keeps the workaround in the generated bundle rather than in reactor C.

## Tests

```bash
python3 -m pytest tools/lf-bundle-python -q
scripts/demo-reactor-lambda-feedback-bundles.sh docker
```
