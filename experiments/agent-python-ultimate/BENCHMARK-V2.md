# Agent Python COW Reactor Comprehensive Benchmark v2

Status: frozen design; v1 producer remains immutable and source-bound.

## Evidence layers

1. **Mechanism microbenchmarks**: COW remap, full-copy restore, UFFD/soft-dirty where explicitly selected.
2. **Reactor direct E2E**: production `AgentPythonDispatcher` request lifecycle.
3. **HTTP E2E**: production handler over loopback TCP; never mixed with direct latency.
4. **Capacity and queueing**: closed-loop clients and open-loop arrivals in separate artifacts.
5. **Density and endurance**: one fresh process per pool-size row; sustained post-knee stability.

Provider/model/network latency is excluded. The GPU allocation is unused and exists only to obtain a fixed scheduled CPU/cgroup stratum.

## Frozen v1 baseline

The source-bound v1 ultimate run contains 1,401 rows across four lifecycles, startup/cache, payload size/shape, requested dirty rate/pattern, deterministic CPU work, burst concurrency, faults, HTTP, and process/cgroup observations. It remains canonical for those rows.

The following v1 limitations are explicit and must not be repaired by reinterpretation:

- most rows have only three successful requests, so p99 is an observed maximum and is not percentile-claim eligible;
- `single-use-saturation` does not set a concurrency input;
- `density` is not a fresh-process `N=1,2,4,8,16` sweep;
- `dirty_bps` is requested logical arena dirtiness, not observed physical private-dirty pages;
- concurrency rows are bounded closed-window bursts, not an open-loop arrival experiment.

## v2 extension campaigns

### E1 — sustained closed-loop capacity

Primary scope: `snapshot/cow` and `snapshot/memcpy`. Single-use and fresh are bounded lifecycle controls, not equal-duration throughput competitors.

| Dimension | Values |
|---|---|
| pool/prepared capacity | 1, 2, 4 |
| clients | 1, 2, 4, 8, 16 |
| successful measured requests | at least 128 per row |
| warmup | 16, retained separately |
| CPU | none, python-100k, numpy-1m, numpy-matmul-64 |
| arena | 64 MiB primary; 8 and 128 MiB boundary rows |
| dirty rate | 0, 0.01%, 0.1%, 1%, 10%, 50%, 100% |
| pattern | sparse primary; contiguous and fixed-seed-random boundary rows |

Record per request: scheduled/actual start, finish, outcome, end-to-end duration. Record per runtime request ID: checkout, execute, decode, restore/close, replacement/refill. Record batch wall duration and exact successful/failed/timeout counts.

### E2 — open-loop queueing and saturation

Derive a provisional capacity for each frozen `(lifecycle,pool,workload)` from E1 without deleting samples. Pre-register arrival rates at `25%, 50%, 75%, 90%, 100%, 110%, 125%, 150%` of that provisional rate. Each row runs for a fixed measurement interval after warmup.

Record:

- intended and actual arrival timestamps;
- scheduler lag;
- admitted, completed, failed, timed out, and not-admitted counts;
- in-flight and queue depth time series;
- service time versus end-to-end/queueing latency;
- throughput and latency distributions;
- CPU, PSS, Private Dirty, cgroup memory, minor/major faults and Go GC deltas.

No open-loop row may silently become closed-loop under overload. Backpressure and dropped/not-admitted work are first-class outcomes.

### E3 — reset and dirty calibration

For `snapshot/cow` and `snapshot/memcpy`, run paired same-process cases over:

- prepared arenas: 8, 32, 64, 128 MiB;
- dirty rates: 0, 0.01%, 0.1%, 1%, 10%, 50%, 100%;
- contiguous, sparse, fixed-seed-random;
- at least 20 reset samples per point.

Keep requested logical arena pages separate from observed mapping/process evidence. For COW, record same-memfd mapping identity, PSS/Private Dirty before write, after write, after reset+refault, minor faults, reset duration, and post-reset digest. For full copy, record copied extent and restore duration. Fallback rejects the row.

### E4 — fresh-process density

One fresh OS process per `(strategy,N,repeat)` for `N=1,2,4,8,16`. Preserve the production per-runtime cap of four by recording runtime shards. Require exact ready capacity before sampling.

Record PSS, RSS (diagnostic only), Private Dirty, VmPTE, VMA/FD count, Go heap/sys, cgroup scope, startup/readiness and fixed/per-slot regression inputs. Repeat each N at least three times; raw non-monotonic rows are retained.

### E5 — endurance and recovery

Run the selected COW point at approximately 70% of the measured saturation knee for 30 minutes, plus a short 110% overload segment. Sample resources and latency in fixed windows. Include timeout/cancel/replacement and the next-request correctness check. Report memory/latency slope and GC/fault growth; do not collapse fault rows into normal latency.

## Statistics

For each eligible row report raw samples and:

- count, min, mean, median, p90, p95, p99, max;
- bootstrap 95% confidence interval for median and throughput using a fixed recorded seed;
- throughput, error/timeout/not-admitted rates;
- average and peak in-flight work;
- queue/service/end-to-end distributions;
- CPU, memory and fault deltas.

A p99 claim requires at least 100 successful samples. Lower-N rows retain nearest-rank values but are labelled descriptive/low-N.

## Capacity models and assumptions

Steady-state Little's Law:

```text
L = lambda * W
lambda ~= C / W
```

The second form applies only below saturation, for stable closed-loop clients and a consistent definition of `W`. It must not use a post-saturation p99 as service time.

Pool/CPU/memory upper-bound model:

```text
lambda_max ~= min(
  pool_size / mean_slot_hold_time,
  effective_cpu_parallelism / mean_cpu_service_time,
  memory_budget / marginal_active_memory
)
```

COW reset model, fitted from E3 rather than treated as a constant:

```text
T_reset ~= T_fixed
        + alpha * observed_unique_dirty_pages
        + beta  * mapped_bytes
        + gamma * cleanup_work
```

COW density model:

```text
COW ~= A + M + N * (P + d*M + H)
full_copy ~= A + N * (2*M + H)
```

Every fitted model reports coefficients, R², residuals, input range, host stratum, and exclusion boundaries. No instances-per-GiB claim is made unless `H` is estimated from the fresh-process N-sweep.

## Saturation knee

The reported knee is the lowest offered load/concurrency where at least two of these hold relative to the prior stable point:

1. queue wait grows monotonically for two points;
2. p99 end-to-end slope increases by at least 2×;
3. throughput gain is below 10% while offered load rises;
4. error/timeout/not-admitted rate becomes non-zero;
5. CPU, memory, pool or cgroup ceiling is reached.

The raw curve remains authoritative; knee detection is a deterministic derived label.

## Artifact closure

Each artifact binds source commit, binary/Guest/config/plan digests, kernel/CPU/cpuset/cgroup, Go/wazero versions, GOMAXPROCS, fixed seed, warmup, sample count and command. Raw evidence is immutable. Derived analysis is deterministic and generated into a separate file. Mechanism, direct E2E, HTTP, open-loop, density and endurance artifacts are not pooled into one latency distribution.
