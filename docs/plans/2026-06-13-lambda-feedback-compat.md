# Lambda Feedback Evaluator Compatibility Implementation Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Replace toy-only Python examples with a compatibility path proven against real Lambda Feedback evaluator repositories.

**Architecture:** Keep evaluator author APIs unchanged (`evaluation_function`, `preview_function`, and `lf_toolkit` registration). Add a thin Python compatibility adapter that can load package-style evaluators, invoke eval/preview with signature compatibility, normalize toolkit return objects, and run under the explicit backend selected by deployment (`pyodide` by default, `reactor-python` as an opt-in fast path). Do not infer runtime from imports or requirements.

**Tech Stack:** Go Shimmy dispatchers, Node/Pyodide runner, CPython-WASI reactor runner, Python adapter code, fixture repositories under `.demo-lambda-sources` or `examples/lambda-feedback-fixtures`, shell demos, Go/Python/Node smoke tests.

---

## Scope

The existing examples are still useful as backend smoke tests:

- `examples/eval-python`: minimal reactor/python smoke.
- `examples/eval-numpy`: CPython-WASI + NumPy smoke.
- `examples/eval-scipy`: Pyodide package smoke.
- `examples/eval-js`: Javy/QuickJS smoke.

They are not sufficient as Lambda Feedback compatibility tests because real repos use package layout, `lf_toolkit`, relative imports, preview signature variants, helper modules, requirements, and non-dict return objects.

This plan adds real-sample compatibility without removing the low-level backend smoke tests.

---

## Phase 0: Choose representative real evaluators

Use 4 tiers of fixtures:

1. **Tiny toolkit baseline:** `evaluation-function-boilerplate-python`
   - package layout;
   - `main.py` uses `create_server()`, `server.eval(...)`, `server.preview(...)`;
   - returns `lf_toolkit.evaluation.Result`.
2. **Relative imports + preview signature variant:** `compareBoolean` or `compareSets`
   - `from .parse import ...`;
   - `preview_function(response, params)`;
   - returns `lf_toolkit.preview.Result(preview=Preview(...))`.
3. **Scientific light:** `IsSimilar` or `ArrayEqual`
   - NumPy and helper package dependencies;
   - validates Pyodide default and reactor opt-in boundary.
4. **Heavy / expected limitation:** `shortTextAnswer`
   - NLTK/gensim/model/data files;
   - likely not first green target, but useful as a documented unsupported/needs-packaging case.

Acceptance criterion: at least tiers 1 and 2 run end-to-end under the new adapter; tier 3 either runs under Pyodide or produces a clear packaging error; tier 4 is documented as a later asset/package milestone.

---

## Task 1: Freeze real evaluator fixtures

**Objective:** Put a small, reproducible subset of real Lambda Feedback evaluator repos under a local fixture directory without depending on network at test time.

**Files:**
- Create: `examples/lambda-feedback-fixtures/README.md`
- Create/copy: `examples/lambda-feedback-fixtures/boilerplate-python/...`
- Create/copy: `examples/lambda-feedback-fixtures/compare-boolean/...`
- Optional later: `examples/lambda-feedback-fixtures/is-similar/...`

**Steps:**

1. Copy from `.demo-lambda-sources` into `examples/lambda-feedback-fixtures` only the files needed for runtime tests:
   - `evaluation_function/main.py`
   - `evaluation_function/evaluation.py`
   - `evaluation_function/preview.py`
   - helper modules such as `parse.py`
   - `requirements.txt`
   - minimal test input JSON files
2. Strip `.git`, CI, Dockerfiles, large artifacts, and unrelated tests.
3. Add `README.md` with source repo names and what each fixture covers.
4. Run: `git diff --check`.

**Expected result:** fixtures are small and deterministic.

---

## Task 2: Add a Python compatibility adapter module

**Objective:** Implement a backend-independent adapter for loading Lambda Feedback package-style evaluators.

