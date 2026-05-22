# Adding a New Evaluation Function Language

This guide walks through implementing an evaluation function in a language not
already covered by the shipped examples. The bar is low: you need a
wasm32-wasip1 toolchain and the ability to implement two exported functions.

## What You Need

1. A compiler that targets `wasm32-wasip1` (the WASI Preview 1 ABI).
2. A way to export C-calling-convention symbols from your binary.
3. A JSON library, or the willingness to produce JSON via string formatting.

The full ABI is specified in `docs/guest-abi.md`. In summary:

```
alloc(size i32) → i32
    Return a pointer to a region of at least `size` bytes.
    Return 0 to signal OOM.

evaluate(req_ptr i32, req_len i32) → i32
    Read the JSON request from [req_ptr, req_ptr+req_len).
    Write the response to some stable buffer as:
        [4-byte LE uint32 length][JSON bytes]
    Return a pointer to that buffer.
```

## Step-by-Step

### 1. Choose a toolchain

| Language | Toolchain | Target triple |
|---|---|---|
| Zig | `zig build` | `wasm32-wasi` |
| AssemblyScript | `asc` (npm) | native |
| Swift | Swift 5.9+ | `wasm32-unknown-wasi` |
| Go | `GOOS=wasip1 GOARCH=wasm go build` | native |
| Rust | `cargo build --target wasm32-wasip1` | native |
| C/C++ | WASI SDK `clang` | `wasm32-wasi` |

Most toolchains that target `wasm32-wasi` or `wasm32-wasip1` produce binaries
compatible with wazero's WASI preview1 host implementation.

### 2. Implement the ABI

The pattern is the same in every language: two static buffers (request and
response), `alloc` returns a pointer to the request buffer, `evaluate` reads
the request and writes a length-prefixed JSON response to the response buffer.

**Zig:**

```zig
const std = @import("std");

var req_buf: [256 * 1024]u8 = undefined;
var resp_buf: [256 * 1024]u8 = undefined;

export fn alloc(size: i32) i32 {
    _ = size;
    return @intCast(@intFromPtr(&req_buf));
}

export fn evaluate(req_ptr: i32, req_len: i32) i32 {
    _ = req_ptr;
    const req = req_buf[0..@intCast(req_len)];
    _ = req; // parse req, build result...

    const resp = "{\"command\":\"eval\",\"result\":{\"is_correct\":true}}";
    const len: u32 = @intCast(resp.len);
    std.mem.writeInt(u32, resp_buf[0..4], len, .little);
    @memcpy(resp_buf[4 .. 4 + resp.len], resp);
    return @intCast(@intFromPtr(&resp_buf));
}

pub fn main() void {}
```

Build:

```bash
zig build-exe eval.zig \
    -target wasm32-wasi \
    -O ReleaseSafe \
    -femit-bin=eval.wasm
```

**AssemblyScript:**

```typescript
// eval.ts
const reqBuf  = new StaticArray<u8>(262144);
const respBuf = new StaticArray<u8>(262144);

export function alloc(size: i32): i32 {
  return changetype<i32>(reqBuf);
}

export function evaluate(reqPtr: i32, reqLen: i32): i32 {
  // parse reqBuf[0..reqLen], build response JSON ...
  const resp = '{"command":"eval","result":{"is_correct":true}}';
  const bytes = String.UTF8.encode(resp);
  const len   = bytes.byteLength;
  store<u32>(changetype<usize>(respBuf), len);      // 4-byte LE length
  memory.copy(
    changetype<usize>(respBuf) + 4,
    changetype<usize>(bytes),
    len,
  );
  return changetype<i32>(respBuf);
}
```

Build:

```bash
npm install -g assemblyscript
asc eval.ts \
    --target wasi \
    --outFile eval.wasm \
    --optimizeLevel 3
```

**Swift (wasm32-unknown-wasi with SwiftWasm):**

```swift
// eval.swift
import Foundation

var reqBuf  = [UInt8](repeating: 0, count: 256 * 1024)
var respBuf = [UInt8](repeating: 0, count: 256 * 1024)

@_silgen_name("alloc")
func alloc(_ size: Int32) -> Int32 {
    return Int32(bitPattern: UInt32(UInt(bitPattern: &reqBuf)))
}

@_silgen_name("evaluate")
func evaluate(_ reqPtr: Int32, _ reqLen: Int32) -> Int32 {
    let resp = #"{"command":"eval","result":{"is_correct":true}}"#
    let bytes = Array(resp.utf8)
    let len = UInt32(bytes.count)
    withUnsafeMutableBytes(of: &respBuf[0]) { ptr in
        ptr.storeBytes(of: len.littleEndian, as: UInt32.self)
    }
    for (i, b) in bytes.enumerated() { respBuf[4 + i] = b }
    return Int32(bitPattern: UInt32(UInt(bitPattern: &respBuf)))
}
```

Build (requires SwiftWasm toolchain):

```bash
swiftc \
    -target wasm32-unknown-wasi \
    -sdk /opt/swift-wasm/swift-stdlib-wasi.sdk \
    -o eval.wasm eval.swift
```

