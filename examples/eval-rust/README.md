# eval-rust

Shimmy evaluation function example written in Rust, compiled to `wasm32-wasip1`.

## Build

```bash
rustup target add wasm32-wasip1
cargo build --target wasm32-wasip1 --release
cp target/wasm32-wasip1/release/eval_rust.wasm eval.wasm
```

Or just:

```bash
make
```

## Run

```bash
FUNCTION_INTERFACE=wasm \
FUNCTION_COMMAND=./eval.wasm \
PORT=8080 \
  ../../shimmy serve
```

## Writing your own

Replace the `match method` arms in `src/lib.rs` with your own evaluation logic.
The `alloc` and `evaluate` exports must remain unchanged — they are the guest ABI
that shimmy calls on every request.

For the complete request/response shape, see the JSON schemas in
[`../../runtime/schema`](../../runtime/schema).
