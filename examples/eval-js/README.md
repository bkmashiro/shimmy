# eval-js — JavaScript evaluation functions via QuickJS/javy

A JavaScript evaluation function runner compiled to WebAssembly using
[javy](https://github.com/bytecodealliance/javy) (Bytecode Alliance's
QuickJS-to-WASM toolchain).

## Architecture

```
eval.js (user eval function)
    ↓  build-runner.sh embeds source into runner.js
runner.js (LSP-framed JSON-RPC loop)
    ↓  javy build → QuickJS compiled to wasm32-wasi
runner.wasm
    ↓  wazero run (subprocess per pool slot)
shimmy FUNCTION_INTERFACE=rpc
```

shimmy uses the existing `rpc` dispatcher with subprocess mode — the same path
as the Pyodide runner.  No new dispatcher is needed.  The runner speaks the
same JSON-RPC 2.0 protocol over stdio that shimmy already uses for subprocess
workers.

```
FUNCTION_INTERFACE=rpc
FUNCTION_RPC_TRANSPORT=stdio
FUNCTION_COMMAND=wazero run runner.wasm
```

## Why javy / QuickJS

[javy](https://github.com/bytecodealliance/javy) compiles JavaScript to
`wasm32-wasi` using [QuickJS](https://bellard.org/quickjs/) as the embedded
JavaScript engine.  The resulting WASM module runs under wazero with full WASI
capability restrictions:

- No filesystem access by default
- No network access
- No environment variable leakage
- Hard memory cap via `WithMemoryLimitPages`
- Per-request timeout via context cancellation

Each pool slot spawns one `wazero run runner.wasm` subprocess.  That subprocess
stays alive for the lifetime of the slot, servicing requests one at a time
through its stdin/stdout.  shimmy manages the pool and routes requests.

## State isolation

Each request calls `evaluationFunction()` inside a fresh `Function()` scope
(analogous to Python's `exec(source, {})`).  Any variable mutations inside the
user's code are scoped to that invocation and do not leak between requests.

This is different from the Go WASM path, where full linear-memory
snapshot/restore is used after every call.  Here, isolation is at the
JavaScript scope level, not the memory level — sufficient for pure-function
eval functions.

## Prerequisites

- **javy** — download the pre-built binary for your platform:
  ```bash
  # Linux arm64 (e.g. Apple Silicon Docker, AWS Graviton)
  curl -sL https://github.com/bytecodealliance/javy/releases/download/v8.1.1/javy-arm-linux-v8.1.1.gz \
      | gunzip > /usr/local/bin/javy && chmod +x /usr/local/bin/javy

  # Linux x86_64
  curl -sL https://github.com/bytecodealliance/javy/releases/download/v8.1.1/javy-x86_64-linux-v8.1.1.gz \
      | gunzip > /usr/local/bin/javy && chmod +x /usr/local/bin/javy
  ```

- **wazero** (to run the module):
  ```bash
  go install github.com/tetratelabs/wazero/cmd/wazero@latest
  ```

- **node or python3** (for the build script, to JSON-encode the eval source)

## Files

| File | Description |
|------|-------------|
| `eval.js` | User-defined evaluation function (`evaluationFunction`) |
| `runner.js` | shimmy runner — LSP-framed JSON-RPC loop over stdin/stdout |
| `build-runner.sh` | Embeds `eval.js` into `runner.js` and compiles with javy |
| `runner.wasm` | Pre-built output (built from the default `eval.js`) |

## Building

```bash
cd examples/eval-js

# Build with the default eval.js
JAVY=/usr/local/bin/javy ./build-runner.sh

# Build with a custom eval script
JAVY=/usr/local/bin/javy ./build-runner.sh /path/to/my_eval.js runner.wasm
```

The build script:
1. JSON-encodes the eval source so it can be embedded as a string literal
2. Patches the `EVAL_SOURCE` constant in `runner.js`
3. Compiles the patched file with `javy build -J javy-stream-io=y`

## Running

### Direct test (without shimmy)

```bash
# Single request via manual LSP framing:
MSG='{"jsonrpc":"2.0","id":1,"method":"eval","params":[{"response":"42","answer":"42"}]}'
printf "Content-Length: %d\r\n\r\n%s" "${#MSG}" "$MSG" | wazero run runner.wasm
```

Expected output:
```
Content-Length: 109

{"jsonrpc":"2.0","id":1,"result":{"is_correct":true,"feedback":"Correct! 42 matches 42.","absolute_error":0}}
```

### Via shimmy

```bash
FUNCTION_INTERFACE=rpc \
FUNCTION_RPC_TRANSPORT=stdio \
FUNCTION_COMMAND="wazero run $(pwd)/runner.wasm" \
PORT=8080 \
  shimmy serve
```

Then send a request:

```bash
curl -s -X POST http://localhost:8080/eval \
  -H 'Content-Type: application/json' \
  -d '{"response": "3.14159", "answer": "3.14159"}'
```

## eval.js contract

The loaded script must define:

```js
function evaluationFunction(response, answer, params) {
    // response: string | number — student's answer
    // answer:   string | number — reference answer
    // params:   object          — optional extra parameters

    return {
        is_correct: boolean,
        feedback:   string,
        // any additional fields are returned verbatim to the caller
    };
}
```

The function is called in a fresh `Function()` scope per request, so
global-variable side effects from one request cannot affect the next.

## Wire protocol

The runner speaks JSON-RPC 2.0 framed with LSP-style `Content-Length` headers:

```
Content-Length: <N>\r\n
\r\n
{"jsonrpc":"2.0","id":1,"method":"eval","params":[{"response":"42","answer":"42"}]}
```

shimmy sends `method` as the configured function interface method name
(typically `"eval"` or `"evaluate"`).  All non-`healthcheck` methods dispatch
to `evaluationFunction` in the loaded script.

## Comparison with other paths

| Path | Interface | Isolation | Notes |
|------|-----------|-----------|-------|
| Go eval function | `wasm` | Linear-memory snapshot/restore | Fastest; alloc/evaluate ABI |
| Python (CPython-WASI) | `wasm` | Linear-memory snapshot/restore | 26 MB module |
| Python (Pyodide/Node.js) | `rpc` (subprocess) | JS `exec(source, {})` scope | scipy/pandas support |
| **JS (javy/QuickJS)** | **`rpc` (subprocess)** | **JS `Function()` scope** | **This example** |

## Troubleshooting

**Build fails with `JS compilation failed`**: make sure your eval.js has no
syntax errors — `node -c eval.js` to check.

**`wazero not found`**: install with `go install github.com/tetratelabs/wazero/cmd/wazero@latest`.

**`javy not found`**: set `JAVY=/path/to/javy` or put javy on PATH.

**Slow first request**: wazero JIT-compiles the WASM module on first run.
Subsequent requests to the same subprocess instance are fast.
