# Wazero COW Prepared Memory Autonomous Roadmap

> **For Hermes:** This is the active long-running execution source of truth. Read it fully, trust live Git state over this prose, update it after every verified slice, and do not stop after one successful slice.

**Goal:** Add an explicit Linux-only copy-on-write prepared-memory strategy for eligible wazero module instances, prove cross-instance isolation and exact reset on bare GitHub Actions Linux, then integrate it with generic WASM and evaluator-preloaded Python reactor pools without weakening existing fail-closed semantics.

**Architecture:** A dispatcher-scoped coordinator owns one immutable prepared linear-memory image. Each eligible runner uses wazero's experimental `MemoryAllocator` to reserve stable backing, prepares normally, verifies its baseline against the canonical image, then remaps the same address as `MAP_PRIVATE`. Kernel COW supplies private dirty pages; post-request restore discards those pages before a healthy runner returns to the pool. UFFD remains an independent fallback for ordinary anonymous backing and is not layered onto COW.

**Tech stack:** Go 1.24+; wazero 1.11 experimental memory allocator; Linux `memfd_create`, sealing, `mmap`, and `madvise`/same-address remap; GitHub Actions bare `ubuntu-latest`; existing Shimmy `SnapshotStrategy`, generic dispatcher, and CPython-WASI reactor.

---

## User intent

The owner approved this design and asked Hermes to write the roadmap and execute it. Continue autonomously through safe verified slices. Linux/kernel behavior and real Python-reactor evidence belong in GitHub CI. **Do not run Docker locally.** Local macOS work is limited to editing, formatting, platform-neutral unit tests, and Linux cross-compilation where useful; it is not canonical COW evidence.

## Value filter and explicit non-goals

Prefer small fail-closed slices that prove memory ownership, exact reset, pool hygiene, and target-Linux behavior. Keep default runtime behavior unchanged until evidence is complete.

Do not, without fresh approval:

- combine UFFD write-protection with the COW path;
- delete memcpy/UFFD/mprotect/soft-dirty fallbacks;
- make COW the default or add it to `auto` selection;
- support post-image `memory.grow`; V1 must reject growth or discard the runner;
- claim whole-module/process freshness from linear-memory reset alone;
- clone or skip per-runner `_initialize`/`py_init` using only a linear-memory image;
- cache images globally across dispatchers, artifacts, evaluators, dependency mounts, or environments;
- use local Docker, paid cloud, production deployment, package publication, or release creation.

## Repository and source of truth

- Repository: `/Users/yuzhe/projects/shimmy-wasm`
- Active branch at discovery: `feat/wasm-backend`
- Runtime selection: `internal/execution/dispatcher.go`
- Generic pool: `internal/execution/wasm/dispatcher.go`, `supervisor.go`
- Python reactor lifecycle: `internal/execution/wasm/python_reactor.go`
- Strategy contract: `internal/execution/wasm/snapshot.go`
- Strategy selection/config: `internal/execution/wasm/config.go`, `snapshot_stub.go`, Linux strategy files
- Pinned engine: `github.com/tetratelabs/wazero v1.11.0`
- CI patterns: `.github/workflows/build.yml`, `uffd-probe.yml`, `bench-snapshot.yml`

## Current state discovered

- Generic WASM compiles one module and instantiates `N` supervisors. Every wazero instance owns a distinct `MemoryInstance.Buffer`.
- `FullMemcpyStrategy` allocates an owned `[]byte` snapshot for every instance, so fully resident guest memory is approximately `2NM` before Host/code overhead.
- Current UFFD tracks and restores dirty pages but still keeps per-instance live memory and per-instance baseline; it is not cross-instance COW.
- Python reactor already takes its snapshot at the correct prepared boundary: `_initialize` → `py_init` → `sys.path` setup → evaluator `py_prepare` → request headroom reservation → `Take`.
- Python currently restores before the next request. A COW runner would therefore retain dirty private pages while idle; the desired invariant is that every healthy runner returns to the pool already restored.
- wazero 1.11 exposes experimental `MemoryAllocator`/`LinearMemory`; module close calls `LinearMemory.Free`, so allocator and strategy ownership must not double-`munmap`.
- No active PR exists for `feat/wasm-backend`. Existing branch commits are signed and pushed to `origin`.

