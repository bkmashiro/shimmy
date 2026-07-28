# Runtime comparison bridge

This experiment creates a comparison graph, not a universal runtime ranking.
Every edge has an explicit semantic or artifact identity, lifecycle evidence,
and a bounded claim.

## Comparison graph inputs

| Edge | Paths | Comparison basis |
|---|---|---|
| `system-native-vs-wasm-semantic` | fresh native file process ↔ generic WASM restore | same fixed equality semantics, public HTTP payload, clean-state evidence, runner, and limits; implementation source differs |
| native ↔ DBI (downstream evidence) | native Lean evaluator ↔ the identical evaluator under DynamoRIO | existing exact pair; this bridge does not force the Go runtime through the DBI clone-denying policy |
| `python-warm` | persistent CPython ↔ Agent COW/memcpy/single-use ↔ loaded Pyodide runtime | exact same `python/eval.py`, payload, runner, and limits; state mechanisms still differ |
| `python-clean` | fresh CPython ↔ Agent fresh/COW/memcpy/single-use ↔ Pyodide fresh namespace | exact same Python source and verified invocation count one; process, module, memory, single-use, and namespace isolation are not equivalent |
| native ↔ QEMU fresh | host native file process ↔ byte-identical binary in a fresh TCG VM | produced by the QEMU job and joined downstream by fixture ID |

DBI and QEMU are wrappers around a native evaluator, not peer guest languages.
The docs join the DBI edge from the existing exact Lean pair and the QEMU edge
from the exact-binary file job; neither is fabricated from the Python or generic
WASM bridge.
Pyodide's process is persistent, but legacy script mode executes the evaluator
in a fresh namespace for every request; the counter evidence therefore qualifies
clean Python application state, not linear-memory reset. The removed legacy
`ReactorPythonDispatcher` is historical evidence, not a current competitor;
`python-reactor` now selects `AgentPythonDispatcher`.

## Workloads

The Python family and exact-native wrapper paths expose both deterministic
profiles:

- `fixed`: JSON/HTTP/runtime fixed cost with zero loop iterations;
- `cpu-100k`: 100,000 iterations of a deterministic unsigned 64-bit recurrence.

The generic WASM edge uses only `fixed`, reusing the repository's already
qualified `demo-stateful` module. It is a same-semantics application E2E edge,
not a same-source runtime score. Native/DBI and native/QEMU retain exact binary
identity. Python paths execute the exact same Python file. Every response carries
`is_correct` and `guest_invocation_count`; contract-based paths additionally
carry `work_checksum`. The benchmark fails closed on the evidence each edge
declares.

## Cost placement

The report keeps these phases separate:

- container/public-server readiness;
- first evaluator request;
- warmed steady samples;
- Agent single-use prepared ready hit, immediate replacement miss, and refill-to-ready wait;
- clean-state versus mutable persistent counters.

Prepared single-use/eager refill costs remain separate evidence and are not
silently added to or removed from request latency.

## Run

The benchmark is wired into the existing manual/runtime-lane GitHub Actions
workflow. It refuses local execution unless `--allow-local` is explicitly set.
The CI command is:

```bash
python3 scripts/benchmark-runtime-comparison-bridge.py \
  --compose-file experiments/runtime-comparison-bridge/compose.yaml \
  --warmups 2 \
  --samples 10 \
  --output artifacts/runtime-lane-bench/comparison-bridge.json
```

The QEMU file benchmark uses:

```bash
FILE_EVALUATOR_PACKAGE=./experiments/runtime-comparison-bridge/native-file
FILE_EVALUATOR_ID=system-bridge-v1
```

The strict QEMU runtime `manifest.json` remains unchanged. A separate
`file-evaluator-manifest.json` sidecar and the host report must contain the same
evaluator binary SHA-256.
