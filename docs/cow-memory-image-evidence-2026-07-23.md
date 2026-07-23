# Linux COW prepared-memory evidence — 2026-07-23

## Status

Shimmy has an **explicit, Linux-only prepared-memory COW prototype** for generic
WASM and the CPython-WASI Python-reactor profile. The default remains full copy.
The implementation uses wazero `v1.11.0`'s experimental memory allocator API,
a sealed dispatcher-scoped `memfd`, and one writable `MAP_PRIVATE` mapping per
eligible instance.

This document records correctness and mechanism evidence. It is not a production
performance claim and does not justify an automatic/default strategy change.

## Evidence environment

Final correctness and Python prepared-image run:

- GitHub Actions run: [`29971630921`](https://github.com/bkmashiro/shimmy-wasm-go/actions/runs/29971630921)
- Commit: `d9f0cab35138859beff22507f34cfd77b472c8ff`
- Platform: `linux/amd64`
- Go: `go1.24.5`
- wazero: `v1.11.0`
- Kernel: Linux `6.17.0-1020-azure`
- Python reactor: `bkmashiro/webassembly-language-runtimes` `v1.0.14`
- Artifact SHA-256: `78dcbb6d673351c0d3b776c42d2fb93b6f638cdc714d58072dece4b115edaa72`

Mechanism/PSS run:

- GitHub Actions run: [`29971226763`](https://github.com/bkmashiro/shimmy-wasm-go/actions/runs/29971226763)
- Commit: `b88c30988151906689388484d6ef5db1a84dcf21`
- Platform/kernel/toolchain: same runner class and versions listed above
- UFFD write-protect availability on this runner: **unavailable**

No local Docker build or container test was run for this work.

## Correctness gates

| Gate | Result |
|---|---|
| Two low-level `MAP_PRIVATE` mappings isolate writes | Pass |
| Reset restores exact bytes at the same virtual address | Pass |
| Allocator `Free` is idempotent and owns per-instance unmap | Pass |
| Candidate prepared-image mismatch refuses COW attachment | Pass; instance remains eligible for full-copy fallback |
| Active COW mapping rejects `memory.grow` | Pass |
| Lost/closed canonical image makes restore fail and marks generic supervisor unhealthy | Pass |
| Two real generic wazero instances attach to one image | Pass |
| Generic repeated request/reset behavior | Pass |
| Two real Python runners independently complete `py_init` + `py_prepare` and attach to one image | Pass |
| Python response is copied before post-request reset | Pass |
| Idle Python runner hash equals its canonical prepared image | Pass |
| Recoverable Python exception returns structured output and still resets | Pass |
| Timeout closes/discards the old Python runner and replacement reattaches | Pass |
| Host stderr buffer is empty before Python runner returns to pool | Pass |
| Full Linux `go test ./...` | Pass |

The final Python evidence used two independently prepared runners with a
`240,058,368`-byte linear-memory image, six successful requests, one recoverable
Python exception, one timeout/replacement, and zero COW fallbacks.

The two runners matched within each dispatcher run. The canonical digest changed
between separate CI runs, so this evidence **does not** support a cross-process or
global prepared-image cache. The implementation intentionally keeps the image
coordinator dispatcher-scoped.

## Linux VM accounting diagnostic

The mechanism run used:

- baseline: `64 MiB`;
- instances/private mappings: `4`;
- dirty fractions: `1%`, `10%`, `50%`;
- mapping evidence: four `/proc/self/maps` entries for the same named memfd;
- verification: every reset was followed by a full SHA-256 comparison.

Process-level `/proc/self/smaps_rollup` observations:

| Dirty fraction | Requested dirty pages across four instances | Private_Dirty before → dirty → reset/refault (KiB) | Reset time |
|---:|---:|---:|---:|
| 1% | 652 | `10,976 → 13,592 → 10,984` | `6.20 ms` |
| 10% | 6,552 | `145,056 → 171,396 → 145,188` | `7.04 ms` |
| 50% | 32,768 | `276,460 → 407,532 → 276,460` | `10.72 ms` |

The first clean four-mapping observation reported RSS `274,624 KiB` but PSS
`76,557 KiB`, consistent with `/proc` charging four virtual mappings in RSS while
proportionally accounting shared physical pages in PSS. Later absolute process
values also include Go heap retained by comparator allocations; only within-case
transitions should be interpreted.

## Reset-only benchmark

GitHub runner, `64 MiB`, `3` iterations per case:

| Dirty fraction | COW reset | Full-copy restore |
|---:|---:|---:|
| 1% | `60.0 µs/op` | `2.53 ms/op` |
| 10% | `419 µs/op` | `2.75 ms/op` |
| 50% | `1.99 ms/op` | `2.57 ms/op` |

This benchmark times only reset/restore after direct page writes. It excludes
request execution, JSON adaptation, Python, pool acquisition, and faults paid by
later reads. It must not be quoted as end-to-end speedup.

UFFD was unavailable on this GitHub runner, so the evidence records that fact
instead of silently substituting another strategy or fabricating a comparison.
Existing UFFD benchmarks and runtime fallback remain separate from COW.

## Complete prepared Python request sample

Final run, pinned Python artifact, prepared evaluator, `5` iterations per mode:

| Strategy | Complete `SendRequest` + post-request restore | Allocations |
|---|---:|---:|
| Full copy | `10.28 ms/op` | `45 allocs/op` |
| COW | `1.22 ms/op` | `45 allocs/op` |

The observed ratio in this controlled five-iteration runner sample was about
`8.43×`. This is a mechanism/application-path diagnostic for one tiny prepared
evaluator, not a production estimate or a claim for arbitrary Python workloads.

## Promotion and isolation boundary

COW remains explicit opt-in through:

```text
FUNCTION_WASM_SNAPSHOT_MODE=cow
```

It is not selected by `FUNCTION_WASM_USE_UFFD`, and it is not part of `auto`.
Promotion requires a separate decision with broader artifacts, kernels, request
shapes, long-running leak tests, and workload-level measurements.

COW restores WASM linear memory only. It does not itself reset mutable WASM
globals/tables, Host/WASI descriptors and offsets, Host RNG/clock state, Go
objects, or external side effects. Timeout/trap/discard policy and capability
restrictions remain part of the isolation contract.
