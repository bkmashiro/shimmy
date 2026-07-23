# Python Reactor Flyweight

**Status:** implemented and verified

**Evidence host baseline:** `8df3a0529aef468628d97380ef4270a5f75c86d5`

**Guest artifact:** SHA-256 `78dcbb6d673351c0d3b776c42d2fb93b6f638cdc714d58072dece4b115edaa72`

## Goal

Make `ReactorPythonDispatcher` own one wazero Runtime, host-module set, and CompiledModule, while each pooled runner owns only a distinct mutable Module instance, WASI state, response buffers, snapshot strategy, and COW-private pages.

This is a density/lifecycle change, not an expansion of the reset claim: COW still resets linear memory only; mutable globals, tables, WASI resources, cancellation state, and Host state remain instance-scoped and independently initialized.

## Ownership invariants

### Dispatcher flyweight (shared, immutable after Start)

- one Guest artifact digest retained after one-time read/compile; the raw 241 MiB slice is released;
- one `wazero.CompilationCache` when configured;
- one `wazero.Runtime` / Store;
- one instantiated WASI host module and one `env` host module definition;
- one `wazero.CompiledModule`;
- one COW canonical-image coordinator;
- close order: all child modules and strategies, COW image, CompiledModule, Runtime, compilation cache.

### Runner instance (never shared between concurrent requests)

- one `api.Module` and module engine instance;
- linear-memory mapping and private dirty pages;
- mutable globals, tables, data/element instances, close/timeout state;
- per-module WASI `Sys` context and resources;
- exported function handles, stderr buffer, health, snapshot strategy;
- `Shutdown` closes only this module/strategy when borrowing the flyweight.

### Compatibility boundary

`NewReactorPythonRunner` remains a standalone owner for focused tests/direct callers. Dispatcher-created runners borrow the flyweight. No fallback silently recompiles per pooled runner: flyweight construction failure fails `Start` and closes partial resources.

## TDD slices

1. RED: shared owner compiles once and yields distinct modules; closing one child does not close owner/sibling.
2. RED: dispatcher pool size is the configured value (remove the stale hard cap of four) and every pooled/replacement runner borrows the same flyweight.
3. RED: timeout permanently closes only the affected module; sibling request succeeds and replacement uses the same flyweight.
4. RED: partial Start and concurrent Shutdown close children before shared owner exactly once.
5. GREEN: introduce dispatcher-owned flyweight and borrowed-runner initialization; preserve standalone runner behavior.
6. Evidence: exact Python artifact, COW pool N={1,2,4,8}; record compile count, startup phases, idle PSS/private/shared memory, per-runner slope, request samples, and timeout/sibling recovery.

## Required gates

- focused RED/GREEN unit tests with non-zero execution;
- `go test ./internal/execution/wasm`;
- `go test -race ./internal/execution/wasm`;
- `go test ./...`, `go vet ./...`, `go build ./...`;
- non-Linux cross-build/stub compatibility;
- bare Linux COW mechanism tests;
- exact fixed Python artifact heavy COW test, sibling-timeout/replacement test, and density evidence validation;
- independent post-fix review of diff and generated evidence.

## Verified evidence

GitHub Actions run [`29976320158`](https://github.com/bkmashiro/shimmy-wasm-go/actions/runs/29976320158) ran on bare `linux/amd64` with Go 1.24.5 and the fixed Guest artifact. Its uploaded `cow-python-8df3a052...` artifact contains the raw JSON/logs.

- exact Python prepared-baseline proof: six successful reset requests, one structured Guest error, one timed-out module replacement, sibling module still callable, zero COW fallback;
- prepared memory: `240,058,368` bytes; canonical image digest stable within each dispatcher;
- idle PSS regression across N={1,2,4,8}: fixed intercept **549.72 MiB**, incremental slope **1.882 MiB/runner**, including **0.450 MiB/runner** page-table slope;
- removing the retained raw artifact reference reduced the measured fixed PSS intercept by about **241.99 MiB**; wazero still retains one shared decoded DataSection/compiled representation;
- N=8 idle PSS: **565.22 MiB**. RSS/VmHWM is not a physical-density metric because it counts every shared 228.94 MiB mapping in full;
- complete prepared request: COW **1.064 ms/op**, full memcpy **11.055 ms/op**, or **10.39×** latency advantage in this five-sample evidence run;
- memory-only 32 GiB model with 28 GiB available: about 14,942 idle, 6,741 at 1% dirty, 2,109 at 5%, 1,135 at 10%, 475 at 25%, and 241 at 50% dirty when all instances are simultaneously active.

The last capacity line is a memory model, not a throughput promise. Current `Start` eagerly prepares the whole configured pool with at most four concurrent initializers; initialization time, CPU, virtual mappings, and kernel limits will constrain a multi-thousand-instance deployment before steady-state memory does. A future lazy/on-demand admission layer is separate scope.

## Acceptance

- one compile and one Runtime per dispatcher, proven by ownership tests and logs/evidence;
- distinct mutable module instances and COW mappings;
- one module timeout cannot invalidate a healthy sibling;
- replacement does not create a new Runtime or recompile;
- no pool hard cap of four; configured capacity remains bounded by Host policy/configuration, while concurrent prepare is capped at four to bound startup peak;
- startup/steady-state PSS claims are generated from raw Linux evidence, not estimated `H` constants;
- clean signed commit and pushed branch after all gates pass.
