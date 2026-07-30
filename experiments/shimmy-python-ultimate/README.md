# Shimmy Python Ultimate Manual Benchmark

Manual-only benchmark for the exact `shimmy-python-runtime-numpy-core.wasm`
artifact. It exercises the production `ShimmyPythonDispatcher`; the HTTP campaign
adds the production `RuntimeHandler` and `CommandHandler` across a real loopback
TCP connection.

## Hard rules

- Never invoke from CI. `scripts/benchmark-shimmy-python-ultimate.sh` and
  `scripts/benchmark-shimmy-python-doc.sh` reject common CI environments and
  expose no override.
- Bind every run to the artifact, manifest, input config, executable SHA-256,
  and source commit.
- Treat `snapshot_selected` as observed evidence. A requested `cow` row that
  selects `memcpy` is `unavailable`, never a COW result.
- Raw row JSON is canonical. Summaries are rebuildable with `validate`; no
  outlier is deleted.
- `metadata.complete` means every planned row has a structurally valid terminal
  record; it is not a success verdict. Consumers must inspect per-row status and
  the report's `ok` / `unavailable` / `failed` aggregates. Resume only reuses an
  exact matching row with `ok`, `unavailable`, or `unsupported` status; failed
  or behavior-drifted rows are executed again.
- Remote input and output live under `/tmp/shimmy-shimmy-python-$SLURM_JOB_ID`.
  A successful result remains there for up to 48 hours until the Mac streams
  and verifies the archive, extracts it, validates the exact-source raw report,
  and only then sends `ACK`; all exit paths remove the exact guarded job
  directory.

## Matrix

`configs/ultimate.json` expands deterministically to 1,401 curated rows across:

- `fresh`, `single-use`, `snapshot/memcpy`, `snapshot/cow`
- cold/warm compilation cache and direct/public-HTTP startup
- 256 B–1,020 KiB inputs and 128 B–900 KiB outputs
- flat, nested, numeric-array, and UTF-8 payloads
- 0/8/32/64/128 MiB prepared arenas
- 0%, 0.01%, 0.1%, 1%, 10%, 50%, and 100% dirty rates
- contiguous, sparse, and fixed-seed random dirtiness
- Python loops, NumPy vector operations, and NumPy matrix multiplication
- pool/prepared capacity 1–4 and concurrency 1–16
- exception, timeout, cancellation, memory growth, oversized payload, and
  post-fault recovery

`--limit` is only for explicit smoke runs. The limit and resulting plan are
stored in the run directory.

## Local gates

```bash
go test ./experiments/shimmy-python-ultimate -count=1
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest \
  scripts.tests.test_benchmark_shimmy_python_doc \
  scripts.tests.test_benchmark_shimmy_python_ultimate_manual
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -o /tmp/shimmy-python-ultimate ./experiments/shimmy-python-ultimate
```

A local exact-artifact smoke can use:

```bash
scripts/benchmark-shimmy-python-ultimate.sh run \
  --config experiments/shimmy-python-ultimate/configs/smoke.json \
  --artifact build/python-reactor/artifacts/shimmy-python-runtime-numpy-core.wasm \
  --manifest build/python-reactor/artifacts/manifest.json \
  --output /tmp/shimmy-python-smoke --limit 4 --max-duration 10m
```

## DoC allocation

Canonical manual request:

```text
partition=a16, node=gpuvm36, 1×nvidia_a16, 6 CPUs, 48 GiB RAM,
60 hours, non-exclusive, export=NIL
```

The GPU is a scheduling requirement and is not used by the benchmark. CPU and
memory claims apply only to the allocated Slurm cgroup on the fixed shared host.
The controller uses `sbcast` after the job reaches `RUNNING`; the job does not
read `/vol/bitbucket` and needs no remote Go, Docker, or sudo.

## Output

Each run contains:

- `metadata.json`: hashes, build info, host, Slurm identity, completion state
- `plan.json`: exact deterministic row list
- `rows/*.input.json`: immutable worker inputs
- `rows/*.json`: raw per-row phases, requests, selected mechanism, faults,
  process `/proc` and cgroup samples
- `checkpoint.jsonl`: fsync-backed completion log
- `report.json`: recomputed campaign/lane summaries
- `report.recomputed.json`: independent `validate` output

Run `shimmy-python-ultimate validate --output RUN_DIR` after transport and compare
the two reports before making any public performance claim.
