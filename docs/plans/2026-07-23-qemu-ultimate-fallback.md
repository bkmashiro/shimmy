# QEMU Transparent Ultimate Fallback Implementation Plan

> **For Hermes:** Execute this plan task-by-task. The controller owns architecture, transparent-compatibility semantics, Imperial/AWS operations, evidence review, and the final verdict. Bounded implementation may be delegated; deployment, publication, remote side effects, and promotion may not.

**Status:** implementation, reproducible image build, GitHub-hosted x86_64 TCG parity, and an Imperial DoC TCG file profile are complete. The verdict remains **PARTIAL** because real AWS Lambda TCG qualification has not run and the DoC experiment account cannot access KVM.

**Goal:** Add QEMU as a transparent wrapper around Shimmy's existing native worker path, analogous to the optional DynamoRIO wrapper. QEMU must take over the complete evaluator process stack without introducing a new evaluator interface, language/package allowlist, or evaluator source change.

**Architecture:** Keep `FUNCTION_INTERFACE=file|rpc` and every existing RPC transport. When `FUNCTION_QEMU_ENABLED=true`, rewrite the worker `StartConfig` so the Host launches `shimmy-qemu-runner`, which boots a full Linux guest and runs the complete original command/cwd/args/env inside it. The existing supervisor and HTTP layers continue to see the same file/RPC worker contract. QEMU is deployment-selected before execution; it never replays an ambiguously started request after another backend fails.

**Tech Stack:** Go, current Shimmy supervisor/worker contracts, `qemu-system-x86_64`, TCG/KVM capability detection, read-only Linux/evaluator images, virtio-serial control/data tunnels, GitHub Actions, Imperial DoC batch hosts, existing CodeBuild/Lambda probe patterns.

