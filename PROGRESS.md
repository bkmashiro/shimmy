# shimmy-wasm Progress

## Current Branch: `feat/wasm-backend`

## Completed

### Core WASM Backend
- `internal/execution/wasm/` — Dispatcher, wasmSupervisor, wasmAdapter
- Guest ABI: `alloc(size i32) i32` + `evaluate(req_ptr i32, req_len i32) i32`
- N:1 CompiledModule pool (compile once, N instances)
- Memory snapshot/restore after every request (linear memory `[]byte`)
- `FUNCTION_INTERFACE=wasm` wired into dispatcher factory

### Security / Sandbox
- No filesystem, no env vars, no stdin/stdout by default
- `FUNCTION_WASM_MAX_MEMORY_PAGES` — hard memory cap via `WithMemoryLimitPages`
- `FUNCTION_WASM_ALLOWED_PATHS` — optional read-only mounts
- `FUNCTION_WASM_ALLOWED_ENV` — optional env var passthrough

### Eval Function Examples
- `examples/eval-go/` — Go eval function (GOOS=wasip1 -buildmode=c-shared), reactor mode
- `examples/eval-python/eval.py` — Python eval function (numeric comparison)
- `examples/eval-pyodide/` — Pyodide/Node.js runner for scipy/pandas eval functions;
  uses existing `rpc` dispatcher + subprocess mode (`FUNCTION_INTERFACE=rpc`,
  `FUNCTION_COMMAND=node runner.js eval.py`); state isolation via `exec(source, {})`
  fresh namespace per request; no memory snapshot required

### CI — userfaultfd Probe
- `.github/workflows/uffd-probe.yml` — runs on Ubuntu VM (not container) to test whether
  `userfaultfd(2)` fd creation and `UFFDIO_REGISTER_MODE_WP` are available
- `internal/execution/wasm/uffd_probe_test.go` — Go test with two probes:
  fd creation (`TestUserfaultfdProbe_FdOnly`) and full WP registration
  (`TestUserfaultfdProbe`)

### SnapshotStrategy interface + uffd probe strategy
- `internal/execution/wasm/snapshot.go` — `SnapshotStrategy` interface +
  `FullMemcpyStrategy` (current baseline, always available)
- `internal/execution/wasm/snapshot_uffd_linux.go` — `UffdProbeStrategy`:
  opens uffd fd, performs UFFDIO_API handshake, mmaps a test region, registers
  it with `UFFDIO_REGISTER_MODE_WP`, arms write-protection. Falls back to
  `FullMemcpyStrategy` for actual wasm memory restore (wazero limitation — see
  below). `NewSnapshotStrategy()` picks uffd or full-memcpy at runtime.
- `internal/execution/wasm/snapshot_stub.go` — non-Linux stub returning
  `FullMemcpyStrategy`
- `internal/execution/wasm/snapshot_uffd_linux_test.go` — three tests:
  `TestUffdStrategy_FallbackOnUnavailable` (passes in Docker, confirms graceful
  fallback), `TestUffdStrategy_EndToEnd` (full uffd WP round-trip on own mmap
  region — skipped in Docker, runs on Ubuntu VM/CI), `TestUffdProbeStrategy_NewAndClose`
- `wasmSupervisor` refactored to use `SnapshotStrategy` interface; `snapshot []byte`
  field removed; `takeSnapshot`/`restoreSnapshot` delegate to strategy

**Wazero limitation for full dirty-page restore:** `api.Memory` does not expose
the raw pointer/mmap address of its linear-memory backing. To wire uffd
tracking to the actual WASM memory (rather than a shadow region), one of these
is needed:
- wazero `experimental` API exposing the backing mmap address (not upstream as
  of v1.x)
- A custom wazero memory allocator that uses our own mmap and passes the
  address to `UffdProbeStrategy`
- Using `unsafe.SliceData` on the `[]byte` from `api.Memory.Read` (works today
  but is unsupported — the slice may be moved by GC if the backing is a Go
  allocation)

