# Cross-runtime E2E benchmark report

Date: 2026-07-01  
Workflow run: [`28530755690`](https://github.com/bkmashiro/shimmy/actions/runs/28530755690)  
Branch / commit: `feat/python-runtime-benchmark-lanes` / `a592122ad5e623dba9db0081e0399454e79d6c51`  
Artifact: `e2e-benchmark` / `benchmark-e2e.json`  
Profile: `ci`  
Measured iterations: `5`  
Warmup iterations: `2`

## Scope

This run exercises Shimmy through the real HTTP runtime path instead of calling
individual adapters directly. The benchmark builds the host `shimmy` binary,
builds the generic Go/WASI demo module, starts Shimmy servers for each runtime
configuration, sends `eval` / `preview` requests, and records request latency and
basic correctness metadata.

The manual CI job also prepares the optional Python runtime dependencies so the
Python compatibility lanes are measured rather than skipped:

- Pyodide: `npm ci --prefix examples/eval-pyodide`.
- reactor-python: download `python-reactor.wasm` from
  `bkmashiro/webassembly-language-runtimes` release `v1.0.13` and verify
  `sha256=4c5fea0b3a6a31a54ea83f8f93a7c912627b4cf5fc6516ee8e50159bb7c04d4c`.

## Runtime lanes

| Runtime | Interface | What it measures |
|---|---:|---|
| `generic-wasm-go` | `wasm` | Generic WASI module through Shimmy's WASM ABI. |
| `python-reactor` | `wasm` | Lambda Feedback-style Python evaluator through the CPython/WASI reactor profile. |
| `python-pyodide` | `pyodide` | Lambda Feedback-style Python evaluator through the historical Pyodide/Node compatibility runner. |
| `python-file-env` | `file` | Python LF fixture through Shimmy's file worker with root/entrypoints from environment variables. |
| `python-file-request` | `file` | Same file worker path, but evaluator root/entrypoint supplied per request. |

## Results

All 17 CI-profile rows passed; no benchmark rows were skipped in this run.

| Case | Runtime | Command | Snapshot | Payload | Request bytes | Response bytes | Mean ms | p95 ms | Status |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|
| `generic-wasm-go-eval-light` | `generic-wasm-go` | eval | full | light | 43 | 193 | 1.62 | 1.66 | passed |
| `generic-wasm-go-eval-heavy` | `generic-wasm-go` | eval | full | heavy | 68,404 | 193 | 25.06 | 26.50 | passed |
| `generic-wasm-go-preview-light` | `generic-wasm-go` | preview | full | light | 47 | 76 | 1.57 | 1.64 | passed |
| `generic-wasm-go-eval-light-off` | `generic-wasm-go` | eval | off | light | 43 | 194 | 0.72 | 0.74 | passed |
| `python-file-env-eval-light` | `python-file-env` | eval | none | light | 43 | 68 | 49.95 | 50.87 | passed |
| `python-file-env-eval-heavy` | `python-file-env` | eval | none | heavy | 68,404 | 68 | 53.43 | 54.30 | passed |
| `python-file-env-preview-light` | `python-file-env` | preview | none | light | 47 | 79 | 50.73 | 51.25 | passed |
| `python-file-request-eval-light` | `python-file-request` | eval | none | light | 216 | 68 | 50.32 | 50.87 | passed |
| `python-file-request-eval-heavy` | `python-file-request` | eval | none | heavy | 68,558 | 68 | 53.67 | 54.65 | passed |
| `python-file-request-preview-light` | `python-file-request` | preview | none | light | 199 | 76 | 50.72 | 51.25 | passed |
| `python-pyodide-eval-light` | `python-pyodide` | eval | none | light | 43 | 68 | 1.54 | 1.91 | passed |
| `python-pyodide-eval-heavy` | `python-pyodide` | eval | none | heavy | 68,404 | 68 | 6.02 | 6.44 | passed |
| `python-pyodide-preview-light` | `python-pyodide` | preview | none | light | 47 | 108 | 1.53 | 1.93 | passed |
| `python-reactor-eval-light` | `python-reactor` | eval | full | light | 43 | 98 | 13.13 | 13.16 | passed |
| `python-reactor-eval-heavy` | `python-reactor` | eval | full | heavy | 68,404 | 103 | 20.36 | 20.48 | passed |
| `python-reactor-preview-light` | `python-reactor` | preview | full | light | 47 | 92 | 13.34 | 13.49 | passed |
| `python-reactor-eval-light-off` | `python-reactor` | eval | off | light | 43 | 98 | 2.61 | 2.71 | passed |

## Snapshot / isolation observations

| Case | Snapshot | Guest invocation count | Isolation OK | Mean ms | p95 ms |
|---|---:|---:|---:|---:|---:|
| `generic-wasm-go-eval-light` | full | 1 | true | 1.62 | 1.66 |
| `generic-wasm-go-eval-light-off` | off | 7 | false | 0.72 | 0.74 |

The generic Go/WASI demo confirms the intended control behavior: full snapshot
restore resets guest mutable state across requests, while `snapshot=off` is
faster but intentionally leaks the warm instance's invocation counter.

The reactor-python lane also reports a clear snapshot overhead comparison for
light eval:

| Case | Snapshot | Mean ms | p95 ms |
|---|---:|---:|---:|
| `python-reactor-eval-light` | full | 13.13 | 13.16 |
| `python-reactor-eval-light-off` | off | 2.61 | 2.71 |

## Comparisons

These ratios use mean latency from this single CI run; they are directional, not
a full performance study.

| Comparison | Ratio | Read as |
|---|---:|---|
| `generic-wasm-go-eval-light` / `python-file-env-eval-light` | 0.03x | generic WASM light eval is about 31x faster than Python file-worker env. |
| `python-reactor-eval-light` / `python-file-env-eval-light` | 0.26x | reactor-python light eval is about 3.8x faster than Python file-worker env. |
| `python-pyodide-eval-light` / `python-file-env-eval-light` | 0.03x | resident Pyodide compatibility path is about 32x faster than Python file-worker env for this tiny fixture. |
| `generic-wasm-go-eval-heavy` / `python-file-env-eval-heavy` | 0.47x | generic WASM heavy eval is about 2.1x faster than Python file-worker env. |
| `python-reactor-eval-heavy` / `python-file-env-eval-heavy` | 0.38x | reactor-python heavy eval is about 2.6x faster than Python file-worker env. |
| `python-pyodide-eval-heavy` / `python-file-env-eval-heavy` | 0.11x | Pyodide heavy eval is about 8.9x faster than Python file-worker env for this fixture. |
| `generic-wasm-go-eval-light-off` / `generic-wasm-go-eval-light` | 0.44x | disabling generic WASM snapshot is about 2.2x faster, but loses isolation. |
| `python-reactor-eval-light-off` / `python-reactor-eval-light` | 0.20x | disabling reactor snapshot is about 5.0x faster, but should be treated as a no-isolation control. |

## Interpretation

- The manual CI benchmark now covers all intended CI-profile lanes: generic
  WASM, Python file worker env/request, Pyodide, and reactor-python.
- The previous Pyodide and reactor-python skips were dependency-preparation
  gaps, not runtime failures. With `npm ci` and the pinned reactor artifact in
  the workflow, both lanes pass in GitHub Actions.
- The file-worker Python paths are consistently around 50 ms in this CI run,
  dominated by process/file-worker overhead rather than payload size.
- Generic WASM and Pyodide are very fast for the tiny fixture. Treat the Pyodide
  numbers as compatibility-runner numbers for this synthetic LF fixture, not as
  proof that arbitrary heavy Python packages will behave similarly.
- Reactor-python has meaningful overhead with full snapshot restore, but remains
  materially faster than the file-worker path for the measured Python fixture.
- `snapshot=off` rows are controls only. They are useful to expose snapshot
  overhead, but they are not acceptable isolation semantics for normal evaluator
  execution.

## Reproduction

Manual CI trigger:

```bash
gh workflow run build.yml \
  --repo bkmashiro/shimmy \
  --ref feat/python-runtime-benchmark-lanes \
  -f run_e2e_benchmark=true
```

Download artifact:

```bash
gh run download 28530755690 \
  --repo bkmashiro/shimmy \
  --name e2e-benchmark \
  --dir /tmp/shimmy-ci-benchmark-28530755690
```

Local / Docker smoke examples:

```bash
make benchmark-e2e BENCH_ARGS='--profile ci --iterations 1 --warmup 0 --json-output /tmp/benchmark-e2e-local.json'
make benchmark-e2e-docker BENCH_ARGS='--profile ci --iterations 1 --warmup 0 --runtime python-reactor --json-output /tmp/benchmark-e2e-docker-reactor.json'
```
