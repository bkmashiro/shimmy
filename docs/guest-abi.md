# Guest ABI

Every WASM module loaded by the `wasm` dispatcher must export exactly two
functions. This document specifies their contracts in full.

## Exported Functions

### `alloc(size i32) → i32`

Called by the host before every request to obtain a pointer into guest linear
memory where the host will write the request payload.

**Contract:**

- The guest must return a pointer `P` such that `P != 0` and
  `[P, P + size)` is writable within the current linear memory.
- The host writes exactly `size` bytes starting at `P` immediately after the
  call returns; the guest must not move or free that region before `evaluate`
  is called.
- `alloc` is called once per request, always before `evaluate`.
- If the guest cannot satisfy the allocation (OOM), it must return `0`. The
  host treats a zero return as an out-of-memory error and aborts the request.

**Implementation note:** WASM is single-threaded and the host calls `alloc`
and `evaluate` on the same goroutine without any concurrency. A static buffer
is the simplest correct implementation (as in all shipped examples). A
bump-pointer heap allocator is also fine. The snapshot/restore cycle resets
the heap state between requests anyway.

**What the host does with the null check:**

```go
if reqPtr == 0 {
    return nil, fmt.Errorf("wasm: alloc returned NULL (out of memory)")
}
```

### `evaluate(req_ptr i32, req_len i32) → i32`

Called by the host with the pointer and length of the JSON request written into
guest memory by the preceding `alloc` call.

**Contract:**

- `req_ptr` is the pointer returned by the immediately preceding `alloc` call.
- `req_len` is the number of bytes written (same value that was passed to
  `alloc`).
- The guest reads `req_len` bytes starting at `req_ptr`, processes the
  request, and writes a length-prefixed JSON response somewhere in linear
  memory.
- Returns a pointer `R` to the response. The response layout at `R` is:

  ```
  bytes [R,   R+4)     — uint32 little-endian length L
  bytes [R+4, R+4+L)  — L bytes of UTF-8 JSON
  ```

- The host validates: `R + 4 + L <= mem.Size()`. A violation is treated as a
  fatal guest error (the supervisor is marked unhealthy).

## Request JSON Envelope

The host marshals each request as:

```json
{
  "method": "<string>",
  "params": { ... }
}
```

`method` is one of `"eval"`, `"preview"`, or `"healthcheck"`. `params` is the
full `map[string]any` from the HTTP request body (i.e., `response`, `answer`,
`params` fields for eval; similar for preview).

Example eval request:

```json
{
  "method": "eval",
  "params": {
    "response": "42",
    "answer": "42",
    "params": {}
  }
}
```

## Response Binary Layout

```
offset  size  type      description
──────  ────  ────────  ──────────────────────────────────────────────────
0       4     uint32LE  length L of the JSON body that follows
4       L     bytes     UTF-8 JSON object (map[string]any)
```

The JSON body is returned verbatim to the HTTP caller as the response payload.
The host does not add any envelope around it; the JSON must already be in the
correct shimmy schema format expected by the runtime (see
`runtime/schema/response-eval.json`).

Typical eval response:

```json
{
  "command": "eval",
  "result": {
    "is_correct": true,
    "feedback": "Correct!"
  }
}
```

## Bounds Validation

The host performs the following check before reading the response body:

```go
if uint64(resPtr)+4+uint64(resLen) > uint64(mem.Size()) {
    return nil, fmt.Errorf(
        "wasm: response out of bounds: resPtr=%d resLen=%d memSize=%d",
        resPtr, resLen, mem.Size(),
    )
}
```

If this check fails the request errors out and the supervisor is **not**
returned to the pool (it is replaced). A corrupt or malicious guest that
returns a pointer outside linear memory cannot read host memory; wazero's
`mem.Read` is bounds-checked and returns `false` rather than performing
an out-of-bounds access.

## Complete Working Examples

### Go (TinyGo-style / `GOOS=wasip1 GOARCH=wasm`)

```go
//go:build wasip1

package main

import (
    "encoding/binary"
    "encoding/json"
    "unsafe"
)

var reqBuf  [256 * 1024]byte
var respBuf [256 * 1024]byte

//go:wasmexport alloc
func alloc(size int32) int32 {
    _ = size
    return int32(uintptr(unsafe.Pointer(&reqBuf[0])))
}

//go:wasmexport evaluate
func evaluate(reqPtr int32, reqLen int32) int32 {
    type Request struct {
        Method string         `json:"method"`
        Params map[string]any `json:"params"`
    }
    var req Request
    json.Unmarshal(reqBuf[:reqLen], &req)

    resp, _ := json.Marshal(map[string]any{
        "command": req.Method,
        "result":  map[string]any{"is_correct": true},
    })
    binary.LittleEndian.PutUint32(respBuf[:4], uint32(len(resp)))
    copy(respBuf[4:], resp)
    return int32(uintptr(unsafe.Pointer(&respBuf[0])))
}

func main() {}
```

