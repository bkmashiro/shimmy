# Lambda Feedback evaluator capability matrix

This page records which real Lambda Feedback Python evaluator shapes are covered
by the current Pyodide package path and the reactor single-file bundle shortcut.
The machine-readable source of truth is
`examples/lambda-feedback-fixtures/capability-matrix.json`; the reactor smoke
coverage is in `internal/execution/wasm/python_reactor_lf_bundle_test.go`.

## Status labels

| Status | Meaning |
|---|---|
| `reactor-bundle` | Package evaluator can be converted into one reactor script with no extra dependencies. |
| `reactor-bundle-pure-deps` | Works on reactor when pure-Python dependencies are installed into a staging directory and passed to `lf-bundle-python --include-root`. |
| `reactor-bundle-artifact-native` | Works on reactor because the required native/WASI dependency is already inside the selected `python-reactor.wasm` artifact. |
| `pyodide-only` | Keep this on Pyodide package mode; do not attempt reactor unless the dependency story changes. |
| `out-of-scope` | Not currently worth targeting in either WASM Python path. |

## Current matrix

| Evaluator / fixture | Source | Reactor status | Why | Pyodide status |
|---|---|---|---|---|
| `boilerplate-python` | `.demo-lambda-sources/evaluation-function-boilerplate-python` | `reactor-bundle` ✅ | package layout + `lf_toolkit` shim only | supported ✅ |
| `compare-boolean` | `.demo-lambda-sources/compareBoolean` | `reactor-bundle-pure-deps` ✅ | SymPy from reactor artifact, plus bundled `mpmath` and narrow `ctypes` polyfill | supported ✅ |
| `array-equal` | `.demo-lambda-sources/ArrayEqual/app` | `reactor-bundle-artifact-native` ✅ | NumPy is present in `python-reactor.wasm` v1.0.11 | supported ✅ |
| `is-similar` | `.demo-lambda-sources/IsSimilar/app` | `reactor-bundle-artifact-native` ✅ | NumPy scalar helpers are present in the reactor artifact | supported ✅ |
| `symbolic-equal` | `.demo-lambda-sources/SymbolicEqual/app` | `reactor-bundle-pure-deps` ✅ | SymPy is in artifact; `mpmath`, `typing_extensions`, `antlr4-python3-runtime`, and `latex2sympy2` are pure Python and bundle cleanly after a narrow `typing.io` rewrite | supported ✅ |
| `short-text-answer` | `.demo-lambda-sources/shortTextAnswer/app` | `pyodide-only` ⚠️ | `nltk` data/filesystem setup, `gensim`/SciPy dependency chain, and `matplotlib` stack are not reactor targets | default path ✅ |

## Policy

1. **Do not auto-route by imports.** The deployer still chooses
   `FUNCTION_INTERFACE` explicitly.
2. **Try reactor bundle only for compatible dependency sets:** evaluator code,
   `lf_toolkit` shim, and pure-Python dependencies can be embedded into one
   script. Native/WASI packages must already be in the reactor artifact.
3. **Do not port big packages into reactor for one evaluator.** Small shims are
   acceptable (`ctypes` stub, old `typing.io` rewrite, `numpy.random` safety
   polyfill). Heavy packages such as SciPy, pandas, sklearn, nltk/gensim stacks,
   or matplotlib remain Pyodide-only unless they become first-class product
   requirements.
4. **Use latest artifact for reactor claims.** The verified artifact is
   `bkmashiro/webassembly-language-runtimes v1.0.11`:
   `sha256 c3e2de93090544a47b1b63fa5f97060cfcd4bd87d4dff96d79a9c5bfa94589a3`.

## Verification commands

```bash
python3 -m pytest examples/lambda-feedback-fixtures tools/lf-bundle-python examples/lambda-feedback-adapter -q
scripts/demo-reactor-lambda-feedback-bundles.sh docker
```

The Docker demo prepares the pure-Python staging directory with:

```bash
uv pip install --target /tmp/shimmy-reactor-artifacts/pure-python-deps \
  typing_extensions mpmath==1.2.1 antlr4-python3-runtime==4.7.2
uv pip install --target /tmp/shimmy-reactor-artifacts/pure-python-deps --no-deps \
  'git+https://github.com/lambda-feedback/latex2sympy.git@master#egg=latex2sympy2'
```
