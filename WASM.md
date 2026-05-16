# WASM Execution Backend

## 1. Overview

The WASM backend is an execution interface for shimmy that runs evaluation functions compiled to WebAssembly inside a wazero sandbox instead of as OS subprocesses. It exists to provide true isolation (no filesystem access, no network, no environment leakage) and to enable memory snapshot/restore semantics that give cheap warm-start behaviour, along with N:1 multiplexing where many concurrent requests share a single compiled module image.

## 2. Architecture

```
Dispatcher
  └─ pool of N wasmSupervisors   (one per concurrent request slot)
       └─ wasmAdapter            (marshals method+data → WASM calls)
            └─ wazero api.Module (independent linear memory per instance)

      CompiledModule             (compiled once at startup, shared by all instances)
```

At startup `Dispatcher.Start` reads the `.wasm` file from disk, compiles it into a single `wazero.CompiledModule`, and instantiates `N` independent `api.Module` instances from that compiled artifact. Each instance has its own linear memory. `N` defaults to `runtime.NumCPU()` and is capped by `FUNCTION_MAX_PROCS`.

Incoming requests are served by acquiring a `wasmSupervisor` from a buffered channel pool. After the request completes the supervisor's memory is restored and it is returned to the pool.

## 3. Guest ABI

A WASM module loaded by this backend must export exactly two functions:

```
alloc(size i32) i32
evaluate(req_ptr i32, req_len i32) i32
```

### `alloc(size i32) i32`

Called by the host before every request. The guest must reserve `size` bytes of linear memory and return a pointer to the start of that region. The host will write the JSON-encoded request envelope into `[ptr, ptr+size)` immediately after the call returns.

The simplest correct implementation is a static buffer:

```go
var reqBuf [256 * 1024]byte

//go:wasmexport alloc
func alloc(size int32) int32 {
    _ = size
    return int32(uintptr(unsafe.Pointer(&reqBuf[0])))
}
```

### `evaluate(req_ptr i32, req_len i32) i32`

Called with the pointer and byte length of the JSON request in linear memory. The function must process the request and write the response into guest memory, then return a pointer `P` where the response is encoded as:

```
bytes [P,   P+4)     — uint32 little-endian response length L
bytes [P+4, P+4+L)   — L bytes of UTF-8 JSON
```

### Request envelope

The host writes a JSON object of the form:

```json
{"method": "<command>", "params": {…}}
```

`method` is the shimmy command name (e.g. `"eval"`, `"preview"`, `"healthcheck"`). `params` is the validated request body forwarded from the HTTP layer.

### Response encoding

The response is a length-prefixed JSON body written anywhere in guest linear memory. The 4-byte prefix is an unsigned 32-bit integer in little-endian byte order giving the length `L` of the JSON that follows immediately after it. The JSON body must be a plain JSON object (`map[string]any`) that shimmy returns verbatim to the HTTP client.

## 4. Writing an eval function (Go)

The complete example is in `examples/eval-go/main.go`:

```go
//go:build wasip1

package main

import (
	"encoding/binary"
	"encoding/json"
	"unsafe"
)

// Static buffers — single-threaded WASM, one request at a time.
var reqBuf [256 * 1024]byte
var respBuf [256 * 1024]byte

// alloc is called by the host to get a pointer where it will write the request.
// We always return the start of reqBuf (one request at a time).
//
//go:wasmexport alloc
func alloc(size int32) int32 {
	_ = size
	return int32(uintptr(unsafe.Pointer(&reqBuf[0])))
}

// evaluate reads the JSON request from reqBuf, processes it, and writes
// a length-prefixed JSON response to respBuf.
// Returns a pointer to respBuf[0] (4-byte LE length + JSON body).
//
//go:wasmexport evaluate
func evaluate(reqPtr int32, reqLen int32) int32 {
	_ = reqPtr // we know it's &reqBuf[0]

	// Parse request envelope: {"method": "...", "params": {...}}
	type Request struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}

	var req Request
	if err := json.Unmarshal(reqBuf[:reqLen], &req); err != nil {
		writeResp(map[string]any{"error": map[string]any{"message": err.Error()}})
		return int32(uintptr(unsafe.Pointer(&respBuf[0])))
	}

	// Simple echo eval: always mark as correct, echo back params as feedback.
	// Response must match the response-eval.json schema:
	//   {"command": "eval", "result": {"is_correct": bool, ...}}
	resp := map[string]any{
		"command": req.Method,
		"result": map[string]any{
			"is_correct": true,
			"feedback":   req.Params,
		},
	}

	writeResp(resp)
	return int32(uintptr(unsafe.Pointer(&respBuf[0])))
}

func writeResp(v map[string]any) {
	data, err := json.Marshal(v)
	if err != nil {
		data = []byte(`{"error":{"message":"marshal failed"}}`)
	}
	binary.LittleEndian.PutUint32(respBuf[:4], uint32(len(data)))
	copy(respBuf[4:], data)
}

func main() {}
```

Key points:

