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

No `requirements.txt` files were copied because these source repos do not include one at
runtime root (they use Poetry metadata instead).

## Notes
- Network-dependent and/or non-runtime artifacts were intentionally omitted: `.git/`, CI workflow files,
  Dockerfiles, tests, and unrelated docs.
- Numeric/scientific fixtures (e.g. `IsSimilar`/`ArrayEqual`) were intentionally left out in this task slice.
  Add them in a later task only if the small footprint and dependency footprint are acceptable.
