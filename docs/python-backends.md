# Python Backends

## Why Python Needs Special Treatment

CPython compiled to WASI (`python.wasm`) is a 242 MB binary. A clean
instantiation of it — linking the standard library, running startup code,
importing site packages — takes 7-8 seconds. That cost is acceptable once per
pool slot but not once per request.

shimmy-wasm provides four distinct execution paths for Python, trading off
isolation, latency, package compatibility, and memory usage against each other.
The right choice depends on the evaluator layout, dependency set, concurrency,
latency, and isolation requirements of the deployment.

## Execution Paths

### Path 1: Per-Request PythonRunner

**`FUNCTION_INTERFACE=wasm` + `FUNCTION_COMMAND=python.wasm`** (or a custom
Python-dialect WASM)

Implemented in `internal/execution/wasm/python.go` as `PythonRunner`.

`python.wasm` is compiled in **command mode**: it exports `_start`, not
`_initialize`. It behaves like a WASI command-line program — it reads from
stdin, writes to stdout, and exits.

**Startup:** `PythonRunner.Start` compiles `python.wasm` once (1-2 s cold).
The compiled module is cached in `PythonRunner.compiled` and reused for all
requests.

**Per request:** `RunScript` calls `rt.InstantiateModule` with fresh module
config each time. The module starts up (7-8 s: Python initialisation), reads
the request JSON from stdin, calls `evaluation_function`, writes the result to
stdout, and exits via `proc_exit`. wazero handles the exit as a `*sys.ExitError`.

**Isolation:** True — each request gets a fresh interpreter heap. Nothing from
a previous request survives.

**Latency:** ~160 ms per request (after compilation; includes Python startup).
This is dominated by CPython initialisation, not eval logic.

**Use case:** Automated testing or development where isolation is paramount and
latency does not matter.

### Path 2: Resident ResidentPythonRunner

**`FUNCTION_INTERFACE=rpc` + subprocess using `python.wasm`**, or specifically
**`FUNCTION_INTERFACE=python-wasm`**

Implemented in `internal/execution/wasm/python_resident.go` as
`ResidentPythonRunner`, pooled by `PythonDispatcher`.

`python.wasm` runs in command mode with a persistent server-loop script as its
`argv`. The module is instantiated once per pool slot and stays alive across
many requests. Go communicates with Python via `io.Pipe` pairs connected to the
module's stdin/stdout.

**The server loop script** (`residentServerScript`) runs inside the module:

```python
import sys, json

sys.stdout.write("__READY__\n")
sys.stdout.flush()

_base_modules = frozenset(sys.modules.keys())

while True:
    line = sys.stdin.readline()
    if not line: break
    req = json.loads(line)
    ns = {}
    exec(compile(req["script"], "<eval>", "exec"), ns)
    fn = ns.get("evaluation_function")
    result = fn(req["input"].get("response"), req["input"].get("answer"), ...)
    # evict imported modules back to base state
    for mod in list(sys.modules.keys()):
        if mod not in _base_modules:
            del sys.modules[mod]
    sys.stdout.write(json.dumps(result) + "\n__DONE__\n")
    sys.stdout.flush()
```

**Startup:** `Init` compiles `python.wasm` (1-2 s cold), instantiates the
module, runs the server loop in a background goroutine, and waits for
`"__READY__\n"` on stdout (total: ~7-8 s).

**Per request:** `SendRequest` writes a JSON line to stdin; a background reader
goroutine (`linesCh` channel) delivers lines from stdout. The host waits for
the JSON response line and the `"__DONE__"` sentinel. Round-trip is ~10-50 ms.

**Isolation:** Namespace-level — each request executes the user script in a
fresh `exec(compile(...), ns={})` namespace. Globals from one request do not
bleed into the next. Modules imported during a request are evicted from
`sys.modules` in the `finally` block so their mutable state does not persist.
However, the Python interpreter itself, the GC heap, and all modules in
`_base_modules` are shared across requests in the same runner instance.