- The `//go:build wasip1` constraint restricts compilation to the WASI target.
- Static arrays (`reqBuf`, `respBuf`) are safe because each WASM module instance handles one request at a time; the memory snapshot/restore mechanism resets them between requests.
- `main()` is intentionally empty. It exists only to satisfy the Go toolchain.

### Build command

```bash
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o eval.wasm .
```

**Why `-buildmode=c-shared`?**

Without this flag, Go compiles the module as a WASI _command_ module. Command modules call `proc_exit` at the end of `main()`, which instructs the WASM runtime to close the module instance permanently. After the first request the instance would be dead and all subsequent calls would fail.

`-buildmode=c-shared` produces a WASI _reactor_ module instead. Reactor modules do not call `proc_exit` after initialisation. The runtime invokes `_initialize` once (setting up the Go runtime and global state), and the exported functions (`alloc`, `evaluate`) remain callable indefinitely for the lifetime of the instance. This is the semantics required for pooled, long-lived WASM instances.

## 5. Running shimmy with the WASM backend

```bash
FUNCTION_INTERFACE=wasm \
FUNCTION_COMMAND=./eval.wasm \
PORT=8080 \
./shimmy serve
```

`FUNCTION_COMMAND` is reused as the path to the `.wasm` file when `FUNCTION_INTERFACE=wasm`.

## 6. Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `FUNCTION_INTERFACE` | — | Set to `wasm` to enable this backend |
| `FUNCTION_COMMAND` | — | Path to the `.wasm` file |
| `FUNCTION_MAX_PROCS` | NumCPU | Max concurrent module instances in pool |
| `FUNCTION_WASM_MAX_MEMORY_PAGES` | 256 (16 MB) | WASM linear memory hard cap (1 page = 64 KB) |
| `FUNCTION_WASM_ALLOWED_PATHS` | — | Comma-separated host paths mounted read-only into the guest filesystem |
| `FUNCTION_WASM_ALLOWED_ENV` | — | Comma-separated env var keys passed through to the module |

`FUNCTION_TIMEOUT` (default: 30 s) applies as a per-request deadline passed via context cancellation to the wazero call.

## 7. Security model

- No filesystem access by default. Paths are only visible inside the module if explicitly listed in `FUNCTION_WASM_ALLOWED_PATHS`, mounted read-only.
- No network access. wazero provides no network-related WASI imports; there are no socket syscalls available to the guest.
- No environment variable leakage. Only keys listed in `FUNCTION_WASM_ALLOWED_ENV` are forwarded; all others are invisible to the module.
- No stdin/stdout/stderr. The module config does not attach any stdio streams, so the guest cannot read from or write to the host terminal.
- Linear memory hard cap enforced by `wazero.RuntimeConfig.WithMemoryLimitPages`. A module that tries to grow beyond `FUNCTION_WASM_MAX_MEMORY_PAGES` pages (default 256 = 16 MB) will receive a WASM memory-grow failure rather than consuming unbounded host memory.
- Per-request timeout via context cancellation. If the guest does not return within `FUNCTION_TIMEOUT`, the context is cancelled and wazero interrupts execution.
- Clean state guaranteed after every request. Linear memory is restored from a snapshot before the next request, so no state from a previous request is visible to the next caller (see section 8).

## 8. Memory snapshot / warm-start

After a module instance is instantiated and its WASI start functions (`_initialize`, `_start`) have returned, `wasmSupervisor.Start` captures the entire linear memory as a `[]byte` snapshot:

```go
size := mem.Size()
buf, _ := mem.Read(0, size)
s.snapshot = make([]byte, len(buf))
copy(s.snapshot, buf)
```

After every call to `evaluate` — whether it succeeds or returns an error — the supervisor writes the snapshot back before returning the instance to the pool:

```go
mem.Write(0, s.snapshot)
```

This is a full `memcpy` of the guest's linear memory. It gives warm-start semantics (the module is already compiled and initialised, no cold-start overhead) while guaranteeing that each request sees exactly the memory state that existed just after module initialisation. No heap allocations, global variable mutations, or cached state from one request can affect the next.

Practical note: a minimal Go WASM module is roughly 3 MB of linear memory, so each snapshot and restore is a ~3 MB memcpy. For most evaluation workloads this is negligible compared to the cost of the evaluation itself.

Future work: replace the full-copy restore with userfaultfd-based dirty-page tracking, restoring only the pages written during the request.

## 9. Multiplexing

The `.wasm` file is compiled once at startup into a single `wazero.CompiledModule`. Compilation is CPU-intensive and produces a validated, optimised representation of the WASM bytecode. This compiled artifact is immutable and safe to share across goroutines.

Each pool slot holds an independent `api.Module` instantiated from the shared `CompiledModule`. Module instances do not share linear memory or mutable state; they only share the read-only compiled code. This means N requests can execute in parallel on N instances without any locking between them — the only synchronisation is the pool channel used to acquire and release supervisors.

Pool size is fixed at startup to `FUNCTION_MAX_PROCS` (defaulting to `runtime.NumCPU()`). If all instances are busy, incoming requests block on the pool channel until a slot becomes available, honouring the caller's context deadline. There is no dynamic scaling; the pool size is chosen to match the available CPU parallelism.
