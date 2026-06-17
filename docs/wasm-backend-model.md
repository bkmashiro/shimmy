# WASM backend model

This document separates three concepts that are easy to conflate:

1. **Runtime interface** — how Shimmy runs and communicates with an evaluator.
2. **WASM runtime/profile** — what kind of WASM guest Shimmy expects once it has a module.
3. **Build/deployment recipe** — how a source language is compiled or bundled into that module.

The important rule is: **do not create one `FUNCTION_INTERFACE` value per source language**.

For copy-pasteable launch configurations, see [deployment-recipes.md](deployment-recipes.md).

## 1. Runtime interface

`FUNCTION_INTERFACE` should describe the execution and communication boundary, not the language used to author the evaluator.

| Interface | Meaning | Current status |
|---|---|---|
| `rpc` | Existing persistent subprocess with JSON-RPC transport. | Baseline / compatibility path. |
| `file` | Existing subprocess-per-request file protocol. | Baseline / compatibility path. |
| `wasm` | In-process wazero WASM/WASI module instance pool. | Generic WASM ABI path works when a built `.wasm` module is provided. |
| `pyodide` | Node/Pyodide subprocess compatibility lane for heavy Python packages. | Works for compatibility demos; not the same isolation/performance model as `wasm`. |
| `reactor-python` | CPython-WASI reactor path with snapshot/restore semantics. | Compatibility alias for `FUNCTION_WASM_PROFILE=python-reactor` under `FUNCTION_INTERFACE=wasm`; kept for migration compatibility. |
| `python-wasm` | Older resident Python/WASM path. | Legacy/comparison only; state can leak across requests. |

## 2. WASM profile

Once `FUNCTION_INTERFACE=wasm` is selected, Shimmy still needs to know what kind of WASM guest it is loading.

**Preferred deployment recipe for Python-reactor on WASM**:

```bash
FUNCTION_INTERFACE=wasm
FUNCTION_WASM_PROFILE=python-reactor
FUNCTION_WASM_MODULE=/path/to/python-reactor.wasm
FUNCTION_WASM_PYTHON_SCRIPT=/path/to/eval.py
```

Legacy compatibility mode is still accepted for now:

```bash
FUNCTION_INTERFACE=reactor-python
FUNCTION_WASM_PYTHON_SCRIPT=/path/to/eval.py
FUNCTION_COMMAND=/path/to/python-reactor.wasm
```

| Profile | What Shimmy receives | Runtime expectation |
|---|---|---|
| `generic` | A pre-built `.wasm` module. | Exposes the Shimmy `alloc` / `evaluate` ABI directly. Source language is irrelevant at runtime. |
| `python-reactor` | A CPython-WASI reactor module plus evaluator script/package config. | Host initializes Python, loads evaluator code, snapshots post-init memory, restores after requests. Current `v1.0.13` artifact exports the same host-facing `alloc` / `evaluate` ABI and retains legacy `py_exec` exports. |
| `js-javy` / future JS profile | A WASI module produced by a JS compiler/bundler. | Needs a clear ABI adapter; current JS demo still uses an RPC/subprocess-style route. |

The repository now accepts `FUNCTION_INTERFACE=wasm` + `FUNCTION_WASM_PROFILE` for profile selection and keeps `FUNCTION_INTERFACE=reactor-python` as a compatibility alias.

## 3. Build/deployment recipe

Language-specific compilation belongs in deployment tooling and documentation. Shimmy's request dispatcher should not guess source language from imports or `requirements.txt` and should not grow `rust-wasm`, `go-wasm`, `python-wasm`, `js-wasm`, etc. as peer runtime modes.

Examples:

| Source language | Build/deployment recipe | Runtime config shape |
|---|---|---|
| Go | `GOOS=wasip1 GOARCH=wasm go build ... -o eval.wasm` | `FUNCTION_INTERFACE=wasm`, module path points to `eval.wasm`. |
| Rust | `cargo build --target wasm32-wasip1` with a Shimmy ABI wrapper. | `FUNCTION_INTERFACE=wasm`, module path points to the built artifact. |
| C/C++ | WASI SDK / clang targeting `wasm32-wasip1`. | `FUNCTION_INTERFACE=wasm`, module path points to the built artifact. |
| Plain Python / NumPy subset | CPython-WASI reactor artifact plus evaluator script or Lambda Feedback package bundle. | `FUNCTION_INTERFACE=wasm` + `FUNCTION_WASM_PROFILE=python-reactor`. |
| SciPy/heavy Python | Pyodide runner with packages loaded in the Pyodide ecosystem. | `FUNCTION_INTERFACE=pyodide`; compatibility lane, not the core in-process WASM pool. |
| JavaScript | Javy/QuickJS or another JS-to-WASI compiler plus an ABI adapter. | Not fully integrated into the generic WASM ABI yet; current example uses RPC with `wazero run`. |

## Current gap

The generic WASM backend already demonstrates the runtime side: if you provide a `.wasm` module exposing the ABI, Shimmy can run it in the wazero-backed pool and restore guest state between requests.

What is not yet complete/polished:

- Lambda/AWS smoke deployment guidance for the recommended recipes;
- JS/Javy integration with the generic in-process ABI instead of the current RPC/subprocess-style demo;
- more regression coverage for the legacy `py_exec` fallback path.

## Recommended documentation language

Use this wording in talks and handoff docs:

> `wasm` is the execution backend. Generic WASM means Shimmy has already been given a module that implements the ABI. Source-language compilation is a build/deployment recipe: Go, Rust, C/C++, Python, and JavaScript can each have their own recipe without becoming separate `FUNCTION_INTERFACE` modes.
