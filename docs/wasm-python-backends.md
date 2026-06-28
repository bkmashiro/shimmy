# WASM and Python backend compatibility matrix

Shimmy has several opt-in execution paths. They intentionally share the public
HTTP/request contract, but they do not all solve the same packaging problem.

## Backend selection

| Use case | Shimmy config | Runtime boundary | Evaluator shape | Snapshot/reset behavior | Notes |
| --- | --- | --- | --- | --- | --- |
| Existing process worker | `FUNCTION_INTERFACE=rpc` or `file` | external process | any worker that speaks Shimmy's existing RPC/file protocol | worker-defined | Default behavior. Still requires `FUNCTION_COMMAND`. |
| Pre-built WASI/WASM artifact | `FUNCTION_INTERFACE=wasm` | in-process wazero | `eval.wasm` exporting Shimmy's internal `alloc` + `evaluate` ABI | warm instance + linear-memory restore | Best for Go/Rust/C/C++ style evaluators that can compile to a single module. Shimmy does not run the language build. |
| Pyodide Python | `FUNCTION_INTERFACE=pyodide` | Node.js process + Pyodide | Python script or package root/entrypoint | Pyodide runner reloads evaluator modules per request | Best for Python evaluators that need Pyodide packages. Uses the existing JSON-RPC stdio worker path, not in-process wazero. |
| CPython reactor WASM | `FUNCTION_INTERFACE=wasm` + `FUNCTION_WASM_PROFILE=python-reactor` | in-process wazero | reactor-mode CPython WASM artifact plus script or package root/entrypoint | warm CPython module + linear-memory restore | Optional Linux/reference path. The large `python-reactor.wasm` artifact is supplied externally and is not committed. |

## Configuration quick reference

### Generic WASM artifact

```shell
FUNCTION_INTERFACE=wasm \
FUNCTION_WASM_MODULE=/path/to/eval.wasm \
shimmy serve
```

The module must export Shimmy's private host/guest adapter ABI:

```text
alloc(size uint32) -> ptr uint32
evaluate(ptr uint32, len uint32) -> response_ptr uint32
```

This ABI is not the public Lambda Feedback/µEd evaluator contract; it is only the
internal boundary between Shimmy and a compiled guest module.

### Pyodide script/package mode

```shell
FUNCTION_INTERFACE=pyodide \
FUNCTION_PYODIDE_RUNNER=$(pwd)/examples/demo-pyodide-python/runner.js \
FUNCTION_PYODIDE_ROOT=$(pwd)/examples/demo-pyodide-package \
FUNCTION_PYODIDE_EVAL_ENTRYPOINT=evaluation_function.evaluation:evaluation_function \
FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT=evaluation_function.preview:preview_function \
shimmy serve
```

Use `FUNCTION_PYODIDE_SCRIPT` instead of `FUNCTION_PYODIDE_ROOT` for a single
script evaluator.

### Python reactor script/package mode

```shell
FUNCTION_INTERFACE=wasm \
FUNCTION_WASM_PROFILE=python-reactor \
FUNCTION_WASM_MODULE=/path/to/python-reactor.wasm \
FUNCTION_WASM_MAX_MEMORY_PAGES=8192 \
FUNCTION_LF_ROOT=$(pwd)/examples/demo-python-reactor-package \
FUNCTION_LF_EVAL_ENTRYPOINT=evaluation_function.evaluation:evaluation_function \
FUNCTION_LF_PREVIEW_ENTRYPOINT=evaluation_function.preview:preview_function \
shimmy serve
```

Use `FUNCTION_WASM_PYTHON_SCRIPT` for a single Python script. If it is unset and
`FUNCTION_LF_ROOT` is set, Shimmy generates a small compatibility script that
imports the named package entrypoints and mounts the package root read-only into
WASI.

## What is intentionally out of scope

- Shimmy does not run `go build`, `cargo build`, `make`, or language-specific
  bundlers during `shimmy serve` for the generic WASM backend.
- The generic WASM backend does not include Pyodide, CPython, Node.js, Lean,
  Wolfram, or other runtime-heavy language packaging.
- `python-reactor.wasm` is not stored in this repository; tests and demos that
  need it are optional and skip when `PYTHON_REACTOR_WASM` is not set.
- Dirty-page/userfaultfd snapshot optimization is separate from these minimal
  compatibility paths; the portable baseline is full linear-memory restore.

## Reviewer checklist

Run the stable checks first:

```shell
go test ./internal/execution ./internal/execution/wasm
go test ./...
scripts/demo-wasm.sh
scripts/demo-cpp-wasm.sh
scripts/demo-pyodide-python.sh
scripts/demo-pyodide-package.sh
git diff --check
```

Optional Linux-only python-reactor checks, if a reactor artifact is available:

```shell
PYTHON_REACTOR_WASM=/path/to/python-reactor.wasm \
  go test ./internal/execution ./internal/execution/wasm \
    -run 'TestPythonReactorPackageEntrypoint_OptionalArtifactSmoke|TestReactorPythonDispatcher_OptionalArtifactSmoke|TestInstantiateEnvModule'

PYTHON_REACTOR_WASM=/path/to/python-reactor.wasm \
  scripts/demo-python-reactor-package.sh
```

Without `PYTHON_REACTOR_WASM`, the artifact smoke tests and demo script should
skip rather than fail.

## Suggested stacked review order

1. Generic WASM backend and Go/Rust/C++ artifact examples.
2. Pyodide script/package compatibility backend.
3. Python-reactor profile shell and env imports.
4. Optional python-reactor artifact smoke.
5. Python-reactor package entrypoints and optional demo docs.

Each layer is opt-in; merging an earlier layer should not require merging later
experimental Python reactor follow-ups.