**Wire protocol:**

```
Host → Python stdin:
  {"script": "...", "method": "eval", "input": {...}}\n

Python → Host stdout:
  {"is_correct": true, "feedback": "..."}\n
  __DONE__\n
```

**Pool:** `PythonDispatcher` sizes the pool at `min(NumCPU(), 8)`. All runners
initialise in parallel (goroutines); startup wall time equals the slowest
individual init.

**Use case:** Legacy compatibility or explicit comparison against the reactor backend. Do not use as the isolation-sensitive default: namespace cleanup and `sys.modules` eviction cannot reset the full CPython heap, base-module mutable state, interpreter caches, GC/allocator state, or C-extension state.

### Path 3: Reactor ReactorPythonRunner

**`FUNCTION_INTERFACE=reactor-python` + `FUNCTION_COMMAND=python-reactor.wasm`**

Implemented in `internal/execution/wasm/python_reactor.go` as
`ReactorPythonRunner`, pooled by `ReactorPythonDispatcher`.

`python-reactor.wasm` is compiled in **reactor mode** (`-mexec-model=reactor`,
WASI SDK) against `libpython3.14.a`. It exports named functions instead of
`_start`:

| Export | Signature | Description |
|---|---|---|
| `py_init` | `() → ()` | Initialise CPython once |
| `py_exec` | `(ptr i32, len i32) → ()` | Execute one request |
| `alloc` | `(size i32) → i32` | Allocate scratch memory |
| `dealloc` | `(ptr i32) → ()` | Free scratch memory |
| `resp_buf` | `() → i32` | Pointer to the 4 MiB response buffer |
| `resp_len` | `() → i32` | Pointer to the int32_t response length |

Because `py_exec` **returns** after each request (reactor model), the Go host
retains control between requests. This enables snapshot/restore of linear
memory between calls without any goroutine running inside WASM.

**Startup:** `Init` reads and compiles `python-reactor.wasm` (cold: 1-3 min for
the 242 MB binary; instant on a warm `FUNCTION_WASM_COMPILE_CACHE`). It then
instantiates in reactor mode (`WithStartFunctions("_initialize")`), calls
`py_init()` (~7-8 s for CPython startup), runs two-phase sys.path setup
(`initSysPath`), and takes a full memory snapshot of the post-init heap.

**Per request (full protocol):**

1. Restore memory snapshot (`strategy.Restore`) — resets CPython heap to
   post-`py_init` state.
2. `alloc(len(reqJSON))` — get scratch pointer.
3. Write request JSON to `memory[ptr:ptr+len]`.
4. `py_exec(ptr, len)` — Python locates `evaluation_function` in the script,
   calls it, writes the JSON result into `_resp_buf`, sets `_resp_len`.
5. `resp_buf()` → `bufPtr`, `resp_len()` → `lenPtr`.
6. Read `int32_t` at `memory[lenPtr:lenPtr+4]` (little-endian).
7. Read `memory[bufPtr:bufPtr+respLen]` as JSON.

**Isolation:** True heap isolation — the snapshot resets `CPython`'s entire
heap, `sys.modules`, global variables, GC state, and all interpreter-level
state to the exact post-`py_init()` values. This is stronger than namespace
isolation: even persistent mutable state in `sys.modules` (e.g., a module that
initialises a global on import) is reset.

**`_initialize` vs `_start`:** Reactor mode modules are instantiated with
`_initialize` instead of `_start`. `_initialize` sets up the WASI environment
and runs static constructors without entering `main`. `_start` would run
`main` and block forever (or call `proc_exit`). For CPython the distinction
matters: `_initialize` prepares the Python runtime but does not enter the
event loop or `Py_Main`; `py_init()` is then called explicitly to start the
interpreter.

