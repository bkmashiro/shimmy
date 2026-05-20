# eval-cpp

Shimmy evaluation function example written in C++17, compiled to `wasm32-wasip1`
using [WASI-SDK](https://github.com/WebAssembly/wasi-sdk).

No external dependencies — JSON is handled with simple `string_view` searches,
sufficient for the fixed eval ABI request format.

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

Edit the method dispatch in `eval.cpp`. The `alloc` and `evaluate` exports
must remain — they are the guest ABI shimmy calls on every request.

See `WASM.md` in the repo root for the full ABI specification.
