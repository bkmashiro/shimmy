# Snapshot and Restore

## Why Snapshot/Restore

WASM module instantiation is cheap for small modules (a few milliseconds for
a compiled Go or Rust binary) but expensive for CPython-WASI: `python.wasm` is
~242 MB and its initialisation path — linking the stdlib, importing site
packages, running CPython's own startup — takes 7-8 seconds per instance.
Re-instantiating on every request would make per-request latency dominated by
startup cost.

Instead, shimmy-wasm instantiates each module **once** per pool slot, takes a
snapshot of its linear memory immediately after initialisation, and restores
that snapshot after every request. The module instance stays alive; only its
heap is rolled back. This gives warm-start semantics without re-compilation.

The isolation guarantee: because linear memory is fully restored before the
next request begins, no heap data written by request N can be read by request
N+1.

WASM mutable globals (e.g., `__stack_pointer`) are **not** snapshotted. This
is safe for all well-formed compiled modules (Go, Rust, C, Zig, etc.) because
function prologues unconditionally initialise `__stack_pointer` before using
the stack. A handcrafted `.wasm` module that stores persistent state in mutable
globals would survive across requests; this is documented as a known limitation
and is not a realistic deployment scenario.

## The `SnapshotStrategy` Interface

```go
type SnapshotStrategy interface {
    // Take captures the current state of WASM linear memory.
    // Called once after module initialisation.
    Take(mem api.Memory) error

    // Restore writes the captured snapshot back into WASM linear memory.
    // Called after every request so the next request sees a clean state.
    Restore(mem api.Memory) error

    // Close releases any OS-level resources (uffd fds, mmap regions).
    // Safe to call on a zero-value or uninitialised strategy.
    Close() error
}
```

Strategy selection happens in `wasmSupervisor.selectStrategy` (or
`ReactorPythonRunner.newSnapshotStrategy`) after the module is instantiated and
memory is available. The mode is driven by `FUNCTION_WASM_SNAPSHOT_MODE`.

## Strategy Details

### 1. FullMemcpy (default)

**Source:** `internal/execution/wasm/snapshot.go`  
**Mode value:** `"memcpy"` or `""`  
**Availability:** Always; no OS prerequisites.

`Take` calls `mem.Read(0, mem.Size())`, makes an owned copy of the returned
slice, and stores it. `Restore` calls `mem.Write(0, snapshot)`. Both
operations touch every byte of linear memory regardless of what the guest
actually wrote.

**Cost:** O(N) where N = total linear memory size.

**Typical numbers** (AMD Ryzen 7 5800H, measured with `BenchmarkSnapshotRestore_FullMemcpy_*`):

| Memory size | Restore time |
|---|---|
| 64 KB (echo.wasm fixture) | ~15 µs |
| 3 MB (compiled Go module) | ~78 µs |
| 14 MB (python-reactor.wasm) | ~350-450 µs |

These numbers represent the `mem.Write` path through wazero; actual
per-request overhead also includes `alloc`, `evaluate`, and response
unmarshalling.

**When to use it:** Always a safe choice. Use it unless you have profiled
snapshot/restore as a bottleneck at your module size.

### 2. SoftDirty

**Source:** `internal/execution/wasm/snapshot_soft_dirty_linux.go`  
**Build tag:** `linux`  
**Mode value:** `"soft-dirty"`  
**Availability:** Linux >= 3.18 with `CONFIG_CHECKPOINT_RESTORE=y` (present in
most distros). Requires write access to `/proc/self/clear_refs`.