## Desired future state

### Correctness

- `FUNCTION_WASM_SNAPSHOT_MODE=cow` is explicit and Linux-only.
- COW-eligible module instances expose complete contiguous linear memory but share untouched physical baseline pages through `MAP_PRIVATE`.
- A write in one instance cannot affect another instance or the canonical image.
- After response bytes are copied/parsed into Go-owned data, reset restores exact baseline bytes before pool return.
- Pointer/size drift, reset failure, attempted unsupported growth, timeout-closed modules, or invalid backing fail closed and prevent pool reuse.
- Independently prepared Python runners attach only when their prepared memory size and SHA-256 equal the dispatcher canonical image; mismatch falls back truthfully or fails startup according to the explicit mode contract.

### Ownership

- `CowLinearMemory.Free` owns per-instance `munmap`.
- The dispatcher-scoped coordinator owns the sealed baseline fd and closes it after all runners/modules are closed.
- `CowSnapshotStrategy.Close` detaches strategy/image bookkeeping but does not unmap allocator-owned memory.
- Request hot-path calls (`api.Memory`, cached function exports, adapters, pool bookkeeping) remain unchanged except for post-response restoration and request-scoped Host buffer cleanup.

### Evidence

- Automatic bare-Linux CI proves allocator/memfd/mapping isolation, exact reset, baseline mismatch handling, growth rejection, wazero integration, and cross-platform build compatibility.
- Manual heavy GitHub CI downloads the pinned Python reactor artifact, initializes at least two independent prepared runners, records baseline equality/mismatch honestly, runs repeated-state canaries, and uploads structured evidence.
- Benchmarks report full request behavior and RSS/PSS/page-fault evidence; reset syscall timing alone is not a product claim.

## Stop conditions

Continue automatically after every verified slice. Stop only when:

1. every executable checkbox below is complete with real gate evidence;
2. a required external artifact/permission is unavailable and no safe synthetic lane remains;
3. repeated GitHub Actions failure proves a target-kernel/toolchain decision is required;
4. proceeding would require changing wazero internals, broad unsafe signal/runtime behavior, or a default strategy flip not approved here.

A clean tree, successful checkpoint, green focused test, or context boundary is not a stop condition.

## Global gates

### Before every commit

```bash
gofmt -w <touched-go-files>
git diff --check
go test ./internal/execution/wasm/...
go test ./...
git status --short --branch
```

Local macOS skips Linux-only runtime tests by build tag; this is expected and must not be reported as Linux evidence. Do not run local Docker.

### Remote canonical gates

- Automatic `.github/workflows/cow-memory.yml` on relevant branch pushes and pull requests, using bare `ubuntu-latest`.
- Linux focused correctness tests with `-count=1` and bounded timeout.
- Full `go test ./...` on Linux.
- Manual heavy Python-reactor job for the release artifact and multi-runner baseline proof.
- Manual benchmark/evidence job only after correctness is green.

## Autonomous execution queue

### Track A — Roadmap and remote gate skeleton

**Promise:** Work is durable and every Linux-only claim has an executable GitHub Actions gate.

- [x] Add this active roadmap and verify repository scope.
- [x] Add `.github/workflows/cow-memory.yml` with automatic lightweight Linux tests and manual heavy inputs; no Docker steps.
- [x] Add path filters for COW source/tests, config, supervisor/dispatcher/reactor wiring, and the workflow itself.

**Gate:** YAML readback, `git diff --check`, signed push, workflow run visible for the target SHA.

### Track B — Linux COW backing RED→GREEN

**Promise:** A standalone allocator/image pair proves kernel semantics before production dispatchers use it.

Planned files:

