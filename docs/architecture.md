# Architecture

## Overview

shimmy-wasm is an HTTP server that exposes a generic evaluation-function API. It
compiles user-supplied code (WASM modules or Python scripts) once, pools a
configurable number of instances, and dispatches each incoming request to an
idle instance with per-request memory isolation.

## High-Level Stack

```
HTTP request
     │
     ▼
┌─────────────────────┐
│   CommandHandler    │  net/http handler; auth check, body read
│  (handler/handler)  │
└────────┬────────────┘
         │ runtime.Request
         ▼
┌─────────────────────┐
│   RuntimeHandler    │  decodes JSON, validates schema, routes by path
│  (runtime/handler)  │  /eval  /preview  /healthcheck
└────────┬────────────┘
         │ EvaluationRequest{Command, Data}
         ▼
┌─────────────────────┐
│  EvaluationRuntime  │  thin facade over Dispatcher
│  (runtime/runtime)  │
└────────┬────────────┘
         │ Dispatcher.Send(ctx, method, params)
         ▼
┌─────────────────────────────────────────────────────────────┐
│                       Dispatcher                            │
│  (selected by FUNCTION_INTERFACE at startup)                │
│                                                             │
│  wasm             → wasm.Dispatcher                         │
│  python-wasm      → wasm.PythonDispatcher                   │
│  reactor-python   → wasm.ReactorPythonDispatcher            │
│  rpc              → dispatcher.DedicatedDispatcher          │
│  <default>        → dispatcher.PooledDispatcher (subprocess)│
└────────┬────────────────────────────────────────────────────┘
         │ pool acquire
         ▼
┌─────────────────────┐
│      Pool           │  buffered Go channel: make(chan *supervisor, N)
│   (chan slot)       │  N = FUNCTION_MAX_PROCS (default: runtime.NumCPU())
└────────┬────────────┘
         │
         ▼
┌─────────────────────┐
│  wasmSupervisor /   │  one live WASM module instance per slot
│  ResidentPython /   │  snapshot restored before each request
│  ReactorPython      │
└────────┬────────────┘
         │
         ▼
┌─────────────────────┐
│    WASM Module      │  wazero api.Module
│  (linear memory)    │  alloc() → evaluate() → response
└─────────────────────┘
```

## The Three WASM Backends

`FUNCTION_INTERFACE` selects which dispatcher is constructed in
`internal/execution/dispatcher.go`:

| `FUNCTION_INTERFACE` | Dispatcher type | Guest binary |
|---|---|---|
| `wasm` | `wasm.Dispatcher` | any wasm32-wasip1 module (`FUNCTION_COMMAND` or `FUNCTION_WASM_MODULE`) |
| `python-wasm` | `wasm.PythonDispatcher` | `python.wasm` (CPython-WASI command mode) |
| `reactor-python` | `wasm.ReactorPythonDispatcher` | `python-reactor.wasm` (reactor mode) |
| `rpc` | `dispatcher.DedicatedDispatcher` | any subprocess speaking JSON over stdio/IPC/HTTP |
| _(anything else)_ | `dispatcher.PooledDispatcher` | subprocess via file or RPC transport |

For the subprocess backends (`rpc`, file) shimmy forks a new process per
request or per pool slot, communicating over the configured transport. For the
three WASM backends the module lives in-process inside wazero.

## How `FUNCTION_INTERFACE` Is Resolved

`internal/execution/dispatcher.go` `NewDispatcher` reads
`params.Config.Supervisor.IO.Interface` (an `IOInterface` string) and switches
on it:

```go
case supervisor.WasmIO:           // "wasm"
    d := wasm.NewDispatcher(...)
case supervisor.PythonWasmIO:     // "python-wasm"
    d := wasm.NewPythonDispatcher(...)
case supervisor.ReactorPythonIO:  // "reactor-python"
    d := wasm.NewReactorPythonDispatcher(...)
case supervisor.RpcIO:            // "rpc"
    return dispatcher.NewDedicatedDispatcher(...)
default:
    return dispatcher.NewPooledDispatcher(...)
```

The `IOInterface` value is derived from `FUNCTION_INTERFACE` environment
variable by the koanf config chain (via the `conf:"interface"` struct tag on
`supervisor.IOConfig`).

## The Pool Model

All three WASM dispatchers use the same pattern: a buffered Go channel of
pre-initialised instances.

```go
pool := make(chan *wasmSupervisor, maxInstances)
```

**Acquire on send:**

```go
select {
case sv = <-d.pool:         // got an idle instance
case <-ctx.Done():          // context expired while waiting
    return nil, ctx.Err()
}
```

**Return or replace after send:**

```go
if sv.healthy {
    d.pool <- sv            // return healthy instance to pool
} else {
    go sv.Shutdown(...)     // discard broken instance
    go d.spawnOne()         // replace it asynchronously
}
```

If `spawnOne` fails (e.g., OOM during re-instantiation) the pool shrinks
permanently; it does not retry. The channel is buffered to exactly `N` slots so
that `cap(pool)` always equals the intended concurrency level, and
`drainPool` can use a simple counting loop on `cap(d.pool)` during shutdown.

**Pool sizing:** defaults to `runtime.NumCPU()` for general WASM and the
resident Python dispatcher (capped at 8); for the reactor dispatcher it is
additionally capped at 4 because each instance holds ~100 MB of CPython heap
in memory.

## Request Lifecycle (Numbered Steps)

The following describes a request through the `wasm.Dispatcher` path. The
Python dispatcher paths differ only in steps 5-6.

1. HTTP server receives `POST /eval` with a JSON body.
2. `CommandHandler.ServeHTTP` reads the body, checks the `api-key` header if
   `AUTH_KEY` is set.
3. `RuntimeHandler.Handle` unmarshals the body against the JSON schema for the
   `/eval` endpoint, extracts `response`, `answer`, `params` fields, builds an
   `EvaluationRequest`.
4. `EvaluationRuntime.Handle` calls `Dispatcher.Send(ctx, "eval", data)`.
5. `wasm.Dispatcher.Send` blocks on `<-d.pool` until an idle `wasmSupervisor`
   is available or the context deadline fires.
6. `wasmSupervisor.Send` acquires its mutex and calls `wasmAdapter.send`.
7. `wasmAdapter.send`:
   a. Marshals `{"method":"eval","params":{...}}` to JSON.
   b. Calls `alloc(len)` on the guest; validates that the returned pointer is
      non-zero.
   c. Writes the JSON bytes into guest linear memory at the returned pointer.
   d. Calls `evaluate(ptr, len)` on the guest.
   e. Reads 4 bytes at the returned pointer as a little-endian `uint32` length
      `L`.
   f. Validates `resPtr + 4 + L <= mem.Size()`.
   g. Reads `L` bytes and unmarshals them as the response JSON.
8. `wasmSupervisor.Send` calls `restoreSnapshot()` via the active
   `SnapshotStrategy`. If restore fails, `sv.healthy` is set to `false`.
9. The supervisor is returned to the pool (healthy) or replaced (unhealthy).
10. The response map propagates back through `EvaluationRuntime` →
    `RuntimeHandler`, which marshals it as the HTTP response body with
    appropriate status code and schema validation.
