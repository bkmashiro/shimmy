# Security

## Threat Model

shimmy-wasm is designed to execute untrusted user-submitted code — evaluation
functions written by students, instructors, or third-party content authors —
at Lambda-scale concurrency. The trust boundary is:

- **Trusted:** the shimmy-wasm binary, the host OS, the Go runtime, wazero,
  the WASI host implementation.
- **Untrusted:** `eval.py` / `eval.wasm` submitted by the user. These may
  attempt to read host secrets, exfiltrate data, exhaust resources, or
  interfere with other tenants.

The primary containment mechanism is WebAssembly isolation via wazero. On AWS
Lambda, traditional Linux sandboxing techniques (kernel namespaces, seccomp
filter installation, ptrace, `setuid`) are not available because the Lambda
runtime already runs inside Firecracker microVMs with a restricted seccomp
profile that blocks the syscalls needed to set up nested namespaces.

## wazero Sandbox Guarantees

wazero provides strong isolation by design:

**Memory isolation:** Each WASM module instance has its own linear memory
region. A module cannot address host memory outside its linear memory — all
memory accesses are bounds-checked by the wazero interpreter and the compiled
native code. A guest that attempts to read beyond `mem.Size()` gets a WASM
trap, not a host memory access.

**No filesystem by default:** `ModuleConfig` is constructed without
`WithFSConfig` paths unless `FUNCTION_WASM_ALLOWED_PATHS` is set. Absent
preopened directories, all WASI `path_*` and `fd_*` calls on non-WASI-provided
file descriptors fail with `EBADF` or `ENOENT`. The adversarial tests
(`examples/adversarial/`) verify that `/proc/self/environ`, `/etc/passwd`, and
`/tmp` writes are blocked.

**No environment variables by default:** `ModuleConfig` exposes no env vars
unless explicitly whitelisted via `FUNCTION_WASM_ALLOWED_ENV`. A guest calling
`environ_get` receives an empty list.

**No network:** wazero's WASI preview1 implementation does not provide
`sock_*` host functions. A guest that calls `net.Dial` gets `ENOSYS`.

**No subprocess execution:** There are no `exec`-family WASI imports. A guest
that calls `os/exec.Command` or `syscall.Exec` gets `ENOSYS` or a trap.

## Per-Request Memory Reset

After every `evaluate` call, the dispatcher restores the pre-request memory
snapshot before returning the supervisor to the pool. This means:

- Any secret embedded in a response by the eval function (e.g., a student's
  heap allocation containing their previous answer) is overwritten before the
  next request runs.
- Stack residue, heap residue, and any data written by the guest during the
  request is gone by the time the next request starts.
- The guarantee holds even if the guest's `evaluate` function returns normally;
  snapshot restore happens unconditionally in the `defer` path of
  `wasmSupervisor.Send`.

