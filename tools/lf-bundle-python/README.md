# lf-bundle-python

Bundle a Lambda Feedback package-style evaluator into one standalone Python
script that exports the Shimmy evaluator functions:

```python
def evaluation_function(response, answer, params=None): ...
def preview_function(response, answer=None, params=None): ...
```

The generated file embeds:

- evaluator package modules from `--root`;
- extra pure-Python modules from repeatable `--include-root` directories;
- `lf_compat_adapter.py`;
- the minimal `lf_toolkit/` shim from `--adapter-root`.

It uses an in-memory `sys.meta_path` loader, so the package root is not required
inside the guest at request time. The Shimmy Python profile executes the generated
script through `runtime_prepare` in each fresh instance.

## Build a bundle

```bash
python3 tools/lf-bundle-python/lf_bundle_python.py \
  --root examples/lambda-feedback-fixtures/boilerplate-python \
  --adapter-root examples/lambda-feedback-adapter \
  --eval-entrypoint evaluation_function.evaluation:evaluation_function \
  --preview-entrypoint evaluation_function.preview:preview_function \
  --out /tmp/boilerplate.bundle.py
```

Run it with the pinned Shimmy Python Runtime bundle:

```bash
FUNCTION_INTERFACE=wasm \
FUNCTION_WASM_PROFILE=shimmy-python \
FUNCTION_WASM_MODULE=dist/shimmy-python/numpy-core/shimmy-python-runtime-numpy-core.wasm \
FUNCTION_WASM_MANIFEST=dist/shimmy-python/numpy-core/manifest.json \
FUNCTION_WASM_PYTHON_SCRIPT=/tmp/boilerplate.bundle.py \
./shimmy serve
```

## Automatic startup bundling

Shimmy can invoke the bundler once at process startup:

```bash
FUNCTION_INTERFACE=wasm \
FUNCTION_WASM_PROFILE=shimmy-python \
FUNCTION_WASM_MODULE=dist/shimmy-python/numpy-core/shimmy-python-runtime-numpy-core.wasm \
FUNCTION_WASM_MANIFEST=dist/shimmy-python/numpy-core/manifest.json \
FUNCTION_LF_ROOT=examples/lambda-feedback-fixtures/boilerplate-python \
./shimmy serve
```

Defaults:

- eval: `evaluation_function.evaluation:evaluation_function`;
- preview: `evaluation_function.preview:preview_function` when
  `evaluation_function/preview.py` exists;
- adapter: `examples/lambda-feedback-adapter`;
- bundler: `tools/lf-bundle-python/lf_bundle_python.py`;
- output: a temporary bundle file.

Override only the values that differ:

```bash
FUNCTION_LF_EVAL_ENTRYPOINT=package.eval:evaluate
FUNCTION_LF_PREVIEW_ENTRYPOINT=package.preview:preview
FUNCTION_LF_ADAPTER_ROOT=/opt/lambda-feedback-adapter
FUNCTION_LF_BUNDLER=/opt/lf-bundle-python.py
FUNCTION_LF_BUNDLE_OUT=/tmp/evaluator.bundle.py
```

The same values can be stored in `FUNCTION_LF_CONFIG`:

```json
{
  "root": "/var/task",
  "eval": "package.eval:evaluate",
  "preview": "package.preview:preview",
  "include_roots": ["/opt/lf-puredeps"]
}
```

Explicit `FUNCTION_LF_*` environment variables override the configuration file.

## Dependencies and filesystem boundary

Install pure-Python dependencies into a Host staging directory and embed them at
startup:

```bash
uv pip install --target /tmp/lf-puredeps \
  mpmath typing_extensions antlr4-python3-runtime==4.7.2
python3 tools/lf-bundle-python/lf_bundle_python.py \
  --root examples/lambda-feedback-fixtures/compare-boolean \
  --adapter-root examples/lambda-feedback-adapter \
  --include-root /tmp/lf-puredeps \
  --eval-entrypoint evaluation_function.evaluation:evaluation_function \
  --preview-entrypoint evaluation_function.preview:preview_function \
  --out /tmp/compare-boolean.bundle.py
```

The Shimmy Python profile exposes no Host filesystem paths to the guest. Shimmy
therefore rejects `FUNCTION_LF_SYS_PATH` and `sys_path` in `FUNCTION_LF_CONFIG`.
Use `FUNCTION_LF_INCLUDE_ROOTS` so dependencies are embedded in the generated
script before sandbox startup.

The bundler does not compile native extensions. NumPy core is already packaged
in the pinned runtime. SciPy remains a Pyodide route.

For old pure-Python dependencies, the bundler applies one narrow Python 3.14
source rewrite: `from typing.io import TextIO` becomes `from typing import
TextIO`. This keeps the compatibility fix in generated source rather than in the
runtime.

## Tests

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest \
  tools/lf-bundle-python/test_lf_bundle_python.py -v
scripts/smoke-python-reactor-handoff.sh direct
```