Swift WASM support is available via the [SwiftWasm project](https://swiftwasm.org/).

### 3. Build and inspect the binary

Verify the exports are present before running:

```bash
# Using wasm-objdump (from wabt)
wasm-objdump -x eval.wasm | grep -E "^  - func|Export"
# Look for:
#   Export[2]:
#    - func[N] <alloc> -> "alloc"
#    - func[M] <evaluate> -> "evaluate"
```

Or with `wasm2wat`:

```bash
wasm2wat eval.wasm | grep -E '^\s*\(export'
# (export "alloc" (func $alloc))
# (export "evaluate" (func $evaluate))
# (export "memory" (mem $memory))    ← required by WASI
```

The `memory` export is required by wazero's WASI host implementation. Most
WASI toolchains export it automatically.

### 4. Test Locally

**Using the `shimmy-eval` CLI** (if built from source):

```bash
cd /path/to/shimmy-wasm
go run ./cmd/eval \
    --module eval.wasm \
    --method eval \
    --input '{"response":"42","answer":"42","params":{}}'
```

**Using `wazero run` (standalone wazero CLI):**

```bash
# Install wazero CLI
go install github.com/tetratelabs/wazero/cmd/wazero@latest

# Run in reactor mode (if your module uses _initialize)
wazero run --interpreter eval.wasm
```

**Running the full server locally:**

```bash
FUNCTION_INTERFACE=wasm \
FUNCTION_COMMAND=./eval.wasm \
FUNCTION_TIMEOUT=10s \
go run . serve

# In another terminal:
curl -s -X POST http://localhost:8080/eval \
    -H 'Content-Type: application/json' \
    -d '{"response":"42","answer":"42"}' | jq .
```

### 5. Plugging into the Dispatcher

No code changes to shimmy-wasm are needed. Set two environment variables:

```bash
FUNCTION_INTERFACE=wasm
FUNCTION_COMMAND=path/to/eval.wasm
```

That is all. The `wasm.Dispatcher` reads the module path, compiles it, and
manages the pool.

```bash
# Docker
docker run --rm \
  -e FUNCTION_INTERFACE=wasm \
  -e FUNCTION_COMMAND=/app/eval.wasm \
  -v $(pwd)/eval.wasm:/app/eval.wasm:ro \
  -p 8080:8080 \
  ghcr.io/lambda-feedback/shimmy-wasm:latest
```

For AWS Lambda, package the `.wasm` file alongside the shimmy-wasm binary in
the Lambda deployment zip. Lambda's `/tmp` can be used for the compile cache.

### 6. Common Pitfalls

**Stack pointer returned as the response buffer.**  
If your response buffer is a local variable (stack-allocated), its address
becomes invalid the moment the function returns. The host reads the response
_after_ `evaluate` returns. Use a module-level static or heap-allocated buffer.

```zig
// WRONG
export fn evaluate(req_ptr: i32, req_len: i32) i32 {
    var buf: [1024]u8 = undefined;  // stack memory
    // ... write to buf ...
    return @intCast(@intFromPtr(&buf));  // dangling after return!
}

// CORRECT
var resp_buf: [1024]u8 = undefined;  // module-level static
export fn evaluate(req_ptr: i32, req_len: i32) i32 {
    // ... write to resp_buf ...
    return @intCast(@intFromPtr(&resp_buf));
}
```

**Missing 4-byte length prefix.**  
The first 4 bytes at the returned pointer must be the response length as a
little-endian `uint32`. The host reads length first, then the JSON body. If
you forget this, the host interprets the first 4 bytes of your JSON as a huge
length, the bounds check fails, and the supervisor is discarded.

**Export visibility stripped by the linker.**  
Linkers that use dead-code elimination may strip `alloc` and `evaluate` if no
call site in the module references them. Mark them for export explicitly:

- Zig: `export fn` keyword.
- AssemblyScript: `export function` keyword.
- C: `__attribute__((visibility("default")))` (required with `-fvisibility=hidden`).
- Swift: `@_silgen_name` exports the symbol but you also need to ensure the
  linker does not prune it; add `-Xlinker --export=alloc -Xlinker --export=evaluate`.

**WASI vs. bare WASM.**  
shimmy-wasm loads modules via `wasi_snapshot_preview1.Instantiate` followed by
`rt.InstantiateModule`. The module must be a valid WASI module (it may import
from `wasi_snapshot_preview1`) but does not need to use any WASI functions —
`alloc` and `evaluate` can be pure computation. A bare WASM module (no WASI
imports at all) will still work; wazero allows modules that import a subset of
available host functions.

**Module starts with `_start` but needs reactor mode.**  
If your toolchain generates a module with `_start` (command mode) and no
`_initialize`, the supervisor calls `WithStartFunctions("_initialize", "_start")`.
wazero will call `_initialize` if it exists, then `_start` if it exists. For
command-mode modules `_start` runs `main` and may call `proc_exit(0)`, which
closes the module. shimmy-wasm's WASM dispatcher is designed for reactor-mode
modules (with `_initialize`). For languages that only support command mode, the
resident Python pattern (stdin/stdout loop) is more appropriate.

**Integer width mismatches.**  
WASM uses 32-bit addresses; pointers are `i32` in WASM type notation. Ensure
your language does not sign-extend the pointer value when returning an `i32`.
A 32-bit value that represents an address in the upper 2 GB of the 4 GB
WASM address space will have its high bit set, making it appear negative as a
signed `i32`. shimmy-wasm's host code casts the `i32` return value to `uint32`
before using it as a memory offset, so sign extension at the WASM boundary is
not an issue — but some language runtimes may warn about the signed/unsigned
mismatch.