Build:

```bash
GOOS=wasip1 GOARCH=wasm go build -o eval.wasm .
```

### Rust

```rust
static mut REQ_BUF:  [u8; 256 * 1024] = [0u8; 256 * 1024];
static mut RESP_BUF: [u8; 256 * 1024] = [0u8; 256 * 1024];

#[no_mangle]
pub unsafe extern "C" fn alloc(_size: i32) -> i32 {
    REQ_BUF.as_ptr() as i32
}

#[no_mangle]
pub unsafe extern "C" fn evaluate(_req_ptr: i32, req_len: i32) -> i32 {
    let req_bytes = &REQ_BUF[..req_len as usize];
    // parse req_bytes, build response JSON ...
    let resp = br#"{"command":"eval","result":{"is_correct":true}}"#;
    let len = resp.len() as u32;
    RESP_BUF[0..4].copy_from_slice(&len.to_le_bytes());
    RESP_BUF[4..4 + resp.len()].copy_from_slice(resp);
    RESP_BUF.as_ptr() as i32
}
```

Build:

```bash
rustup target add wasm32-wasip1
cargo build --target wasm32-wasip1 --release
```

### C (WASI SDK)

```c
#include <stdint.h>
#include <string.h>
#include <stdio.h>

static uint8_t req_buf[256 * 1024];
static uint8_t resp_buf[256 * 1024];

static void write_resp(const char *json) {
    uint32_t len = (uint32_t)strlen(json);
    resp_buf[0] = (uint8_t)(len & 0xff);
    resp_buf[1] = (uint8_t)((len >> 8) & 0xff);
    resp_buf[2] = (uint8_t)((len >> 16) & 0xff);
    resp_buf[3] = (uint8_t)((len >> 24) & 0xff);
    memcpy(resp_buf + 4, json, len);
}

__attribute__((visibility("default")))
int32_t alloc(int32_t size) {
    (void)size;
    return (int32_t)(uintptr_t)req_buf;
}

__attribute__((visibility("default")))
int32_t evaluate(int32_t req_ptr, int32_t req_len) {
    (void)req_ptr;
    (void)req_len;
    write_resp("{\"command\":\"eval\",\"result\":{\"is_correct\":true}}");
    return (int32_t)(uintptr_t)resp_buf;
}
```

Build (requires WASI SDK):

```bash
/opt/wasi-sdk/bin/clang \
    --sysroot /opt/wasi-sdk/share/wasi-sysroot \
    -target wasm32-wasi \
    -o eval.wasm eval.c
```

## Common Mistakes

**Returning a stack pointer.**  
Stack-allocated buffers become invalid the moment the function returns. The
host reads the response _after_ `evaluate` returns; the memory at the stack
pointer is either reused or zeroed by the runtime. Always write responses into
a static or heap-allocated buffer whose lifetime outlasts the call.

```c
// WRONG — stack buffer evaporates on return
int32_t evaluate(...) {
    char resp[64];
    snprintf(resp, sizeof(resp), "{\"is_correct\":true}");
    // ... write length prefix, return pointer to resp ...
    return (int32_t)(uintptr_t)resp;  // dangling pointer
}
```

**Forgetting the 4-byte length prefix.**  
The host reads 4 bytes at `resPtr` before reading any JSON. If you return a
pointer directly to JSON without a length prefix, the host will interpret the
first 4 bytes of your JSON as a length, almost certainly producing a
`response out of bounds` error or garbage.

**Returning `alloc` memory for the response.**  
The host writes the _request_ into the `alloc` region immediately after
`alloc` returns, before calling `evaluate`. If your response buffer and request
buffer are the same static region, you must copy the request data out before
building the response — or use separate buffers (as in all examples above).

**Not exporting `alloc` or `evaluate` with the correct visibility.**  
In Rust, `#[no_mangle]` is required. In C, `__attribute__((visibility("default")))` is
required when building with `--export-dynamic` or when the linker strips
unexported symbols. In Go (wasip1), `//go:wasmexport` is the correct directive;
the older `//export` pragma does not work for wazero-callable exports.

**Using a linker that strips unused exports.**  
Some WASI toolchains enable `--gc-sections` or `--strip-all` by default. If
`alloc` or `evaluate` appear unused (no Go/Rust callers), the linker will
remove them. Explicitly keep them with `--export=alloc --export=evaluate` or
the language-specific equivalent.
