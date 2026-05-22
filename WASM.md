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

	var resp map[string]any

	switch req.Method {
	case "eval":
		response, _ := req.Params["response"].(string)
		answer, _ := req.Params["answer"].(string)
		isCorrect := response == answer
		feedback := "Correct!"
		if !isCorrect {
			feedback = "Incorrect."
		}
		resp = map[string]any{
			"command": "eval",
			"result": map[string]any{
				"is_correct": isCorrect,
				"feedback":   feedback,
			},
		}

	case "preview":
		response, _ := req.Params["response"].(string)
		resp = map[string]any{
			"command": "preview",
			"result": map[string]any{
				"preview": map[string]any{
					"type":    "text",
					"content": response,
				},
			},
		}

	case "healthcheck":
		resp = map[string]any{
			"command": "healthcheck",
			"result": map[string]any{
				"status": "ok",
			},
		}

	default:
		resp = map[string]any{
			"error": map[string]any{
				"message": "unknown method: " + req.Method,
			},
		}
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

### Go / Rust / C eval functions (`wasm` interface)

```bash
FUNCTION_INTERFACE=wasm \
FUNCTION_COMMAND=./eval.wasm \
PORT=8080 \
./shimmy serve
```

`FUNCTION_COMMAND` is reused as the path to the `.wasm` file when `FUNCTION_INTERFACE=wasm`.

### Python eval functions — reactor mode (`reactor-python` interface)

Reactor mode runs CPython as a WASM reactor module with per-request memory snapshot/restore. Use this for the best isolation and state-clean guarantees.

```bash
FUNCTION_INTERFACE=reactor-python \
FUNCTION_WASM_MODULE=/app/python-reactor.wasm \
FUNCTION_WASM_PYTHON_SCRIPT=/app/eval.py \
FUNCTION_WASM_COMPILE_CACHE=/var/cache/wazero \
FUNCTION_MAX_PROCS=2 \
PORT=8080 \
./shimmy serve
```

### Python eval functions — resident mode (`python-wasm` interface)

Resident mode keeps a single long-lived CPython interpreter alive per pool slot, communicating via stdin/stdout JSON. Lower per-request overhead (~1.8 ms vs ~30 ms), but no memory snapshot — isolation relies on `exec()` namespace cleanup.

```bash
FUNCTION_INTERFACE=python-wasm \
FUNCTION_WASM_MODULE=./internal/execution/wasm/testdata/python.wasm \
FUNCTION_WASM_PYTHON_SCRIPT=/app/eval.py \
FUNCTION_WASM_COMPILE_CACHE=/var/cache/wazero \
FUNCTION_MAX_PROCS=2 \
PORT=8080 \
./shimmy serve
```

## 6. Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `FUNCTION_INTERFACE` | — | Set to `wasm` to enable this backend |
| `FUNCTION_COMMAND` | — | Path to the `.wasm` file |
| `FUNCTION_MAX_PROCS` | NumCPU | Max concurrent module instances in pool |
| `FUNCTION_WASM_MAX_MEMORY_PAGES` | 256 (16 MB) | WASM linear memory hard cap (1 page = 64 KB) |
| `FUNCTION_WASM_ALLOWED_PATHS` | — | Comma-separated host paths mounted read-only into the guest filesystem |
| `FUNCTION_WASM_ALLOWED_ENV` | — | Comma-separated env var keys passed through to the module |
| `FUNCTION_WASM_COMPILE_CACHE` | — | Directory for wazero's on-disk JIT compilation cache (see section 9) |

`FUNCTION_TIMEOUT` (default: 30 s) applies as a per-request deadline passed via context cancellation to the wazero call.

## 7. Security model

- No filesystem access by default. Paths are only visible inside the module if explicitly listed in `FUNCTION_WASM_ALLOWED_PATHS`, mounted read-only.
- No network access. wazero provides no network-related WASI imports; there are no socket syscalls available to the guest.
- No environment variable leakage. Only keys listed in `FUNCTION_WASM_ALLOWED_ENV` are forwarded; all others are invisible to the module.
- No stdin/stdout/stderr. The module config does not attach any stdio streams, so the guest cannot read from or write to the host terminal.
- Linear memory hard cap enforced by `wazero.RuntimeConfig.WithMemoryLimitPages`. A module that tries to grow beyond `FUNCTION_WASM_MAX_MEMORY_PAGES` pages (default 256 = 16 MB) will receive a WASM memory-grow failure rather than consuming unbounded host memory.
- Per-request timeout via context cancellation. If the guest does not return within `FUNCTION_TIMEOUT`, the context is cancelled and wazero interrupts execution.
- Clean state guaranteed after every request. Linear memory is restored from a snapshot before the next request, so no state from a previous request is visible to the next caller (see section 8).

### Purity requirement

The snapshot/restore guarantee is sound **only for pure (stateless) evaluation functions** — functions whose output depends solely on the inputs of the current request and not on any state accumulated from previous requests.

Under the WASI sandbox enforced by this backend, the only mutable state a guest module can retain across requests is its own **linear memory** (heap, globals, stack). All other side-effect channels are blocked:

| Channel | Status | Reason |
|---------|--------|--------|
| Filesystem writes | ❌ blocked | No writable mounts by default |
| Network I/O | ❌ blocked | No socket imports provided |
| Environment variables | read-only | Only explicitly whitelisted keys visible |
| Host process state | ❌ blocked | No shared memory, no signals |
| WASM linear memory | ✅ restored | Full memcpy restore after every request |
| WASM mutable globals | ⚠️ not restored | See note below |

Because linear memory is the sole remaining state carrier and it is explicitly restored after each request, a well-behaved eval function that only reads its inputs and writes its response has exactly the same observable behaviour as a fresh cold-start invocation.

> **Note — WASM mutable globals.** WASM modules can also carry state in mutable globals (declared with `(global $x (mut i32) ...)`). The snapshot/restore mechanism covers only linear memory; mutable globals are not snapshotted. In practice this is not a concern for any supported compilation target: Go (`GOOS=wasip1`), Rust (`wasm32-wasip1`), and C (WASI-SDK) all use globals only for the stack pointer (`__stack_pointer`), which is unconditionally saved and restored by function prologues/epilogues — so it is always consistent when `evaluate()` returns. A handcrafted `.wasm` binary could deliberately store cross-request state in a custom mutable global, but shimmy-wasm does not accept arbitrary `.wasm` uploads: all eval functions are compiled from source server-side. If that constraint ever changes, globals should be snapshotted alongside linear memory.

**Temporary filesystem access:** If an eval function needs to write temporary files (e.g. for intermediate computation), mount a per-request scratch directory via `FUNCTION_WASM_ALLOWED_PATHS` pointing to a fresh `tmpfs` path, and clean it up after each request at the host level. The guest writing to that path is a controlled, bounded side effect that does not persist across requests provided the host cleans up the directory contents.

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

### On-disk compilation cache

Set `FUNCTION_WASM_COMPILE_CACHE` to a persistent directory to enable wazero's on-disk JIT cache:

```bash
FUNCTION_WASM_COMPILE_CACHE=/var/cache/wazero ./shimmy serve
```

On the first start wazero compiles the module and writes the result to the cache directory. On subsequent starts — including container restarts and CI re-runs — the pre-compiled artifact is read from disk and `CompileModule` completes in milliseconds instead of minutes. The directory is safe to share across processes: wazero uses file locking and keyed sub-directories internally.

The cache is keyed on the binary content of the `.wasm` file. Deploying a new WASM binary automatically invalidates the cached entry.

For large binaries (e.g. CPython compiled to WASM, ~240 MB) this cache is strongly recommended: a cold compile can take 1–3 minutes, which would exceed the default fx startup timeout (now 5 minutes, raised from 15 s).

## 10. Writing an eval function (JavaScript / javy)

JavaScript eval functions run via [javy](https://github.com/bytecodealliance/javy) — Bytecode Alliance's QuickJS-to-WASM toolchain. javy compiles JavaScript to `wasm32-wasi`, embedding QuickJS as the runtime. The resulting WASM module runs under wazero with the same sandbox restrictions (no filesystem, no network, hard memory cap, per-request timeout).

### Interface

JavaScript eval functions use the **`rpc` dispatcher** (subprocess mode) rather than the WASM dispatcher ABI (`alloc`/`evaluate`). The runner speaks LSP-framed JSON-RPC 2.0 over stdin/stdout — the same protocol shimmy uses for all subprocess workers.

```
FUNCTION_INTERFACE=rpc
FUNCTION_RPC_TRANSPORT=stdio
FUNCTION_COMMAND="wazero run runner.wasm"
```

### Architecture

```
eval.js (user eval function)
    ↓  build-runner.sh embeds source into runner.js
runner.js (LSP-framed JSON-RPC loop)
    ↓  javy build -J javy-stream-io=y
runner.wasm  (QuickJS embedded, wasm32-wasi)
    ↓  wazero run (one subprocess per pool slot)
shimmy rpc dispatcher
```

One `wazero run runner.wasm` subprocess is spawned per pool slot. The subprocess stays alive and handles requests sequentially through its stdin/stdout pipe. shimmy manages the pool and routes requests.

### State isolation

Each request calls `evaluationFunction()` inside a fresh `Function()` scope:

```js
var wrapper = new Function(
    "__response__", "__answer__", "__params__",
    EVAL_SOURCE + "\nreturn evaluationFunction(__response__, __answer__, __params__);"
);
```

This gives per-request isolation analogous to Python's `exec(source, {})`: any variable mutations inside the user's code are scoped to that call and cannot leak between requests.

### Eval function contract

```js
// eval.js — must define evaluationFunction
function evaluationFunction(response, answer, params) {
    // response: string | number
    // answer:   string | number
    // params:   object (optional extra parameters)

    return {
        is_correct: true,           // required bool
        feedback:   "Correct!",     // required string
        // any additional fields returned verbatim to the HTTP caller
    };
}
```

### Build command

```bash
# 1. Install javy (arm64-linux shown; pick the right binary for your platform)
curl -sL https://github.com/bytecodealliance/javy/releases/latest/download/javy-arm-linux-v8.1.1.gz \
    | gunzip > /usr/local/bin/javy && chmod +x /usr/local/bin/javy

# 2. Build: embed eval.js into runner.js and compile with javy
cd examples/eval-js
JAVY=/usr/local/bin/javy ./build-runner.sh eval.js runner.wasm
```

The build script JSON-encodes the eval source, patches `runner.js`, and compiles with `javy build -J javy-stream-io=y`.

### Running

```bash
FUNCTION_INTERFACE=rpc \
FUNCTION_RPC_TRANSPORT=stdio \
FUNCTION_COMMAND="wazero run $(pwd)/examples/eval-js/runner.wasm" \
PORT=8080 \
  ./shimmy serve
```

### Comparison with Go eval functions

| Aspect | Go (`wasm` dispatcher) | JavaScript (`rpc` dispatcher) |
|--------|----------------------|------------------------------|
| ABI | `alloc`/`evaluate` exports | LSP-framed JSON-RPC 2.0 |
| Isolation | Linear-memory snapshot/restore | JS `Function()` scope per request |
| Concurrency | N independent wazero instances | N subprocess instances (one per pool slot) |
| State | Guaranteed clean (memcpy restore) | Clean via scoped Function() |
| Warm-start | Yes (module compiled once) | Yes (QuickJS started once per slot) |

The complete example is in `examples/eval-js/`.
