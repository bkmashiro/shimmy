# Multi-scenario Shimmy-WASM demos

There are now two demo entry points:

```bash
scripts/demo-wasm.sh       # shortest end-to-end story: state reset in one WASM evaluator
scripts/demo-scenarios.sh  # scenario matrix: multiple guest languages + sample inputs
```

## What `scripts/demo-scenarios.sh` runs

The scenario runner builds Shimmy once, then runs one local HTTP server per demo
module and sends a concrete grading request to it.

| Scenario | Guest language/runtime | Sample request | Expected result | Local status |
|---|---|---|---|---|
| `go-demo-stateful-snapshot` | Go → `wasm32-wasip1` | `response="42"`, `answer="42"` | correct; guest counter remains `1` | ✅ macOS/Linux |
| `go-eval-simple-equality` | Go → `wasm32-wasip1` | `response="hello"`, `answer="hello"` | correct | ✅ macOS/Linux |
| `rust-eval-simple-equality` | Rust → `wasm32-wasip1` | `response="rust"`, `answer="rust"` | correct | ✅ if Rust toolchain installed |
| `c-eval-simple-equality` | C → `wasm32-wasip1` | `response="c"`, `answer="c"` | correct | optional; needs WASI SDK |
| `cpp-eval-simple-equality` | C++ → `wasm32-wasip1` | `response="cpp"`, `answer="cpp"` | correct | optional; needs WASI SDK |

The optional cases are skipped with a clear message if their compiler is not
installed, so the script remains safe as a live-demo command.

## Real Lambda Feedback source material

To pull public evaluation functions currently in the Lambda Feedback GitHub org:

```bash
scripts/fetch-lambda-examples.sh
```

This clones a reference set into `.demo-lambda-sources/` and writes
`.demo-lambda-sources/SUMMARY.md`. The set currently includes:

- Python numeric/scientific: `IsSimilar`, `ArrayEqual`, `SymbolicEqual`, `compareBoolean`, `compareSets`, `shortTextAnswer`, `evaluatePython`
- Boilerplates: `evaluation-function-boilerplate-python`
- Non-Python language examples: `wolframIsSimilar`, `evaluation-function-boilerplate-lean`

These repositories are not vendored into Shimmy; they are fetched as live
reference material for choosing realistic inputs and explaining what real
evaluation functions look like.

## Python / NumPy / SymPy demos

The current `reactor-python` backend is intentionally Linux-only in this branch
because its snapshot strategies depend on Linux process/memory semantics. On
Linux, realistic Python scenarios can be run with the existing CPython-WASI
artifact:

```bash
FUNCTION_INTERFACE=reactor-python \
FUNCTION_COMMAND=$PWD/internal/execution/wasm/testdata/python-reactor.wasm \
FUNCTION_WASM_PYTHON_SCRIPT=$PWD/examples/eval-numpy/eval.py \
FUNCTION_WASM_MAX_MEMORY_PAGES=8192 \
FUNCTION_MAX_PROCS=1 \
FUNCTION_TIMEOUT=30s \
bin/shimmy-demo serve --host 127.0.0.1 --port 18080
```

Sample request:

```bash
curl -sS -X POST http://127.0.0.1:18080/ \
  -H 'Content-Type: application/json' \
  -H 'Command: eval' \
  --data '{"response":"1,2,3.000001","answer":"1,2,3","params":{"rtol":0.00001}}' \
  | python3 -m json.tool
```

Good real Lambda Feedback candidates for Python/WASM demo scripts:

| Source repo | Why it is useful | WASM path |
|---|---|---|
| `lambda-feedback/IsSimilar` | numeric tolerance grading using NumPy scalar helpers | reactor-python / NumPy |
| `lambda-feedback/ArrayEqual` | array/matrix comparison using `numpy.allclose` | reactor-python / NumPy |
| `lambda-feedback/SymbolicEqual` | symbolic algebra with SymPy | reactor-python / SymPy |
| `lambda-feedback/compareBoolean` | boolean expression parsing + SymPy logic | reactor-python / SymPy |
| `lambda-feedback/evaluatePython` | code runner / sandbox story | separate security demo; not a pure evaluator port |

## Non-WASM fallback language demos

Some real Lambda Feedback languages are better shown as integration constraints
rather than first-class Shimmy-WASM modules:

- Wolfram Language (`wolframIsSimilar`, `wolframEvaluationFunction`): real Lambda
  workloads exist, but they require a Wolfram runtime/container, not a generic
  WASI module today.
- Lean (`evaluation-function-boilerplate-lean`): real boilerplate exists; useful
  as a future-language story, but it needs a Lean build/runtime integration path.
- JavaScript: `examples/eval-js/` already demonstrates Javy/QuickJS via the
  existing RPC/subprocess lane; it needs `javy` and a WASM runner command.

For supervisor demos, lead with `scripts/demo-wasm.sh` or
`scripts/demo-scenarios.sh`, then use `scripts/fetch-lambda-examples.sh` to show
that the chosen Python/NumPy/SymPy cases correspond to real public Lambda
Feedback evaluation functions.