**`exec(source, {})` namespace model (reactor vs resident):** In the resident
runner, each request runs `exec(compile(script, ...), ns={})` inside the
persistent server loop. The namespace `ns` is fresh per request but the
interpreter state (GC, `sys.modules`) is shared. In the reactor runner, the
entire interpreter heap is reset before `py_exec`; the Python code inside the
reactor's `_handle_request` function also uses `exec(script_src, {})`, but
since `sys.modules` itself has been reset to the snapshot state, any module
that was imported in request N is guaranteed absent in request N+1.

**Module isolation:** The snapshot taken after `py_init()` and `initSysPath`
captures numpy pre-warmed into `sys.modules` (via phase 2 of `initSysPath`).
All numpy submodules are in the snapshot. After restore, numpy is available
immediately without import cost. User-imported modules (from the eval script)
are absent from the snapshot and must be imported per-request; they are
evicted by the restore.

**Pool:** `ReactorPythonDispatcher` sizes the pool at `min(NumCPU(), 4)` (hard
cap 4) because each runner holds ~100 MB of CPython heap in WASM linear memory.
All runners initialise in parallel via goroutines; the slowest governs startup
wall time.

**Health tracking:** If `py_exec` is interrupted by a context timeout,
wazero's `WithCloseOnContextDone` permanently closes the module. The runner
detects this and sets `r.closed = true`. `IsHealthy()` returns `false`. The
dispatcher discards the runner and spawns a replacement in a background
goroutine via `spawnReplacement`.

**Use case:** Single-file evaluators that need the fastest Python path and a
compatible CPython-WASI dependency set. Reactor does not expose the Pyodide
package-mode environment contract directly, and the dispatcher still fails fast
if Pyodide package-mode env vars are set with `FUNCTION_INTERFACE=reactor-python`.
For real Lambda Feedback packages, use one of two explicit paths:

- default compatibility path: Pyodide package mode;
- fast-path shortcut: generate a single-file wrapper with
  `tools/lf-bundle-python/lf_bundle_python.py`, then point
  `FUNCTION_WASM_PYTHON_SCRIPT` at the generated bundle.

The bundle shortcut covers evaluator package code, the adapter shim, and
repeatable `--include-root` directories for pure-Python dependencies such as
`mpmath`, `typing_extensions`, `antlr4-python3-runtime`, and `latex2sympy2`.
Native/WASI packages such as NumPy must be present in the reactor artifact. The
verified `v1.0.11` artifact includes NumPy and SymPy, and has now passed
boilerplate, compareBoolean, ArrayEqual, IsSimilar, and SymbolicEqual
basic+LaTeX-preview fixture probes. SymPy still needs bundled pure-Python
`mpmath` plus the narrow reactor `ctypes` polyfill under
`tools/lf-bundle-python/polyfills/reactor`. Old ANTLR 4.7.x imports
`typing.io`, which no longer exists as a pseudo-package in CPython 3.14; the
bundler applies a narrow source rewrite to `from typing import ...`. SciPy is
intentionally not a reactor target; use Pyodide for SciPy-heavy evaluators. The
reactor host also installs a small `numpy.random` safety polyfill: `seed`,
`rand`, `randn`, `random_sample`, `uniform`, `normal`, `randint`, and basic
`choice` work deterministically through stdlib `random`, while unsupported
advanced RNG APIs fail with a Python error instead of aborting the WASM instance.

### Path 4: Pyodide package/script runner

**`FUNCTION_INTERFACE=pyodide` + `FUNCTION_PYODIDE_RUNNER=examples/eval-pyodide/runner.js`**

Implemented as a Node.js subprocess running `examples/eval-pyodide/runner.js`.
The Go dispatcher translates `FUNCTION_INTERFACE=pyodide` into the existing
JSON-RPC stdio supervisor path.

Pyodide supports two modes:

1. **Legacy script mode** — set `FUNCTION_PYODIDE_SCRIPT=/path/to/eval.py` or
   pass the script path as the runner argument. The script must define
   `evaluation_function(response, answer, params)` and may define
   `preview_function`.
