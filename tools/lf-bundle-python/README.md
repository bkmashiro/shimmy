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

## Scope

This is a shortcut for evaluator packages, bundled pure-Python dependencies, and
packages whose native/WASI pieces are already present in `python-reactor.wasm`.

It can embed pure-Python third-party packages after installing them into a staging
folder:

```bash
uv pip install --target /tmp/lf-puredeps mpmath
python3 tools/lf-bundle-python/lf_bundle_python.py \
  --root examples/lambda-feedback-fixtures/compare-boolean \
  --adapter-root examples/lambda-feedback-adapter \
  --include-root /tmp/lf-puredeps \
  --include-root tools/lf-bundle-python/polyfills/reactor \
  --eval-entrypoint evaluation_function.evaluation:evaluation_function \
  --preview-entrypoint evaluation_function.preview:preview_function \
  --out /tmp/compare-boolean.bundle.py
```

It does **not** compile native extension packages. Those must already be available
in the target backend:

- reactor-python: built into the reactor artifact / WASI VFS, e.g. NumPy in `v1.0.11`
- Pyodide: loaded through `FUNCTION_PYODIDE_PACKAGES`

`scipy` is intentionally out of reactor scope; use Pyodide for scipy-heavy evaluators.

## Tests

```bash
python3 -m pytest tools/lf-bundle-python -q
scripts/demo-reactor-lambda-feedback-bundles.sh docker
```
