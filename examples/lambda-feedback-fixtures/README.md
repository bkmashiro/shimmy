# Lambda Feedback fixture snapshots

This directory contains a small, local subset of real Lambda Feedback evaluator repositories,
copied from `.demo-lambda-sources` so tests can run without network access.

## Capability matrix

The machine-readable matrix lives in [`capability-matrix.json`](./capability-matrix.json).
It separates three reactor-success categories from explicit Pyodide-only rows:

| Fixture | Status | Reactor requirements | Pyodide stance |
|---|---|---|---|
| `boilerplate-python` | `reactor-bundle` | evaluator package + adapter bundle only | supported |
| `compare-boolean` | `reactor-bundle-pure-deps` | `sympy` in `python-reactor.wasm`; bundle `mpmath` + reactor `ctypes` polyfill | supported |
| `array-equal` | `reactor-bundle-artifact-native` | `numpy` from `python-reactor.wasm` | supported |
| `is-similar` | `reactor-bundle-artifact-native` | `numpy` from `python-reactor.wasm` | supported |
| `symbolic-equal` | `reactor-bundle-pure-deps` | `sympy` in artifact; bundle `mpmath`, `typing_extensions`, `antlr4-python3-runtime`, `latex2sympy2`; apply old `typing.io` rewrite | supported |
| `short-text-answer` | `pyodide-only` | not targeted for reactor: `nltk` data, `gensim`/SciPy chain, `matplotlib` stack | default compatibility path |

Rules of thumb:
- Pure Python evaluator code and dependencies can be bundled with `tools/lf-bundle-python --include-root`.
- Native/WASI dependencies must already be present in the chosen `python-reactor.wasm` artifact.
- Heavy scientific/data/rendering stacks remain Pyodide-only unless they become first-class reactor artifact requirements.

## Fixtures

### `boilerplate-python`
- **Source repo/folder:** `.demo-lambda-sources/evaluation-function-boilerplate-python`
- **Contains (runtime files only):** `evaluation_function/main.py`, `evaluation_function/__init__.py`,
  `evaluation_function/evaluation.py`, `evaluation_function/preview.py`
- **Compatibility behavior covered:**
  - Minimal package-style evaluator layout.
  - Uses `lf_toolkit.create_server(...)` registration with `server.eval(...)` and `server.preview(...)`.
  - Direct `lf_toolkit`-style result objects (`Result`, `Params`).

### `compare-boolean`
- **Source repo/folder:** `.demo-lambda-sources/compareBoolean`
- **Contains (runtime files only):** `evaluation_function/main.py`, `evaluation_function/__init__.py`,
  `evaluation_function/evaluation.py`, `evaluation_function/preview.py`, `evaluation_function/parse.py`,
  `evaluation_function/lex.py`, `evaluation_function/ast.py`
- **Compatibility behavior covered:**
  - Relative imports inside evaluator packages (e.g. `from .parse import ...`).
  - Rich parser/AST helper modules needed at runtime.
  - Preview/eval flow with real parse-side error handling (`FeedbackException`).
  - Reactor bundle path with bundled pure-Python `mpmath` plus a narrow `ctypes` polyfill for SymPy's optional gmpy detection.

### `array-equal`
- **Source repo/folder:** `.demo-lambda-sources/ArrayEqual/app`
- **Contains (runtime files only):** `evaluation_function/evaluation.py`, `evaluation_function/main.py`,
  `evaluation_function_utils/errors.py`
- **Compatibility behavior covered:**
  - NumPy array construction and `np.allclose(...)` under the latest reactor artifact.
  - Small compatibility shim for `evaluation_function_utils.errors.EvaluationException`.

### `is-similar`
- **Source repo/folder:** `.demo-lambda-sources/IsSimilar/app`
- **Contains (runtime files only):** `evaluation_function/evaluation.py`, `evaluation_function/main.py`
- **Compatibility behavior covered:**
  - NumPy scalar helper import (`from numpy import spacing`).
  - Numeric tolerance feedback fields returned through reactor bundle normalization.

### `symbolic-equal`
- **Source repo/folder:** `.demo-lambda-sources/SymbolicEqual/app`
- **Contains (runtime files only):** `evaluation_function/evaluation.py`, `evaluation_function/preview.py`,
  parser helpers, feedback messages, and minimal `main.py` registration.
- **Compatibility behavior covered:**
  - Larger real evaluator package copied from a flat `app/` layout into `evaluation_function/`.
  - Basic symbolic comparison via SymPy.
  - LaTeX preview via pure-Python `latex2sympy2` + `antlr4-python3-runtime` bundled through `--include-root`.
  - Bundler compatibility rewrite for old `antlr4-python3-runtime` imports (`typing.io` -> `typing`).

No `requirements.txt` files were copied for package-style fixtures because the runtime test matrix
handles third-party deps separately: built-in reactor packages stay in `python-reactor.wasm`, while
extra pure-Python deps are supplied to the bundler via `--include-root`.

## Notes
- Network-dependent and/or non-runtime artifacts were intentionally omitted: `.git/`, CI workflow files,
  Dockerfiles, tests, and unrelated docs.
- `scipy` and very heavy/data-bearing dependencies remain out of reactor scope; use Pyodide for those.
