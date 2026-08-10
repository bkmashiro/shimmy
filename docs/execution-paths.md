# Execution paths

Shimmy keeps the existing `rpc` and `file` process interfaces and adds explicit,
opt-in runtime paths. Selection is configuration-driven; Shimmy does not inspect
source files or silently retry a request under a different backend.

## Generic WebAssembly

```bash
FUNCTION_INTERFACE=wasm
FUNCTION_WASM_PROFILE=generic
FUNCTION_WASM_MODULE=/opt/evaluator/evaluator.wasm
```

The module runs in-process under wazero and exports `memory`, `alloc`, and
`dispatch`. Shimmy copies the request into guest linear memory, copies the
response out, and restores the prepared memory state before reusing the
instance.

## Python Reactor

```bash
FUNCTION_INTERFACE=wasm
FUNCTION_WASM_PROFILE=python-reactor
FUNCTION_WASM_MODULE=/opt/runtime/python-reactor.wasm
FUNCTION_WASM_MANIFEST=/opt/runtime/manifest.json
FUNCTION_WASM_PYTHON_SCRIPT=/opt/evaluator/evaluator.py
```

The manifest, artifact digest, imports, exports, function signatures, producer
identity, and declared Python modules are verified before startup. Canonical
Shimmy-produced artifacts use `shimmy_python_init`, `shimmy_python_prepare`, and
`evaluate`; the previous `runtime_init`, `runtime_prepare`, and `execute` ABI
remains accepted for existing pinned artifacts. The prepared script exposes
`evaluation_function`/`preview_function` for the producer ABI and `dispatch` for
the compatibility ABI.

Producer profiles are capability-specific: `base` contains the standard
library, `numpy-core` adds NumPy, and `sympy` adds pure-Python SymPy and mpmath.
SciPy and Pandas remain on the Pyodide compatibility path.

On Linux, run the full HTTP startup and request-flow check against a real
Producer artifact and manifest:

```bash
SHIMMY_PYTHON_REACTOR_WASM=/opt/runtime/python-reactor.wasm \
SHIMMY_PYTHON_REACTOR_MANIFEST=/opt/runtime/manifest.json \
  scripts/e2e-python-reactor.sh
```

The check starts Shimmy with the configuration above, sends two `eval` requests
and one `preview` request, and verifies that prepared guest state is restored
between requests.

Package-shaped evaluators remain external inputs. Build a single trusted script
with [`lf-bundle-python`](../tools/lf-bundle-python/README.md), then point
`FUNCTION_WASM_PYTHON_SCRIPT` at that output. The bundler embeds reachable
pure-Python modules, reads artifact-provided modules from the bound manifest,
and fails before writing output when a required module is unresolved.
`scripts/e2e-python-reactor-lf-packages.sh` exercises pinned external
repositories against the base, NumPy, and SymPy profiles without copying their
source into this repository.

## Pyodide compatibility

```bash
FUNCTION_INTERFACE=pyodide
FUNCTION_PYODIDE_RUNNER=/opt/shimmy/pyodide/runner.js
FUNCTION_PYODIDE_SCRIPT=/opt/evaluator/evaluator.py
```

Package mode uses `FUNCTION_PYODIDE_ROOT` together with
`FUNCTION_PYODIDE_EVAL_ENTRYPOINT`. Pyodide remains a Node.js subprocess using
framed JSON-RPC over stdio; it is not a wazero profile. Evaluators requiring
SciPy or other unported native-extension stacks select this path explicitly.
The Linux smoke test installs the locked Pyodide Node package and executes a
real SciPy function through Shimmy HTTP:

```bash
npm ci --prefix examples/eval-pyodide
scripts/e2e-pyodide-scipy.sh
```

### Isolated Python evaluation example

[`examples/safe-eval-python`](../examples/safe-eval-python/README.md) is a
backend-level Python Reactor example for executing student Python in `demo`,
`io_test`, `unit_test`, and `preview` modes. It uses wazero's WASM capability
boundary, request deadline, manifest integrity/ABI validation, and snapshot reset; it
does not depend on nsjail, privileged Lambda configuration, Node, or Docker.

Manifest validation checks digest and capability consistency. Artifact
authenticity, trusted Producer commit policy, and digital-signature verification
remain deployment-system responsibilities.

Source checks are feedback and defense in depth, not the sandbox. Student code
that needs NumPy or SymPy selects the corresponding manifest-validated Reactor artifact;
trusted SciPy evaluator logic remains on the Pyodide compatibility path.
Backend selection is explicit, and Shimmy never redirects an existing evaluator
to this example automatically:

```bash
SHIMMY_PYTHON_REACTOR_WASM=/path/to/base.wasm \
SHIMMY_PYTHON_REACTOR_MANIFEST=/path/to/base.manifest.json \
scripts/e2e-safe-eval-python.sh
```

## DBI security wrapper

```bash
FUNCTION_INTERFACE=rpc              # or file
FUNCTION_COMMAND=/opt/evaluator/worker
FUNCTION_DBI_SECURITY_ENABLED=true
FUNCTION_DBI_DRRUN=/opt/dynamorio/bin64/drrun
FUNCTION_DBI_CLIENT=/opt/dynamorio/lib64/release/libclient.so
```

DBI transparently wraps the native worker command without changing its RPC or
file protocol. It is rejected for WASM and Pyodide interfaces.

## QEMU Linux fallback

```bash
FUNCTION_INTERFACE=rpc              # or file
FUNCTION_COMMAND=/opt/evaluator/worker
FUNCTION_QEMU_ENABLED=true
FUNCTION_QEMU_RUNNER=/opt/shimmy/bin/shimmy-qemu-runner
FUNCTION_QEMU_BINARY=/usr/bin/qemu-system-x86_64
FUNCTION_QEMU_ROOTFS=/opt/shimmy-qemu/evaluator.squashfs
FUNCTION_QEMU_IMAGE_MANIFEST=/opt/shimmy-qemu/manifest.json
FUNCTION_QEMU_ACCELERATOR=tcg       # or kvm; must be explicit
FUNCTION_QEMU_RESET_POLICY=lazy     # or off
```

The runner verifies the image manifest and bridges the existing file or RPC
protocol through the guest. `lazy` uses a fresh worker for each invocation;
Lambda defaults to this policy. There is no silent KVM-to-TCG fallback.

DBI and QEMU are mutually exclusive. A failed native or DBI request is never
retried under QEMU.
