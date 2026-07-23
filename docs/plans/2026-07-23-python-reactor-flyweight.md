# Python Reactor Flyweight

**Status:** active implementation roadmap  
**Host baseline:** `b13a5d0bedfcc7626ef2bb70bf42c045e014a71e`  
**Guest evidence baseline:** fixed `python3.12-wasi-reactor` artifact from the existing heavy COW workflow

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

## Acceptance

- one compile and one Runtime per dispatcher, proven by ownership tests and logs/evidence;
- distinct mutable module instances and COW mappings;
- one module timeout cannot invalidate a healthy sibling;
- replacement does not create a new Runtime or recompile;
- no pool hard cap of four; configured capacity remains bounded by Host policy/configuration, while concurrent prepare is capped at four to bound startup peak;
- startup/steady-state PSS claims are generated from raw Linux evidence, not estimated `H` constants;
- clean signed commit and pushed branch after all gates pass.
