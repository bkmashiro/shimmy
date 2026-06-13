# Lambda Feedback fixture snapshots

This directory contains a small, local subset of real Lambda Feedback evaluator repositories,
copied from `.demo-lambda-sources` so tests can run without network access.

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

No `requirements.txt` files were copied for package-style fixtures because the runtime test matrix
handles third-party deps separately: built-in reactor packages stay in `python-reactor.wasm`, while
extra pure-Python deps are supplied to the bundler via `--include-root`.

## Notes
- Network-dependent and/or non-runtime artifacts were intentionally omitted: `.git/`, CI workflow files,
  Dockerfiles, tests, and unrelated docs.
- `scipy` and very heavy/data-bearing dependencies remain out of reactor scope; use Pyodide for those.
