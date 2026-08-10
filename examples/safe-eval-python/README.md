# `safe-eval-python` Reactor example

This example provides a small `demo` / `io_test` / `unit_test` evaluator for
student Python. It is selected at the **Shimmy backend boundary** and runs inside
the Python Reactor WASM profile; it does not adapt the Linux
`evaluatePython` implementation and does not start CPython or Node subprocesses.

```text
Shimmy HTTP
  → wazero
  → verified Python Reactor artifact
  → safe_eval.py
  → student code
```

## Why this path

AWS Lambda cannot grant the namespaces or capabilities required to make nsjail
a usable runtime boundary. Ordinary Pyodide on Node exposes a JavaScript bridge,
so it is appropriate for trusted SciPy evaluator code but not the security
boundary for arbitrary student code.

The Reactor path works within Lambda's normal process constraints:

- wazero provides the host boundary;
- `FUNCTION_WASM_ALLOWED_PATHS` must remain empty, so no host directory is
  mounted into WASI;
- the selected artifact and manifest are verified before execution;
- request deadlines close a non-terminating WASM module;
- snapshot lifecycle restores prepared memory after every request;
- a failed or timed-out snapshot slot is closed and replaced;
- code, input, test count, and output have explicit evaluator limits.

The AST checks in `safe_eval.py` provide early feedback and reduce accidental
misuse. They are not claimed as the sandbox. The WASM capability boundary,
request deadline, memory limit, and state reset are the security controls.

## Backend selection

Use a signed Producer artifact and its matching manifest:

```bash
export FUNCTION_INTERFACE=wasm
export FUNCTION_WASM_PROFILE=python-reactor
export FUNCTION_WASM_MODULE=/opt/shimmy/runtime/shimmy-python-runtime-base.wasm
export FUNCTION_WASM_MANIFEST=/opt/shimmy/runtime/shimmy-python-runtime-base.manifest.json
export FUNCTION_WASM_PYTHON_SCRIPT="$PWD/examples/safe-eval-python/safe_eval.py"
export FUNCTION_WASM_PYTHON_LIFECYCLE=snapshot
export FUNCTION_WASM_SNAPSHOT_MODE=memcpy
export FUNCTION_WASM_ALLOWED_PATHS=
export FUNCTION_MAX_PROCS=1
export FUNCTION_WORKER_SEND_TIMEOUT=5s

shimmy serve --host 127.0.0.1 --port 8080
```

The evaluator is below the 1 MiB trusted-script limit and only uses the standard
library. Package availability comes from the manifest-validated artifact profile, never
from runtime pip or network installation:

| Student-code requirement | Backend/artifact |
|---|---|
| Standard library | Python Reactor `base` |
| NumPy | Python Reactor `numpy-core` |
| SymPy + mpmath | Python Reactor `sympy` |
| Trusted evaluator directly using SciPy | Pyodide compatibility runner |
| Existing Linux evaluator requiring `subprocess` | Existing RPC/container backend |

Do not silently fall back between these paths. Switching the module, manifest,
and runner is deployment configuration.

Runtime manifest validation establishes digest, ABI, imports/exports, and
capability consistency. Artifact authenticity, trusted Producer commit policy,
and digital-signature verification remain deployment-system responsibilities.

## Request examples

Shimmy's `eval` schema requires a non-null `answer`; use an empty string when a
mode does not need one. The `preview` schema does not accept `answer`.

### Demo

```bash
curl -sS -X POST http://127.0.0.1:8080/ \
  -H 'Content-Type: application/json' -H 'Command: eval' \
  --data '{"response":"print(6 * 7)","answer":"","params":{"mode":"demo"}}'
```

Demo returns captured stdout and `is_correct: false`; it displays execution
output but does not claim a pass condition.

### Input/output tests

```bash
curl -sS -X POST http://127.0.0.1:8080/ \
  -H 'Content-Type: application/json' -H 'Command: eval' \
  --data '{
    "response":"n = int(input())\nprint(n * n)",
    "answer":"",
    "params":{"mode":"io_test","tests":[
      {"input":"5\n","expected_output":"25\n"},
      {"input":"3\n","expected_output":"9\n","hidden":true}
    ]}
  }'
```

Each test receives a fresh Python namespace. Hidden test details omit actual and
expected output. An `inject` object can initialize variables before execution:

```json
{"inject":{"n":5},"expected_output":"25\n"}
```

### Unit tests

The example intentionally implements a small contract: zero-argument functions
whose names begin with `test_`; a failed `assert` fails that test.

```bash
curl -sS -X POST http://127.0.0.1:8080/ \
  -H 'Content-Type: application/json' -H 'Command: eval' \
  --data '{
    "response":"def square(n):\n    return n * n",
    "answer":"",
    "params":{"mode":"unit_test","test_code":"def test_square():\n    assert square(5) == 25"}
  }'
```

### Preview

```bash
curl -sS -X POST http://127.0.0.1:8080/ \
  -H 'Content-Type: application/json' -H 'Command: preview' \
  --data '{"response":"import socket","params":{}}'
```

## Built-in evaluator limits

The limits are constants in the trusted script and cannot be raised by request
parameters:

| Limit | Value |
|---|---:|
| Student code | 64 KiB |
| Captured stdout/stderr retained in memory per stream/execution | 64 KiB, enforced while writing |
| Input per test | 64 KiB |
| Tests per request | 32 |

The deployment additionally controls the Reactor memory-page limit and Shimmy
request deadline. Keep the HTTP/worker deadline short enough to bound infinite
loops and long enough for the selected profile's normal work.

## Verification

Host-side behavior tests:

```bash
python3 -m unittest examples/safe-eval-python/safe_eval_test.py -v
```

Full Linux path with a real Producer artifact:

```bash
SHIMMY_PYTHON_REACTOR_WASM=/path/to/base.wasm \
SHIMMY_PYTHON_REACTOR_MANIFEST=/path/to/base.manifest.json \
scripts/e2e-safe-eval-python.sh
```

The E2E covers all three modes, preview rejection, timeout of an infinite loop,
and successful recovery through a replacement snapshot slot. No Docker,
privileged Lambda configuration, runtime package installation, or nsjail is
required.