If restore fails (e.g., the strategy's backing memory is corrupted), the
supervisor is marked unhealthy and discarded rather than returned to the pool:

```go
if restoreErr := s.restoreSnapshot(); restoreErr != nil {
    s.healthy = false
    // ...
}
```

## Resource Limits

**Memory:** `FUNCTION_WASM_MAX_MEMORY_PAGES` sets the limit passed to
`wazero.NewRuntimeConfig().WithMemoryLimitPages(N)`. One page = 64 KB. The
default is 256 pages = 16 MB. A guest that calls `memory.grow` beyond this
limit receives a `nil` return from `memory.grow` (WASM spec: out-of-memory).
The adversarial `mem-bomb` test verifies this.

**CPU / time:** `FUNCTION_TIMEOUT` sets a per-request deadline. The wazero
runtime is created with `WithCloseOnContextDone(true)`, which causes wazero to
interrupt any running WASM execution when the context is cancelled or expires.
This relies on wazero's epoch interruption mechanism — the wazero compiler
inserts epoch checks at function-call boundaries. An infinite loop is
interrupted within one epoch tick (typically < 1 ms) after the context fires.
The adversarial `cpu-bomb` and `stack-bomb` tests verify this.

**Concurrency:** `FUNCTION_MAX_PROCS` limits the pool size. At most N requests
run simultaneously regardless of incoming HTTP concurrency; excess requests
block on the pool channel until a slot is available or the request context
expires.

## Host Function Surface Area

The only host functions installed into the wazero runtime are WASI preview1:

```go
wasi_snapshot_preview1.Instantiate(ctx, rt)
```

And the module config exposes only:

```go
modCfg := wazero.NewModuleConfig().
    WithName("").
    WithSysNanosleep().    // nanosleep(2) — needed by Go runtime's scheduler
    WithSysWalltime().     // wall clock — needed by time.Now()
    WithSysNanotime()      // monotonic clock — needed by time.Since()
```

No `WithFSConfig`, no `WithEnv`, no `WithStdin/Stdout/Stderr`, no `WithArgs`
by default. The WASI functions that are instantiated by
`wasi_snapshot_preview1.Instantiate` are the full preview1 set (so that
modules compiled for WASIP1 can link), but file/env/socket operations on
non-preopened descriptors return appropriate WASI errors.

For the reactor Python runner, additional host functions are registered via
`instantiateEnvModule` (the `"env"` module) to satisfy CPython's WASM import
requirements: dynamic-linking stubs, float16/complex math helpers, and RNG
stubs. These are all no-ops or thin wrappers and do not grant additional access
to host resources.

## Optional Extensions

**`FUNCTION_WASM_ALLOWED_PATHS`:** A comma-separated list of host filesystem
paths to mount into the WASM sandbox as read-only directories:

```go
for _, p := range d.cfg.AllowedPaths {
    fsCfg = fsCfg.WithReadOnlyDirMount(p, p)
}
```

The guest sees these paths at the same location as on the host (identity
mount). Only read access is permitted; write syscalls on these paths fail.
Use this to expose a read-only dataset or Python site-packages directory.

**`FUNCTION_WASM_ALLOWED_ENV`:** A comma-separated list of host environment
variable names to expose to the guest:

```go
for _, key := range d.cfg.AllowedEnv {
    if val, ok := os.LookupEnv(key); ok {
        modCfg = modCfg.WithEnv(key, val)
    }
}
```

Only variables explicitly listed are passed; `AWS_SECRET_ACCESS_KEY` and
similar secrets are invisible to the guest as long as they are not listed.

## WASM Mutable Globals

WASM mutable globals (e.g., `__stack_pointer`, global 0 in most compiled
modules) are not covered by the snapshot/restore cycle. This is intentional and
safe for all well-formed compiled toolchains:

- Compiled Go, Rust, C, Zig, and similar languages follow the convention that
  `__stack_pointer` is saved/restored by each function's prologue/epilogue.
  The value of `__stack_pointer` after `evaluate` returns is identical to its
  value before `evaluate` was called, because the call stack has fully unwound.
- The value of `__stack_pointer` captured in the snapshot is the
  post-initialisation value, which equals the value after `evaluate` returns.
  No restore is needed.

**Known limitation:** A handcrafted `.wasm` binary that stores persistent
mutable state in WASM globals (outside linear memory) would survive snapshot
restore. This is not a realistic scenario for compiled user evaluation
functions. If you are auditing a specific module, inspect its global section
with `wasm-objdump -x` or `wasm2wat` and verify that mutable globals are
only used for stack management, not persistent state.

## What the Sandbox Does NOT Protect Against

**Side-channel attacks:** A malicious guest can use timing to distinguish
correct from incorrect answers by measuring CPU cycles or response latency.
The sandbox provides no timing isolation.

**CPU starvation:** Multiple simultaneous CPU-intensive requests can starve the
Go runtime scheduler. `FUNCTION_TIMEOUT` bounds the duration of any single
request, but a steady stream of compute-heavy requests at the pool limit can
saturate CPU without triggering the memory or time limits.

**Memory pressure (below limit):** A guest that allocates up to
`FUNCTION_WASM_MAX_MEMORY_PAGES` pages on every request causes the snapshot
restore to copy the full allocation region back each time. This is a
resource-exhaustion vector that degrades throughput but does not cross the
isolation boundary.

**Cache timing via `/proc`:** The soft-dirty and mprotect snapshot strategies
write to `/proc/self/clear_refs` and read `/proc/self/pagemap`. A sufficiently
privileged co-tenant on the same host could observe these operations. This is
not a concern in Lambda (single-tenant microVM) but may matter in shared Kubernetes
deployments.

## AWS Lambda / Firecracker Context

AWS Lambda runs each function in a Firecracker microVM. The Lambda runtime
sandbox already provides:

- VM-level memory isolation (no shared physical pages between concurrent
  invocations).
- A restrictive seccomp-bpf profile applied to the guest process. This profile
  typically blocks `clone(CLONE_NEWNS)` (namespace creation), `ptrace`,
  `userfaultfd` (on older Lambda AMIs), and other sandboxing primitives.

Because namespace and seccomp-based sandboxing is not available inside the
Lambda execution environment, WASM fills the isolation gap:

- **No `clone(CLONE_NEWNS)`** → WASM provides equivalent file/network/process
  isolation via the WASI capability model without requiring OS namespaces.
- **No nested seccomp** → wazero's interpreter/compiler enforces memory bounds
  and provides a controlled syscall surface via WASI host functions, without
  needing a seccomp filter at the host-syscall level.
- **Firecracker's per-invocation isolation** → ensures that even if the WASM
  sandbox is somehow bypassed, the attacker is contained to a single microVM
  with a fixed concurrency limit and a 15-minute total lifetime.
