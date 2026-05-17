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
- `examples/eval-js/` — **JavaScript eval functions via javy/QuickJS** (new):
  - `eval.js` — user eval function contract (`evaluationFunction(response, answer, params)`)
  - `runner.js` — LSP-framed JSON-RPC 2.0 loop; embeds user source at build time;
    state isolation via fresh `Function()` scope per request
  - `build-runner.sh` — embeds eval.js into runner.js, compiles with `javy build -J javy-stream-io=y`
  - `runner.wasm` — pre-built artifact (QuickJS wasm32-wasi, ~1.3 MB)
  - uses existing `rpc` dispatcher + subprocess mode (`FUNCTION_INTERFACE=rpc`,
    `FUNCTION_COMMAND="wazero run runner.wasm"`); one wazero subprocess per pool slot
  - test: `internal/execution/wasm/dispatcher_javy_test.go` — `TestDispatcher_Send_JS`
    spawns the wazero subprocess and verifies end-to-end JSON-RPC framing and eval results

### CI — userfaultfd Probe
- `.github/workflows/uffd-probe.yml` — runs on Ubuntu VM (not container) to test whether
  `userfaultfd(2)` fd creation and `UFFDIO_REGISTER_MODE_WP` are available
- `internal/execution/wasm/uffd_probe_test.go` — Go test with two probes:
  fd creation (`TestUserfaultfdProbe_FdOnly`) and full WP registration
  (`TestUserfaultfdProbe`)

### SnapshotStrategy interface + uffd dirty-page strategy (experimental, behind `FUNCTION_WASM_USE_UFFD=true`)
- `internal/execution/wasm/snapshot.go` — `SnapshotStrategy` interface +
  `FullMemcpyStrategy` (current baseline, always available)
- `internal/execution/wasm/snapshot_uffd_linux.go` — `UffdProbeStrategy`
  (probe/scaffold) + **`UffdStrategy`** (full dirty-page tracking, experimental):
  - `NewUffdStrategy(mem api.Memory)` extracts the raw backing pointer via
    `unsafe.SliceData(mem.Read(0, size))`, registers the WASM linear memory with
    `UFFDIO_REGISTER_MODE_WP`, arms write-protection, and starts a background
    `faultLoop` goroutine that reads `uffd_msg` structs, records dirty pages,
    and unblocks faulting threads by disarming WP per-page.
  - `Take`: full memcpy into snapshot buffer + re-arm WP + clear dirty bitset
  - `Restore`: copy back only dirty pages + re-arm WP per dirty page
  - `Close`: disarm WP, close uffd fd (stops `faultLoop`), wait for goroutine exit
- `internal/execution/wasm/supervisor_linux.go` — `selectStrategy()`: attempts
  `UffdStrategy` when `useUffd=true`; falls back to `FullMemcpyStrategy` on error
- `internal/execution/wasm/supervisor_stub.go` — non-Linux stub always returns
  `FullMemcpyStrategy`
- `internal/execution/wasm/snapshot_stub.go` — non-Linux stub returning
  `FullMemcpyStrategy` for `NewSnapshotStrategy()`
- `internal/execution/wasm/config.go` — `UseUffd bool` field; env var
  `FUNCTION_WASM_USE_UFFD=true` enables it
- `wasmSupervisor` updated: `useUffd bool` field; strategy selected in `Start()`
  after module instantiation (when `api.Memory` is available)
- `internal/execution/wasm/snapshot_uffd_linux_test.go` — five tests:
  `TestUffdStrategy_FallbackOnUnavailable`, `TestUffdStrategy_EndToEnd`,
  `TestUffdProbeStrategy_NewAndClose`, `TestUffdStrategy_DirtyPageTracking`
  (verifies only written pages are restored via real wazero module),
  `TestUffdStrategy_SupervisorIntegration` (full supervisor round-trip with
  `UseUffd=true`); uffd tests skip in Docker/seccomp
- `bench_test.go` — `BenchmarkSnapshotRestore_Uffd_3MB`: measures dirty-page
  restore at 3MB with ~10% dirty pages; skipped if uffd unavailable

**Implementation note:** `mem.Read(0, size)` on wazero/linux returns a slice
backed directly by the `mmap(MAP_ANONYMOUS)` region for WASM linear memory.
`unsafe.SliceData` extracts the base address without copying. Since
`WithMemoryLimitPages` prevents memory growth, the registration stays valid
for the module lifetime. This uses an unofficial path (not part of wazero's
public API) but works in practice on current wazero versions.

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

- `UffdStrategy` (dirty-page restore) is behind `FUNCTION_WASM_USE_UFFD=true`
  and uses an unofficial wazero internal (raw slice pointer from `mem.Read`).
  Works on current wazero/linux but could break if wazero changes its memory
  allocation strategy. Not tested in Docker (seccomp blocks uffd).

## Pending Work

- [ ] Update interim report with benchmark data and architecture
- [ ] Fix echo.wasm fixture (allocator state in linear memory, not global)
- [x] userfaultfd dirty-page restore benchmark — `BenchmarkSnapshotRestore_FullMemcpy_3MB`
      measures restore cost at 3MB (54µs on this machine)
- [x] userfaultfd full dirty-page restore — `UffdStrategy` implemented (experimental,
      `FUNCTION_WASM_USE_UFFD=true`); uses `unsafe.SliceData` on wazero linear memory
- [ ] numpy integration test (mount wasi-wheels output into Python sandbox)
- [x] Pyodide/Node.js fallback path (scipy route) — subprocess mode, existing shimmy
      (state isolation via fresh namespace `exec(source, {})` per request; no memory snapshot)
- [x] JavaScript eval functions via javy/QuickJS — `examples/eval-js/`; subprocess mode
      (`FUNCTION_INTERFACE=rpc`, `FUNCTION_COMMAND="wazero run runner.wasm"`);
      state isolation via `Function()` scope per request; `TestDispatcher_Send_JS` passes
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