**Mechanism:** Uses the Linux soft-dirty PTE mechanism documented in
`Documentation/vm/soft-dirty.txt`. After `Take`, the strategy writes `"4"` to
`/proc/self/clear_refs` to atomically reset the soft-dirty bit on every PTE in
the process. When the WASM guest writes to a page the kernel automatically sets
its soft-dirty bit. On `Restore`, `/proc/self/pagemap` is read in a single
bulk `ReadAt` (one syscall covering the entire WASM memory region's VPN range)
to identify which pages have their soft-dirty bit set; only those pages are
copied back from the snapshot.

**Cost:**
- `Take`: O(N) memcpy + O(1) `clear_refs` write.
- `Restore`: O(N/pagesize) pagemap read + O(D) page copies (D = dirty pages).

**Process-wide limitation:** `clear_refs` resets soft-dirty bits for the
**entire process**, not just the WASM linear memory region. A second WASM
instance in the same process would have its own dirty bits reset by the first
instance's `Take`, making dirty-page tracking unreliable. For this reason,
shimmy-wasm rejects `SnapshotMode = "soft-dirty"` with `MaxInstances > 1`:

```
wasm: snapshot mode "soft-dirty" is not safe with MaxInstances=4 > 1
(process-wide dirty bits cannot be attributed to individual instances);
use "memcpy" or "uffd" instead
```

**When to use it:** Only with `FUNCTION_MAX_PROCS=1`. Useful when you need
faster restore than memcpy and cannot use uffd (Docker default seccomp).

### 3. Mprotect

**Source:** `internal/execution/wasm/snapshot_mprotect_linux.go`  
**Build tags:** `linux && cgo`  
**Mode value:** `"mprotect"`  
**Availability:** Linux with CGO enabled. Does not require special privileges
or seccomp exceptions.

**Mechanism:** On `Take`, the strategy installs a global `SIGSEGV` handler via
`sigaction(SA_SIGINFO | SA_ONSTACK | SA_NODEFER)` and write-protects the
entire WASM linear memory region with `mprotect(PROT_READ)`. When the guest
writes to a protected page, the kernel raises `SIGSEGV`; the C signal handler
records the page index in a C-allocated atomic bitmap (`_Atomic uint64_t`
words, one bit per page), then lifts write-protection on that specific page
via `mprotect(PROT_READ|PROT_WRITE)` so the faulting store can retry. On
`Restore`, the strategy reads the bitmap, copies back only the dirty pages,
re-protects the entire region, and clears the bitmap.

The dirty-page bitmap lives in C-allocated memory and is updated exclusively
via C atomic RMW operations inside the signal handler. The Go runtime is never
entered from signal context, preventing the `morestack on g0` crash that occurs
when Go code is called from a signal handler on the alternate signal stack.

**Process-wide limitation:** The C globals `g_base` and `g_size` describe a
single memory region. A second `MprotectStrategy` instance would overwrite them,
corrupting dirty-page tracking for both. The same `MaxInstances > 1` guard
applies as for soft-dirty.

**When to use it:** Only with `FUNCTION_MAX_PROCS=1`, and only when CGO is
available and you need faster restore than memcpy.

**Caution:** The bench-results.txt in the repository shows a `morestack on g0`
fatal error from an older version of the mprotect implementation. This was
caused by attempting to invoke a Go callback from the C signal handler; the
current implementation uses only C-level atomics in the handler and does not
exhibit this crash.

### 4. Uffd (userfaultfd)

**Source:** `internal/execution/wasm/snapshot_uffd_linux.go`  
**Build tag:** `linux`  
**Mode value:** `"uffd"`  
**Availability:** Linux kernel with `UFFD_FEATURE_PAGEFAULT_FLAG_WP`. Requires
that the `userfaultfd(2)` syscall is not blocked by seccomp (Docker's default
profile blocks it; AWS Lambda's does not on newer AMIs).

The file contains **two** implementations:

**`UffdProbeStrategy`** — validates that uffd+WP works end-to-end (opens a
uffd fd, negotiates the API, mmap's a test page, registers it in WP mode, arms
write-protection). It then delegates `Take`/`Restore` to `FullMemcpyStrategy`.
Used by the probe workflow and as a capability check.

**`UffdStrategy`** — a full dirty-page tracking implementation. On
construction it registers the WASM linear memory region directly with uffd in
`UFFDIO_REGISTER_MODE_WP` mode by extracting the backing pointer from
`mem.Read(0, size)` using `unsafe.SliceData`. A background goroutine
(`faultLoop`) blocks reading `uffd_msg` events from the fd; for each
`UFFD_EVENT_PAGEFAULT` with `UFFD_PAGEFAULT_FLAG_WP` it marks the faulting
page as dirty in a `[]bool` slice and disarms WP on that page via
`UFFDIO_WRITEPROTECT` so the faulting thread can proceed. `Restore` copies
back only the dirty pages and re-arms WP on them; no process-wide state is
touched.

Because `UffdStrategy` tracks dirty pages per-instance (each has its own uffd
fd and `dirty []bool`), it is safe for `MaxInstances > 1`.

**Current limitation:** The `UffdProbeStrategy` comment (retained for
accuracy) says it falls back to memcpy because wazero doesn't expose the raw
backing address. `UffdStrategy` solves this by using `unsafe.SliceData` on the
slice returned by `mem.Read`; this relies on wazero's implementation detail
that `api.Memory.Read` returns a slice directly backed by the mmap region
(which is true for wazero's Linux compiler). There is a `MAP_SHARED`
constraint mentioned in probing code: the uffd registration of the memory
region requires that the mapping not be `MAP_SHARED`, which wazero's linear
memory satisfies (it uses `MAP_PRIVATE | MAP_ANONYMOUS`). The probe workflow
(`.github/workflows/uffd-probe.yml`) validates this on target kernels.

**When to use it:** The recommended strategy for production when multiple pool
slots are needed and the host kernel + seccomp policy permit userfaultfd.

## Strategy Selection Logic

`wasmSupervisor.selectStrategy` (Linux build):

```go
switch mode {
case "soft-dirty":
    sd, err := NewSoftDirtyStrategy(mem)
    if err != nil { return NewFullMemcpyStrategy() }  // graceful fallback
    return sd
case "mprotect":
    mp, err := NewMprotectStrategy(mem)
    if err != nil { return NewFullMemcpyStrategy() }
    return mp
case "uffd":
    us, err := NewUffdStrategy(mem)
    if err != nil { return NewFullMemcpyStrategy() }
    return us
default: // "memcpy" or ""
    return NewFullMemcpyStrategy()
}
```

On non-Linux platforms (`snapshot_stub.go`) `selectStrategy` always returns
`NewFullMemcpyStrategy()` regardless of `SnapshotMode`.

The multi-instance guard fires before pool construction, not inside
`selectStrategy`, so the error is returned from `Dispatcher.Start` during
startup rather than silently producing corrupt snapshots at runtime:

```go
if maxInstances > 1 {
    switch d.cfg.SnapshotMode {
    case "soft-dirty", "mprotect":
        return fmt.Errorf("wasm: snapshot mode %q is not safe with MaxInstances=%d > 1 ...", ...)
    }
}
```

`"uffd"` is explicitly excluded from this guard because `UffdStrategy` is
safe for concurrent instances.

## What Is NOT Snapshotted

Only WASM **linear memory** is snapshotted. The following are not restored:

- **WASM mutable globals** — including `__stack_pointer` (global 0 in most
  toolchains). This is safe because every function prologue that uses the stack
  saves and restores `__stack_pointer` from the call's own stack frame. The
  value of `__stack_pointer` at the moment of `Take` is the top-of-stack value
  immediately after module initialisation, before any function has been called.
  After `evaluate` returns, `__stack_pointer` is already back to that same
  value (it is restored by the function's epilogue before returning). The
  snapshot does not need to cover it.
- **WASM tables** — function-pointer tables are typically immutable after
  instantiation for compiled modules.
- **Host state** — file descriptors, clocks, WASI state managed by the host.
  The WASM module's linear memory does not hold these; the WASI host functions
  are stateless from the module's perspective.

## Performance Summary

All numbers are approximate, measured on AMD Ryzen 7 5800H with wazero on
Linux.

| Strategy | Memory | Take cost | Restore cost (0 dirty) | Restore cost (10% dirty) |
|---|---|---|---|---|
| FullMemcpy | 64 KB | ~6 µs | ~15 µs | ~15 µs |
| FullMemcpy | 3 MB | ~200 µs | ~78 µs | ~78 µs |
| SoftDirty | 14 MB | ~350 µs | low (pagemap scan only) | O(dirty pages) |
| Mprotect | 14 MB | ~350 µs | low (re-mprotect only) | O(dirty pages) |
| Uffd | 14 MB | ~350 µs | zero (no dirty pages) | O(dirty pages) |

SoftDirty and Mprotect have a bulk `ReadAt` / re-`mprotect` overhead on every
`Restore` call even when zero pages are dirty (the overhead is roughly one
syscall). Uffd `Restore` with zero dirty pages is nearly free (just clears the
`dirty []bool` slice under a mutex).