**Files:**
- Create: `examples/lambda-feedback-adapter/lf_compat_adapter.py`
- Create: `examples/lambda-feedback-adapter/README.md`
- Test: `examples/lambda-feedback-adapter/test_lf_compat_adapter.py`

**Core API:**

```python
def load_entrypoint(spec: str):
    # spec: "evaluation_function.evaluation:evaluation_function"
    ...


def call_function(fn, method: str, response, answer=None, params=None):
    # preview supports both (response, params) and (response, answer, params)
    ...


def normalize_result(value):
    # dict, model_dump(), dict(), dataclass, attrs-like object, public fields
    ...
```

**Tests:**

1. `normalize_result` returns dict unchanged.
2. `normalize_result` handles object with `model_dump()`.
3. `normalize_result` handles object with `dict()`.
4. `call_function` calls preview two-arg function as `(response, params)`.
5. `call_function` calls eval as `(response, answer, params)`.
6. `load_entrypoint` loads `evaluation_function.evaluation:evaluation_function` from a fixture package.

**Commands:**

```bash
python3 -m pytest examples/lambda-feedback-adapter/test_lf_compat_adapter.py -q
```

Expected: tests pass locally with normal CPython.

---

## Task 3: Provide a minimal `lf_toolkit` shim for compatibility tests

**Objective:** Let real fixtures import `lf_toolkit` without installing the full production toolkit in Pyodide/reactor smoke tests.

**Files:**
- Create: `examples/lambda-feedback-adapter/lf_toolkit/__init__.py`
- Create: `examples/lambda-feedback-adapter/lf_toolkit/evaluation.py`
- Create: `examples/lambda-feedback-adapter/lf_toolkit/preview.py`
- Create: `examples/lambda-feedback-adapter/lf_toolkit/shared/params.py`

**Implementation shape:**

```python
class Result:
    def __init__(self, **kwargs):
        self.__dict__.update(kwargs)

    def dict(self):
        return dict(self.__dict__)
```

For `create_server()` tests, implement only a tiny registry:

```python
class Server:
    def eval(self, fn): self.eval_fn = fn
    def preview(self, fn): self.preview_fn = fn
```

`run(server)` can raise or no-op in adapter extraction tests. Do not pretend this is the full toolkit.

**Acceptance:** `compareBoolean` / boilerplate modules import successfully in ordinary Python when `PYTHONPATH` includes adapter + fixture root.

---

## Task 4: Add a CLI runner around the adapter

**Objective:** Create one local command that invokes a real fixture exactly like Shimmy would.

**Files:**
- Create: `examples/lambda-feedback-adapter/run_lf_eval.py`

**CLI:**

```bash
python3 examples/lambda-feedback-adapter/run_lf_eval.py \
  --root examples/lambda-feedback-fixtures/boilerplate-python \
  --eval-entrypoint evaluation_function.evaluation:evaluation_function \
  --preview-entrypoint evaluation_function.preview:preview_function \
  --method eval \
  --input '{"response":"3.14","answer":"3.14159","params":{"tolerance":0.01}}'
```

**Output:** JSON only on stdout.

**Acceptance:** local CPython smoke passes for boilerplate and compareBoolean.

---

## Task 5: Integrate adapter into Pyodide runner

**Objective:** Make `examples/eval-pyodide/runner.js` support package-style entrypoints, not only `FUNCTION_PYODIDE_SCRIPT` single-file exec.

**Files:**
- Modify: `examples/eval-pyodide/runner.js`
- Modify: `examples/eval-pyodide/README.md`
- Test/script: `scripts/demo-lambda-feedback-fixtures.sh`

**New env vars:**

```bash
FUNCTION_PYODIDE_ROOT=/opt/evaluator
FUNCTION_PYODIDE_EVAL_ENTRYPOINT=evaluation_function.evaluation:evaluation_function
FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT=evaluation_function.preview:preview_function
FUNCTION_PYODIDE_ADAPTER=/opt/shimmy/examples/lambda-feedback-adapter/lf_compat_adapter.py
```