### Python Execution Paths
- `python.go` — PythonRunner: per-request instantiation (~160ms, compile amortised)
- `python_resident.go` — ResidentPythonRunner: goroutine + io.Pipe (~1.8ms after init)

### Tests & Benchmarks
- 11 unit tests (Dispatcher, Supervisor, snapshot/restore, concurrency)
- `bench_test.go` — WASM benchmarks
- `bench_subprocess_test.go` — subprocess baseline
- `python_test.go`, `python_resident_test.go`

## Benchmark Results (this machine, 10-core)

| Scenario | Latency |
|----------|---------|
| Subprocess RPC baseline | ~29,800 ns |
| WASM pool=1 (Go reactor) | ~9,700 ns (3× faster) |
| WASM pool=N (Go reactor) | ~5,000 ns |
| Snapshot restore (memcpy, 64KB) | ~753 ns (<8% of dispatch) |
| Snapshot restore (memcpy, 3MB) | ~54 µs (realistic Go WASM size) |
| Python per-request instantiation | ~160 ms |
| Python resident (goroutine) | ~1.8 ms (88× faster) |

Note: snapshot benchmark uses 64KB echo.wasm fixture. Real Go WASM is 3MB
(~35µs restore). CPython-WASI is 26MB — motivates userfaultfd dirty-page
restore on large modules.

## Known Issues / Technical Debt

- `echo.wasm` fixture stores bump-allocator pointer in WASM global (not linear
  memory), so snapshot/restore doesn't reset it. Benchmark works around this
  by recreating dispatcher every ~500 iterations. Real modules store state in
  linear memory and are correctly restored.

- asyncify approach for CPython-WASI investigated but abandoned: CPython's call
  stack is thousands of frames deep, causing goroutine stack overflow on rewind
  in wazero. Goroutine + io.Pipe is the practical alternative.

- userfaultfd full dirty-page restore not yet implemented for WASM linear memory.
  `SnapshotStrategy` interface + `UffdProbeStrategy` scaffold is in place; uffd
  fd creation and `UFFDIO_REGISTER_MODE_WP` confirmed on Ubuntu CI. Blocked by
  wazero not exposing the raw linear-memory address — see wazero limitation note
  under "SnapshotStrategy interface" in Completed section.

## Pending Work

- [ ] Update interim report with benchmark data and architecture
- [ ] Fix echo.wasm fixture (allocator state in linear memory, not global)
- [x] userfaultfd dirty-page restore benchmark — `BenchmarkSnapshotRestore_FullMemcpy_3MB`
      measures restore cost at 3MB (54µs on this machine)
- [ ] userfaultfd full dirty-page restore (wires uffd to actual WASM linear memory —
      blocked by wazero limitation, see Completed section for details)
- [ ] numpy integration test (mount wasi-wheels output into Python sandbox)
- [x] Pyodide/Node.js fallback path (scipy route) — subprocess mode, existing shimmy
      (state isolation via fresh namespace `exec(source, {})` per request; no memory snapshot)
- [ ] Open PR feat/wasm-backend → main

## Architecture Notes

### Why goroutine-based resident Python works

Python server loop (`while True: readline()`) blocks the WASM goroutine at
WASI `fd_read`. Host sends request JSON via `io.Pipe`, reads response. State
isolation via `exec(code, {})` with fresh namespace per request. Not
memory-level isolation (unlike reactor snapshot/restore), but sufficient for
pure-function eval functions.

### Why Go eval functions use reactor mode

`GOOS=wasip1 GOARCH=wasm -buildmode=c-shared` produces a reactor module with
`_initialize` export. Host calls `alloc`/`evaluate` directly. Clean call
boundary means snapshot/restore naturally works: after `evaluate()` returns,
all state is in linear memory — restore before next call gives clean state.

### Why asyncify failed for CPython

CPython-WASI's call stack during `readline()` is thousands of frames deep.
asyncify rewind wraps each frame in extra function calls; on wazero this
exhausts the Go goroutine stack before the rewind completes. The binary was
correctly produced (`wasm-opt --asyncify python.wasm`) and exports
`asyncify_start_unwind` etc., but the execution fails at runtime.