2. **Lambda Feedback package mode** — set:
   - `FUNCTION_PYODIDE_ROOT=/path/to/evaluator/root`
   - `FUNCTION_PYODIDE_EVAL_ENTRYPOINT=evaluation_function.evaluation:evaluation_function`
   - optional `FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT=evaluation_function.preview:preview_function`
   - `FUNCTION_PYODIDE_ADAPTER=/path/to/examples/lambda-feedback-adapter/lf_compat_adapter.py`
   - optional `FUNCTION_PYODIDE_PACKAGES=sympy,scipy,...`

Package mode mirrors the evaluator root into the Pyodide virtual filesystem,
mirrors the adapter directory so the minimal `lf_toolkit` shim is importable,
loads the entrypoints once, and dispatches requests through
`lf_compat_adapter.call_function()` and `normalize_result()`.

**Isolation:** Legacy script mode executes the script in a fresh namespace per
request. Package mode keeps imported package modules loaded for performance;
evaluators should not rely on mutable global state being reset between requests.
Use reactor-python single-file mode when snapshot/restore isolation is the
primary requirement and the evaluator fits its package constraints.

**Use case:** Default compatibility path for real Lambda Feedback Python
packages and for heavy scientific dependencies available in Pyodide (SciPy,
Pandas, SymPy, etc.).

## The Compilation Cache

Cold compilation of `python-reactor.wasm` takes 1-3 minutes on modern hardware.
`FUNCTION_WASM_COMPILE_CACHE` enables wazero's on-disk cache
(`wazero.NewCompilationCacheWithDir`). On cache hit, compilation is nearly
instantaneous. The cache is safe to share between multiple processes and
container restarts as long as the same wazero version and module bytes are
used.

```bash
FUNCTION_WASM_COMPILE_CACHE=/var/cache/shimmy/wazero \
FUNCTION_INTERFACE=reactor-python \
FUNCTION_COMMAND=/opt/shimmy/python-reactor.wasm \
./shimmy serve
```

For `python.wasm` (command mode) compilation takes 1-2 s cold. The same cache
variable applies.

## Pool Startup Behaviour for Python

All three Python dispatchers initialise pool runners in parallel:

```go
var wg sync.WaitGroup
wg.Add(poolSize)
for i := 0; i < poolSize; i++ {
    go func() {
        defer wg.Done()
        runner := NewResidentPythonRunner(...)
        if err := runner.Init(ctx); err != nil { /* record error */ }
    }()
}
wg.Wait()
```

If any runner fails to initialise, all successfully-started runners are shut
down and `Start` returns an error. This prevents a partially-populated pool
from serving requests with reduced capacity silently.

## Summary Table

| Property | Per-Request | Resident | Reactor | Pyodide |
|---|---|---|---|---|
| `FUNCTION_INTERFACE` | `wasm` | `python-wasm` | `reactor-python` | `pyodide` |
| Binary/process | `python.wasm` | `python.wasm` | `python-reactor.wasm` | Node.js + Pyodide |
| Instances per pool | 1 per request | persistent, pooled | persistent, pooled (max 4) | subprocess worker |
| CPython startup cost | per request (~7 s) | once per pool slot | once per pool slot | once per worker |
| Per-request latency | ~160 ms | ~10-50 ms | ~1-5 ms + restore | highest; depends on Pyodide/packages |
| Heap isolation | full (fresh instance) | namespace only | full (snapshot/restore) | legacy script: namespace; package mode: persistent imports |
| `sys.modules` reset | yes | eviction pass | yes (snapshot) | no in package mode |
| Package-style LF evaluator support | no | no | via generated single-file bundle | yes |
| Max pool size | N/A | 8 | 4 | supervisor config |
| Memory per slot | none (transient) | ~242 MB | ~100 MB | Pyodide runtime + packages |