Keep `FUNCTION_PYODIDE_SCRIPT` as legacy/simple mode.

**Acceptance:**

```bash
scripts/demo-lambda-feedback-fixtures.sh pyodide-boilerplate
scripts/demo-lambda-feedback-fixtures.sh pyodide-compare-boolean
```

Both return valid JSON and no traceback.

---

## Task 6: Integrate adapter into reactor-python path, if feasible

**Objective:** Decide whether reactor can support the same adapter immediately or needs a separate packaging step.

**Files:**
- Inspect/modify: `build/python-reactor/py_reactor.c`
- Inspect/modify: `internal/execution/wasm/python_reactor.go`
- Test: `internal/execution/wasm/python_reactor_test.go`

**Decision gate:**

- If reactor currently only accepts `FUNCTION_WASM_PYTHON_SCRIPT`, add entrypoint/root env support only if mounted source/module loading is already possible.
- If not, document that reactor remains single-file/known-compatible until module packaging is implemented.

**Acceptance:** Either:

1. reactor runs boilerplate fixture package; or
2. reactor fails with an explicit “package entrypoints are not supported by reactor-python yet” error and docs explain Pyodide is the default compatibility path.

---

## Task 7: Add end-to-end fixture demo script

**Objective:** One command proves the compatibility story.

**Files:**
- Create: `scripts/demo-lambda-feedback-fixtures.sh`

**Modes:**

```bash
scripts/demo-lambda-feedback-fixtures.sh list
scripts/demo-lambda-feedback-fixtures.sh local-boilerplate
scripts/demo-lambda-feedback-fixtures.sh local-compare-boolean
scripts/demo-lambda-feedback-fixtures.sh pyodide-boilerplate
scripts/demo-lambda-feedback-fixtures.sh pyodide-compare-boolean
scripts/demo-lambda-feedback-fixtures.sh all
```

**Acceptance:** `all` runs the green targets and prints a compact summary.

---

## Task 8: Document compatibility status

**Objective:** Update docs so examples are not misleading.

**Files:**
- Modify: `docs/python-examples.md`
- Modify: `docs/python-backends.md`
- Modify: `docs/writing-eval-functions.md`
- Modify: `README.md` or `WASM.md` if they still mention toy-only flow

**Content:**

- Existing examples are backend smoke tests.
- Real Lambda Feedback compatibility is tested with fixtures.
- `lf_toolkit` compatibility is via adapter/shim; full toolkit support is a milestone.
- Runtime selection is explicit.
- Python default compatibility path is Pyodide.
- Reactor is fast path after dependencies are confirmed.

**Acceptance:** Docs link to the demo command and list tested fixtures.

---

## Task 9: Verification gate

Run:

```bash
git diff --check
python3 -m pytest examples/lambda-feedback-adapter/test_lf_compat_adapter.py -q
bash -n scripts/demo-lambda-feedback-fixtures.sh
scripts/demo-lambda-feedback-fixtures.sh all
bash -n scripts/demo-python-examples.sh
scripts/demo-python-examples.sh
```

If docs are mirrored to `shimmy-docs`, also run there:

```bash
cd /Users/yuzhe/projects/shimmy-docs
npm run build
```

Expected: all commands pass, except known VitePress SSR `window is not defined` warning is acceptable if exit code is 0.

---

## Suggested implementation order

1. Fixtures only.
2. Adapter tests under normal CPython.
3. Minimal `lf_toolkit` shim.
4. CLI runner.
5. Pyodide integration.
6. Demo script.
7. Reactor decision gate.
8. Docs.
9. Final verification + signed commit.

This keeps the first useful milestone small: “two real Lambda Feedback package evaluators run through adapter locally”. Pyodide and reactor integration come after the adapter behavior is locked by tests.