- Create `internal/execution/wasm/snapshot_cow_linux.go`
- Create `internal/execution/wasm/snapshot_cow_stub.go`
- Create `internal/execution/wasm/snapshot_cow_linux_test.go`

Slices:

- [x] RED: two private mappings share baseline but writes remain isolated.
- [x] RED: reset restores exact bytes and does not change base address or visible length.
- [x] RED: `Free` owns one unmap; strategy close cannot double-unmap.
- [x] RED: growth after image attachment is rejected/fails closed.
- [x] GREEN: implement stable allocator backing, sealed image, attach, reset, and cleanup.

**Do not:** use UFFD, process `fork`, cgo, a software load/store overlay, or global image cache.

### Track C — Wazero synthetic integration

**Promise:** Real wazero instances use the custom backing and preserve Shimmy's pool contract.

Planned files:

- Modify `internal/execution/wasm/snapshot.go`
- Modify `internal/execution/wasm/snapshot_stub.go`
- Modify `internal/execution/wasm/config.go`
- Modify `internal/execution/wasm/supervisor.go`
- Modify `internal/execution/wasm/dispatcher.go`
- Add focused integration tests under `internal/execution/wasm/`

Slices:

- [x] RED: config accepts explicit `cow`, keeps it pool-safe, and never maps deprecated UFFD flags onto COW.
- [x] RED: custom allocator is passed only to the target module instantiation and exposes the concrete backing explicitly.
- [x] RED: two real fixed-memory WASM instances attach to one coordinator, isolate writes, and restore exact state.
- [x] RED: canonical mismatch produces an explicit fallback/error and never overwrites a differing prepared state.
- [x] RED: pointer/size drift or restore error marks a supervisor unhealthy and replacement logic remains intact.
- [x] GREEN: connect dispatcher-scoped image coordinator and `CowSnapshotStrategy` without changing default `memcpy` behavior.

### Track D — Python reactor prepared image

**Promise:** COW captures evaluator-preloaded CPython state and a healthy runner returns clean to the pool.

Planned files:

- Modify `internal/execution/wasm/python_reactor.go`
- Modify/add focused Python reactor lifecycle tests
- Extend `.github/workflows/cow-memory.yml`

Slices:

- [x] RED: allocator attaches at the existing post-`py_prepare`, post-headroom `Take` boundary.
- [x] RED: dispatcher runners share one coordinator but prepare independently before hash/size attachment.
- [x] RED: response is parsed into Go-owned data before post-request restore.
- [x] RED: post-request restore runs on normal and recoverable error paths before pool return; failure marks runner unhealthy.
- [x] RED: request-scoped Host stderr capture is reset/bounded and not confused with COW freshness.
- [x] GREEN: implement reactor wiring while preserving timeout/discard/replacement semantics.
- [x] Remote heavy evidence: run at least two real prepared runners against the pinned release artifact and record whether baselines are byte/hash-identical. Do not force attach if they differ.

### Track E — Metrics, documentation, and promotion boundary

**Promise:** Performance and memory claims are reproducible and COW remains opt-in until justified.

- [x] Add structured Linux evidence for shared/private RSS/PSS and page faults across multiple dirty fractions.
- [x] Compare full-copy, UFFD where available, and COW without conflating restore-only and full-request latency.
- [x] Document operator config, Linux-only status, fallback, fixed-memory requirement, Host-state exclusions, and Python prepared-image boundary.
- [x] Keep COW explicit opt-in; record a separate future decision gate for any default/`auto` promotion.

## Per-slice checklist

1. Inspect live code and Git status.
2. Write a failing focused test, or state in this roadmap why a code test is not practical.
3. Run the test and capture the expected RED failure.
4. Implement the minimum behavior.
5. Run the focused gate.
6. Update this roadmap checkbox and completion log with real evidence.
7. Run applicable global gates.
8. Make a small signed commit and push unless the user has changed remote authority.
9. Verify signature, remote SHA, and relevant GitHub Actions run.
10. Continue immediately to the next executable slice.

## Completion log

