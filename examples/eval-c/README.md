# eval-c

Shimmy evaluation function example written in C, compiled to `wasm32-wasip1`
using [WASI-SDK](https://github.com/WebAssembly/wasi-sdk).

JSON parsing uses [jsmn](https://github.com/zserge/jsmn) — a single-header,
zero-allocation tokeniser bundled in this directory.

## Dependencies

- [WASI-SDK](https://github.com/WebAssembly/wasi-sdk/releases) installed at
  `/opt/wasi-sdk` (override with `WASI_SDK=/path/to/wasi-sdk make`)

## Build

```bash
make
# or
WASI_SDK=/path/to/wasi-sdk make
```

## Run

```bash
FUNCTION_INTERFACE=wasm \
FUNCTION_COMMAND=./eval.wasm \
PORT=8080 \
  ../../shimmy serve
```

## Writing your own

Edit the `eval` branch in `eval.c` with your evaluation logic.
The `alloc` and `evaluate` exports must remain — they are the guest ABI
that shimmy calls on every request.

See the canonical Guest ABI docs in shimmy-docs: `docs/wasm/guest-abi.md` (or the published `/wasm/guest-abi` page).