**Current evidence:** [machine-readable QEMU fallback evidence](../qemu-fallback-evidence.json). GitHub Actions run [`30042170032`](https://github.com/bkmashiro/shimmy-wasm-go/actions/runs/30042170032) passed two-build artifact reproducibility, public file-response parity, and persistent RPC parity for stdio, IPC, TCP, HTTP and WebSocket at commit `82e2370f9df667b105deb207ba5333ff26994c53`. The DoC profile passed three native/QEMU response comparisons under TCG. Its latency samples are fresh-file/fresh-VM diagnostic evidence, not a production SLO. KVM did not fall back to TCG: it failed before launch because `/dev/kvm` was unavailable to the account. No AWS resources were written.

---

## 1. Correct product boundary

This work belongs to:

```text
implementation: this repository
mechanism/Lambda probes: a separately controlled qualification harness
accepted evidence: reviewed, version-bound project evidence artifacts
```

It does not belong to Agent Python Runtime.

Shimmy's deployment portfolio remains conceptually:

```text
generic WASM / python-reactor / Pyodide when suitable
native file or RPC worker, optionally wrapped by DynamoRIO
original file or RPC worker transparently rehosted by QEMU alone as the terminal fallback
```

“Terminal fallback” means the deployment chooses the QEMU wrapper when ordinary Host execution is unsuitable or unavailable. It does not mean:

```text
start request on backend A
observe timeout/error
silently execute the same request again in QEMU
```

Such replay could duplicate evaluator side effects. QEMU takeover occurs before the worker starts, exactly like DBI command wrapping.

## 2. No new execution interface

Do **not** add:

```text
FUNCTION_INTERFACE=qemu
```

Keep the current interface and transport unchanged:

```text
FUNCTION_INTERFACE=file
```

or:

```text
FUNCTION_INTERFACE=rpc
FUNCTION_RPC_TRANSPORT=stdio|ipc|tcp|http|ws
```

Enable the wrapper separately:

```text
FUNCTION_QEMU_ENABLED=true
```

This mirrors the current DynamoRIO design:

- DBI is not an IO interface;
- QEMU is not an IO interface;
- both transform the worker command boundary;
- neither changes Shimmy's HTTP request/result schema;
- neither requires evaluator source changes.

## 3. Transparent takeover invariants

With QEMU enabled, all of the following remain semantically unchanged from the unwrapped worker configuration:

- `FUNCTION_INTERFACE`;
- `FUNCTION_COMMAND` as the command executed inside the guest;
- `FUNCTION_ARGS` and ordering;
- `FUNCTION_WORKING_DIR` inside the guest;
- the current effective worker environment inside the guest: Host `os.Environ()`, explicit `FUNCTION_ENV`, and adapter-added `EVAL_*`, with only QEMU wrapper-private control variables removed;
- `FUNCTION_WORKER_SEND_TIMEOUT` and stop timeout;
- `FUNCTION_MAX_PROCS` worker concurrency;
- `file` transient lifecycle;
- `rpc` persistent lifecycle outside Lambda; the owner-approved Lambda
  isolation profile may intentionally narrow RPC to one runner/VM per request;
- RPC transport choice and endpoint semantics;
- `eval`, `preview`, `healthcheck` and result/error envelopes;
- evaluator stdout/stderr and exit behavior, subject only to the transport's existing treatment.

There is no evaluator-language, package, entrypoint, ABI, Python/Lean, import, or syscall allowlist in the QEMU wrapper. Python and Lean are test fixtures, not support boundaries.

Operational artifacts still have to exist: the selected guest image must contain the configured command, interpreter, shared libraries, data, cwd and explicit environment dependencies at their declared guest paths. That is deployment packaging, equivalent to putting those artifacts into the current Lambda/container image; it is not an evaluator-code restriction.

## 4. Command transformation

### Original worker

Example native file worker:

```text
Cmd:  python3
Cwd:  /opt/evaluator
Args: [evaluation.py]
Env:  [MODEL_PATH=/opt/evaluator/model.bin]
IO:   file
```

### QEMU transformation

QEMU wraps the original worker directly:

```text
Cmd: shimmy-qemu-runner
Args:
  [
    --runtime-config, <private-config-descriptor>,
    --,
    <original Cmd>,
    <original Args...>
  ]
```

QEMU and DynamoRIO are alternative wrappers:

```text
native/DBI lane: original evaluator → optional DynamoRIO wrapper
QEMU lane:       original evaluator → QEMU wrapper
```

If both `FUNCTION_DBI_SECURITY_ENABLED=true` and `FUNCTION_QEMU_ENABLED=true` are configured, startup fails before the worker starts and tells the operator to select one wrapper. QEMU does not require DynamoRIO, a DBI client, or a DBI policy inside or outside the guest.

`applyQEMUFallbackConfig` must:

- leave `cfg.IO.Interface` and all RPC transport config unchanged;
- capture the original command/cwd/args/env;
- replace only the Host process command with `shimmy-qemu-runner`;
- avoid secrets in argv/logs;
- preserve argument boundaries without a shell;
- remain a no-op when disabled.

## 5. Lifecycle follows the existing interface unless isolation is selected

QEMU preserves the existing lifecycle by default outside Lambda. The
owner-approved Lambda isolation profile is an explicit exception: QEMU RPC
defaults to lazy one-shot VM ownership so mutable guest state never crosses
invocations.

### `file`

Current `file` semantics are transient: the supervisor starts a worker during each `Send`, appending Host request and response paths. The QEMU runner therefore:

1. receives those Host file paths;
2. reads the existing request envelope;
3. boots one QEMU guest for that worker invocation;
4. creates guest request/response paths;
5. runs the original evaluator command with guest paths appended;
6. returns the guest response into the original Host response file;
7. exits and destroys the VM.

This naturally gives one VM per file request because that is already the file-worker lifecycle.

### `rpc`

With reset policy `off`, current RPC semantics are persistent: the supervisor starts one worker and reuses it. The QEMU runner therefore:

1. boots one guest when the RPC worker starts;
2. starts the original RPC evaluator inside the guest;
3. bridges the selected transport for the lifetime of that worker;
4. preserves multiple requests and evaluator state exactly as the native RPC path does;
5. destroys the VM when the worker stops, is cancelled or times out.

With reset policy `lazy`, the supervisor deliberately treats RPC as
invocation-scoped without changing the RPC transport contract. It does not boot
at Shimmy init, stops and waits for the runner after every successful or failed
request, and boots the next clean VM only when another request arrives. Lambda
defaults to this policy; non-Lambda deployments default to `off`.

## 6. Transparent transport bridge

Use virtio-serial as the internal Host/guest tunnel. This is an implementation detail, not a new public evaluator protocol.

### File transport

The Host runner translates only filesystem location:

```text
Host request/response file
↔ length-bounded internal frame
↔ guest tmpfs request/response file
```

The evaluator still receives the same JSON and two path arguments plus the same `EVAL_IO`, `EVAL_FILE_NAME_REQUEST`, and `EVAL_FILE_NAME_RESPONSE` meanings, with paths valid inside the guest.

### RPC stdio

The runner presents a normal duplex stdio pipe to Shimmy and tunnels the raw stream to the guest evaluator's stdin/stdout. Do not decode/re-encode evaluator JSON-RPC when a byte stream is sufficient.

### RPC IPC/TCP/HTTP/WS

The runner exposes the same Host endpoint expected by the existing supervisor. A generic multiplexed byte-stream bridge carries accepted connections over virtio-serial. The guest bridge connects to the evaluator's corresponding guest-local endpoint:

- IPC path exists independently inside the guest;
- loopback TCP/HTTP/WS addresses have the same value inside the guest;
- HTTP and WebSocket are tunneled as raw TCP streams, not reimplemented;
- connection open/data/half-close/close/error states are preserved;
- multiple concurrent connections receive independent stream IDs and bounded buffers.

This supports every currently advertised RPC transport without evaluator changes.

## 7. Environment, cwd and filesystem mapping

Transparent execution requires the current effective worker configuration inside the guest:

- original command path;
- original cwd;
- original args;
- Host ambient environment inherited by `ProcessWorker` today, plus exact explicit `FUNCTION_ENV` entries in the same override order;
- transport environment produced by the current adapter;
- file request/response environment translated to guest paths.

`shimmy-qemu-runner` therefore forwards the environment it actually inherits after the current `ProcessWorker` assembly, preserving duplicate-key/last-value behavior, and removes only wrapper-private `FUNCTION_QEMU_*`/internal control entries that did not belong to the original evaluator. This deliberately preserves the existing native path's ambient environment, including any deployment credentials; tightening that authority would be a separate all-backend security change, not a QEMU transparency change.

The deployment image builder preserves evaluator paths inside the guest. It may use:

- one sealed rootfs containing the evaluator; or
- a sealed base Linux rootfs plus a read-only evaluator block image mounted at the paths recorded in its manifest.

No evaluator source rewrite, special SDK, Python adapter, Lean adapter, or WASI ABI is required.

Writable locations, external network, DNS and credentials are controlled by the deployment profile, not hardcoded as QEMU compatibility restrictions. The QEMU layer must be able to mirror the intended native evaluator environment. Security/authority claims must describe the selected deployment profile rather than pretending all QEMU deployments are networkless sandboxes.

## 8. QEMU runtime configuration

Planned wrapper-specific environment:

```text
FUNCTION_QEMU_ENABLED=true
FUNCTION_QEMU_RUNNER=/opt/shimmy/bin/shimmy-qemu-runner
FUNCTION_QEMU_BINARY=/opt/qemu/bin/qemu-system-x86_64
FUNCTION_QEMU_ROOTFS=/opt/shimmy-qemu/evaluator.squashfs
FUNCTION_QEMU_IMAGE_MANIFEST=/opt/shimmy-qemu/image-manifest.json
FUNCTION_QEMU_ACCELERATOR=tcg|kvm|auto
FUNCTION_QEMU_MEMORY_MB=<bounded integer>
FUNCTION_QEMU_VCPUS=<bounded integer>
FUNCTION_QEMU_BOOT_TIMEOUT=<duration>
FUNCTION_QEMU_WORK_ROOT=/tmp/shimmy-qemu
FUNCTION_QEMU_NETWORK_PROFILE=inherit|none
```

Rules:

- existing worker timeouts and `FUNCTION_MAX_PROCS` remain canonical;
- QEMU and DBI are mutually exclusive; enabling both fails closed before worker startup;
- `auto` is experiment-only and records the selected accelerator;
- Lambda uses explicit TCG unless the exact target proves KVM;
- missing binary/image/manifest/digest fails before the worker starts;
- the runner verifies the artifact chain before QEMU execution;
- image paths and optional network profile are deployment configuration, not evaluator compatibility filters;
- no wrapper option silently alters the evaluator command, cwd, args, explicit env, IO interface or transport.

## 9. Generic QEMU runner boundary

The Host executable is:

```text
cmd/shimmy-qemu-runner
```

It must emulate the process surface expected by the current `worker.ProcessWorker`:

- starts synchronously enough for existing RPC retry logic;
- provides stdin/stdout/stderr or Host endpoints as selected;
- returns the evaluator-equivalent exit/error outcome;
- handles SIGTERM and process-group cancellation;
- kills only its owned QEMU/bridge descendants;
- removes only its own request-scoped sockets/config/work directories;
- emits structured lifecycle metrics without logging evaluator secrets;
- never invokes a shell to reconstruct the original argv.

The guest bridge/agent starts the complete inner command exactly once for RPC and exactly once per file invocation.

## 10. Artifact and storage boundary

### Repository roles

```text
this repository:
  wrapper/config implementation, Host runner, guest bridge, image contracts, GHA and DoC harness

separately approved qualification harness:
  real CodeBuild/Lambda packaging and target probe

reviewed project evidence:
  accepted canonical evidence and final bounded interpretation
```

### Imperial DoC storage

```text
<approved DoC project scratch>/shimmy/qemu-fallback/
├── sources/
├── images/
│   ├── base/
│   └── evaluator/
├── work/<shimmy-commit>/
└── runs/<run-id>/
```

All DoC downloads, generated images and raw results go under this root, never DoC home. Scripts must require the root explicitly, reject path/symlink escape, enforce a declared cache cap, and clean only named experiment directories.

`shell2` remains control/download only. QEMU executes on `batch1`/`batch2` or another approved compute target. SSH remains strict and CA-backed.

## 11. Evidence environments

### GitHub Actions

Push and manual `workflow_dispatch` smoke prove:

- command/config transformation;
- file parity;
- RPC parity for stdio, IPC, TCP, HTTP and WS;
- generic evaluator fixtures without source changes;
- explicit TCG selection;
- timeout/crash cleanup;
- no stable performance claim.

### Imperial DoC

DoC evidence compares the same evaluator configuration with only QEMU toggled:

```text
baseline: FUNCTION_QEMU_ENABLED=false
candidate: FUNCTION_QEMU_ENABLED=true
```

Hold constant:

- Shimmy commit;
- `FUNCTION_INTERFACE` and RPC transport;
- command/cwd/args/explicit env;
- evaluator bundle;
- request corpus;
- concurrency and timeouts.

Record full HTTP, worker startup, VM ready, evaluator and teardown times separately. Keep KVM and TCG results separate.

### AWS Lambda x86_64

Real Lambda TCG is the authoritative deployment gate. It requires separate approval before CodeBuild/ECR/Lambda writes or paid runs.

The target probe must show that the same Lambda Feedback evaluator image/config works with QEMU enabled, including persistent RPC behavior where selected. GHA/DoC cannot substitute for this result.

## 12. Acceptance

### VALIDATED AS TRANSPARENT TERMINAL FALLBACK

All are required:

- QEMU is enabled as a wrapper, not a new IO interface;
- disabled mode leaves `StartConfig` byte-for-byte/semantically unchanged;
- enabled mode preserves `file` and `rpc` interface selection;
- stdio, IPC, TCP, HTTP and WS RPC transports retain equivalent behavior;
- original command/cwd/args/effective worker environment execute inside the guest without evaluator source changes;
- file remains transient; RPC remains persistent outside Lambda and becomes
  invocation-scoped under the owner-approved Lambda lazy-reset profile;
- Python and Lean are parity fixtures, not allowlisted runtimes;
- QEMU runs the original evaluator without a DBI dependency or nested DBI layer;
- evaluator result/error/exit/timeout behavior remains equivalent at Shimmy's public boundary;
- `FUNCTION_MAX_PROCS` and cancellation still bound worker/VM ownership;
- process, socket, bridge and work-directory cleanup passes under success, error, timeout and cancellation;
- GHA, DoC and real Lambda TCG evidence pass with exact artifact identities;
- docs call the path a transparent terminal compatibility fallback, not an automatically replayed request or a universal security proof.

### PARTIAL

Use when a transport, target environment, evaluator parity case or cleanup proof remains incomplete. Do not document a transport-independent transparent takeover while PARTIAL.

### INVALIDATED

Use when the wrapper requires evaluator source changes, changes interface/lifecycle/result semantics, cannot bridge an advertised transport, cannot preserve the inner process configuration, or cannot execute within the configured Lambda envelope.

### Natural stop

The owner decision on 2026-07-24 opened bounded one-slot Lambda request
isolation and snapshot/reset investigation. Automatic request replay,
distributed scheduling and unrelated runtime work remain out of scope.

---

## Task 0: Correct the plan contract

**Files:**
- Rewrite: `docs/plans/2026-07-23-qemu-ultimate-fallback.md`
- Modify: `README.md`

**Steps:**

1. Remove the proposed `FUNCTION_INTERFACE=qemu` model.
2. Remove mandatory one-VM-per-request semantics for RPC.
3. Define transparent `file`/`rpc` takeover and full transport parity.
4. Keep QEMU deployment-selected before execution; prohibit ambiguous request replay.
5. Run docs/link/diff gates.
6. Signed docs-only commit and push.

## Task 1: Specify and test wrapper transformation

**Files:**
- Create: `internal/execution/dispatcher_qemu.go`
- Create: `internal/execution/dispatcher_qemu_test.go`
- Modify: `internal/execution/dispatcher.go`

**TDD:**

1. RED: disabled/unset wrapper leaves config unchanged.
2. RED: invalid boolean, runner/image/binary/manifest/accelerator/resource config fails closed.
3. RED: file and every RPC transport retain their `IOConfig` exactly.
4. RED: command/cwd/args and effective `os.Environ()+FUNCTION_ENV+EVAL_*` order are preserved without shell flattening or secret logging; only wrapper-private QEMU controls are removed.
5. RED: enabling DBI and QEMU together fails before worker startup; QEMU wraps only the original worker command.
6. GREEN: implement `applyQEMUFallbackConfig` adjacent to `applyDBISecurityConfig`.
7. Focused and full execution tests.
8. Commit: `feat(qemu): define transparent worker wrapper`.

## Task 2: Define the internal stream tunnel

**Files:**
- Create: `internal/execution/qemurun/protocol.go`
- Create: `internal/execution/qemurun/protocol_test.go`
- Create shared golden fixtures under `internal/execution/qemurun/testdata/`

**TDD:**

1. RED: control hello/ready/exit, file request/result, stream open/data/half-close/close/error and cancellation frames.
2. RED: stream IDs, bounded frame lengths, backpressure, ordering, duplicate/unknown IDs, truncation and malformed frames.
3. Implement a length-bounded multiplexed virtio-serial protocol.
4. Fuzz decoders and connection-state machines.
5. Commit: `feat(qemu): add transparent stream tunnel`.

## Task 3: Implement the generic guest bridge

**Files:**
- Create: `experiments/qemu-fallback/guest-bridge/`
- Create tests and native fixtures in the same subtree

**TDD:**

1. RED: restore exact inner command/cwd/args/explicit env.
2. RED: file mode translates only request/response paths and preserves payload/exit behavior.
3. RED: RPC stdio byte bridge preserves framing.
4. RED: IPC/TCP/HTTP/WS raw stream bridges preserve connect/data/half-close/close behavior.
5. RED: persistent RPC evaluator starts exactly once; file evaluator starts once per invocation.
6. GREEN: implement static Linux amd64 guest bridge.
7. Native bridge parity tests before QEMU.
8. Commit: `feat(qemu): add generic guest worker bridge`.

## Task 4: Implement the Host QEMU runner

**Files:**
- Create: `cmd/shimmy-qemu-runner/main.go`
- Create: `internal/execution/qemurun/config.go`
- Create: `internal/execution/qemurun/command.go`
- Create: `internal/execution/qemurun/process_linux.go`
- Create: `internal/execution/qemurun/process_stub.go`
- Create focused tests

**TDD:**

1. RED: typed QEMU argv, no shell, exact artifact checks and bounded resources.
2. RED: Host endpoint parity for every RPC transport.
3. RED: file Host path translation and response writeback.
4. RED: startup/request/stop timeout, QEMU crash, bridge crash, cancellation and signal escalation.
5. RED: no orphan process group/socket/config/work directory under any exit path.
6. GREEN: implement runner and non-Linux unsupported stub.
7. Race, build and process fault-injection gates.
8. Commit: `feat(qemu): add transparent vm runner`.

## Task 5: Build reproducible full-Linux artifacts

**Files:**
- Create: `experiments/qemu-fallback/sources.lock.json`
- Create: `experiments/qemu-fallback/image-manifest.schema.json`
- Create: `experiments/qemu-fallback/scripts/`
- Create: `experiments/qemu-fallback/README.md`

**TDD:**

1. RED: reject mutable sources, bad hashes, missing licenses, path escape and cache overflow.
2. Pin Debian-family base, package snapshot, kernel/firmware, guest bridge and evaluator bundle identities.
3. Preserve configured evaluator paths and dependencies inside the guest.
4. Support the same artifact builder for Python, Lean and arbitrary supplied evaluator bundles without source adapters.
5. Verify image manifest before and after preparation.
6. Commit: `build(qemu): lock transparent fallback images`.

## Task 6: Prove native-vs-QEMU parity locally/GHA

**Files:**
- Create: `experiments/qemu-fallback/parity/`
- Create: `.github/workflows/qemu-fallback.yml`

**Matrix:**

- `file` × Python/Lean;
- `rpc-stdio` × Python/Lean;
- `rpc-ipc` × representative evaluator;
- `rpc-tcp` × representative evaluator;
- `rpc-http` × representative evaluator;
- `rpc-ws` × representative evaluator.

For each row, run the same HTTP corpus with QEMU off/on and compare schema-normalized results, errors, lifecycle and state behavior. Manual workflow only during the spike.

Commit: `ci(qemu): prove transparent fallback parity`.

## Task 7: Run DoC same-config profile

**Files:**
- Create: `experiments/qemu-fallback/scripts/probe_host.py`
- Create: `experiments/qemu-fallback/scripts/run_doc_profile.py`
- Create: `experiments/qemu-fallback/evidence.schema.json`

**Controller procedure:**

1. Strict CA-backed SSH to the less-loaded permitted batch host.
2. Keep downloads/images/results under `<approved DoC project scratch>/shimmy/qemu-fallback/`.
3. Hold evaluator and Shimmy config constant; toggle only `FUNCTION_QEMU_ENABLED` plus required runtime artifacts.
4. Run correctness before performance; begin N=1, then N=2 if load/memory/cleanup gates pass.
5. Record KVM and TCG separately, with three fresh process/VM repeats.
6. Validate evidence locally before committing bounded raw results.

## Task 8: Qualify real Lambda x86_64 TCG

**Files:**
- Create the target probe in the separately approved qualification harness.
- Add accepted evidence here only after validation.

**Precondition:** explicit owner approval for AWS writes/cost.

**Steps:**

1. Build exact digest-bound container with Shimmy, QEMU, guest base and evaluator bundle.
2. Run the same baseline/candidate evaluator config on Lambda x86_64.
3. Cover file and representative persistent RPC path through real Shimmy HTTP.
4. Verify timeout/crash cleanup and successful subsequent invocation.
5. Record cold/warm, VM ready, evaluator, teardown, HTTP, memory and artifact-size evidence.
6. Redact credentials and verify resource cleanup/retention state.

## Task 9: Verdict and documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/deployment-recipes.md`
- Create or modify: `docs/qemu-final-fallback.md`
- Update this plan and the checked-in evidence ledger

**Steps:**

1. Classify VALIDATED/PARTIAL/INVALIDATED using Section 12.
2. Document the wrapper as analogous to DBI and preserve existing interface/transport recipes.
3. Add no evaluator-language restrictions unsupported by actual parity evidence.
4. Keep automatic request replay explicitly out of scope.
5. Run full Go/race/build/workflow/docs/evidence gates.
6. Signed small commits, push, verify remote signatures and clean worktrees.
7. Stop unless a new owner decision opens optimization or deployment work.

## Task 10: Lambda one-slot request isolation (owner-approved)

**Decision:** one QEMU slot per Lambda execution environment; default reset
timing is lazy clean-on-borrow. Never replay a request after ambiguous Guest
execution.

**Implemented slice:**

1. `WorkerLifecycleInvocation` decouples worker reuse from the selected RPC
   transport.
2. Lambda QEMU defaults `FUNCTION_QEMU_RESET_POLICY` to `lazy`; non-Lambda
   defaults to `off`.
3. Lazy RPC does not preboot during Init, synchronously stops/waits before
   `Send` returns after both success and failure, and boots only when the next
   request actually arrives. The supervisor holds the slot until process exit.
4. The dedicated dispatcher releases failed workers before returning the error.
5. On Linux, QEMU receives `Pdeathsig=SIGKILL` so a force-killed runner cannot
   leave its independently grouped VM orphaned.
6. GitHub Actions run `30133144075` passed on x86_64 TCG. The lazy stdio smoke
   observed distinct Guest boot IDs
   `1c711cd9-00ae-413b-b75e-306aa96968f8` and
   `00ba30e7-539f-49ee-b6f2-9de70ea8c63e`; the check run reported no
   annotations after moving official actions to their Node 24 majors.

**Remaining slices:**

1. Add a nonce-aware quiescent Guest checkpoint protocol and QMP control.
2. Qualify fresh-QEMU immutable snapshot restore before exposing it as a reset
   strategy.
3. Treat same-process `loadvm` as a separately qualified weaker-assurance mode.
4. Add a custom Lambda Runtime API loop only if explicit eager reset is enabled;
   default lazy does not require post-response work.