- 2026-07-23: Discovery completed. Confirmed clean `feat/wasm-backend`, wazero 1.11 allocator hook, per-instance current snapshots, Python post-prepare `Take` boundary, pre-request-only reactor restore, allocator-owned `Free`, and eight existing GitHub workflows. No local Docker was used.
- 2026-07-23: Added the roadmap, bare-Linux COW workflow, and RED allocator contract. GitHub run `29969641876` failed on the planned missing COW symbols; this was the recorded remote RED state.
- 2026-07-23: Implemented sealed `memfd` images, same-address `MAP_PRIVATE` attachment/reset, SHA-256 prepared-state gate, attach-time growth freeze, and allocator-owned idempotent unmap. Focused Linux tests passed; stale unrelated Linux routing expectations were corrected, and full run `29969879062` passed.
- 2026-07-23: Connected dispatcher-scoped coordinators and wazero's experimental allocator to generic fixed-memory instances without changing the default strategy. Run `29970380214` passed two-instance attachment, repeated request/reset, growth rejection, and the full Linux suite.
- 2026-07-23: Connected Python reactor at the existing post-`py_prepare`, post-headroom boundary; moved restore to post-response pool return; reset Host stderr; preserved timeout discard/replacement. The first heavy run `29970702260` correctly exposed a stale release artifact without `py_prepare`; CI was repinned to documented reactor `v1.0.14` SHA-256 `78dcbb6d673351c0d3b776c42d2fb93b6f638cdc714d58072dece4b115edaa72` instead of weakening the test.
- 2026-07-23: Python behavior run `29971630921` passed on commit `d9f0cab35138859beff22507f34cfd77b472c8ff`: two independently prepared 240,058,368-byte runners, zero fallback, six normal requests, one recoverable Python error, one timeout/discard/replacement, canonical hash after every pool return, empty Host stderr, and complete-request benchmark. The canonical hash matched within each dispatcher but differed across separate runs, so no cross-process/global cache claim is made.
- 2026-07-23: Mechanism run `29971226763` passed: four private mappings of one 64 MiB memfd, 1/10/50% dirty cases, exact reset hashes, PSS/RSS/private-dirty/minor-fault JSON, reset-only COW/full-copy benchmark, and explicit `userfaultfd unavailable` evidence rather than fallback substitution. Results and limitations are recorded in `docs/cow-memory-image-evidence-2026-07-23.md`.
- 2026-07-23: Local non-Docker gates repeatedly passed: `go test ./...`, `go vet ./...`, `go build ./...`, `git diff --check`, and Linux/amd64 test-binary cross-compilation. Operator boundaries are documented in `docs/deployment-recipes.md` and `docs/wasm-backend-model.md`.
- 2026-07-23: Final lifecycle review found that timeout-set `closed=true` made runner `Shutdown` return before releasing its wazero runtime. Commit `f074b7f8b12bb79df1d3b8c263887851cb54c393` changed idempotence to test owned-resource state and added a fixed-runner cleanup assertion. Final heavy run `29972072029` passed the full Linux suite, prepared-image/error/timeout/replacement/resource-release proof, and complete-request benchmark.
- 2026-07-23: An extra local `go test -race ./internal/execution/...` gate exposed two pre-existing pooled-dispatcher tests that read asynchronous counters after a 1 ms sleep. Commit `56ee46d` replaced the sleeps with completion-channel synchronization; dispatcher `-race -count=20`, full execution race, and ordinary repository gates all passed. Production dispatcher code was unchanged.

## Current execution pointer

**Final review:** commit the evidence/operator docs, verify the signed remote SHA and all final gates, perform an independent fixed-SHA review, and address any validated finding before closing this roadmap.

## Short prompt to resume

```text
Read docs/plans/2026-07-23-wazero-cow-prepared-memory-roadmap.md fully, then execute it in /Users/yuzhe/projects/shimmy-wasm. Do not stop after one slice: update the roadmap, run gates, make signed commits, push, and continue until all executable work is done or a proven blocker remains. Put Linux/kernel/Python-reactor tests in GitHub Actions and never run local Docker.
```
