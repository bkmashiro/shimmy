# QEMU Ultimate Fallback Implementation Plan

> **For Hermes:** Use the spike workflow and execute this plan task-by-task. The controller owns architecture, routing semantics, all Imperial/AWS operations, evidence review, and the final verdict. Bounded implementation work may be delegated; remote execution, deployment, publication, and promotion may not.

**Status:** planned after an explicit supervisor request; no implementation or qualification claim exists yet.

**Goal:** Add and qualify a full Linux QEMU lane as Shimmy's explicit last-resort compatibility fallback for evaluators that cannot run through generic WASM, python-reactor, Pyodide, or the bounded native/DBI path.

**Architecture:** Add `FUNCTION_INTERFACE=qemu` as a distinct execution boundary. For the correctness baseline, each Shimmy request launches one disposable `qemu-system-x86_64` VM from a hash-bound read-only Linux/evaluator image, exchanges one bounded `eval`/`preview` envelope with a guest agent over virtio-serial, then destroys the entire VM. GitHub Actions provides manual functional smoke, Imperial DoC batch servers provide controlled x86 mechanism/performance evidence, and real AWS Lambda x86_64 under TCG is the authoritative deployment gate.

**Tech Stack:** Go, `qemu-system-x86_64`, TCG/KVM capability detection, qcow2/read-only guest images, virtio-serial, pinned Debian-family Linux inputs, current Shimmy dispatcher/HTTP contracts, GitHub Actions, Imperial DoC batch hosts, existing CodeBuild/Lambda probe patterns.

---

## 1. Correct project and product boundary

This work belongs to:

```text
implementation: ~/projects/shimmy-wasm
mechanism/Lambda probes: ~/projects/shimmy-sandbox-prototypes
canonical accepted evidence: ~/projects/shimmy-docs/docs/current/evidence
```

It does **not** belong to Agent Python Runtime. That project remains a WASI-focused Agent execution infrastructure project and must not gain a QEMU backend from this supervisor request.

Shimmy's current portfolio is:

```text
generic WASM
  → python-reactor for fresh Python within the pinned WASI package set
  → Pyodide for a wider Emscripten/Python package set
  → native file/RPC plus bounded optional DynamoRIO policy
  → QEMU full Linux as the terminal compatibility fallback
```

“Terminal fallback” describes deployment-time/runtime-portfolio selection. It does **not** authorize retrying a request after another backend has started execution. Retrying `eval` or `preview` after an ambiguous timeout/error could duplicate evaluator filesystem, network, process, licensing, or other external effects.

Therefore:

- `FUNCTION_INTERFACE=qemu` is explicit and fail-closed;
- no `FUNCTION_INTERFACE=auto` is introduced in this plan;
- no request is automatically replayed from WASM/Pyodide/native into QEMU;
- startup qualification may reject an incompatible selected lane before the HTTP server becomes ready;
- a later deployment planner may recommend QEMU only from static artifact/compatibility evidence, never from an already-started request.

## 2. Why full system QEMU

The supervisor's “ultimate fallback” requirement is broader than Python/NumPy. The lane must be able to host an ordinary Linux userspace containing interpreters, native libraries, Lean/native binaries, or evaluator-specific dependencies.

This plan uses `qemu-system-x86_64`, not qemu-user:

- qemu-user is a syscall/ISA compatibility tool and is not a VM isolation boundary;
- a full guest kernel gives a materially different boundary for arbitrary Linux evaluators;
- Shimmy already defines `FUNCTION_INTERFACE` as an execution boundary rather than a language;
- the final target is AWS Lambda x86_64, where KVM is not expected and TCG must be qualified explicitly.

QEMU is still not called a complete production sandbox merely because it boots a VM. QEMU's emulated-device surface, guest-image supply chain, Host process limits, outer Lambda constraints, cleanup, and side effects all remain in scope for evidence.

## 3. Lifecycle baseline

The first correct implementation is one VM per request:

```text
Shimmy HTTP eval/preview
  → QEMU dispatcher Send
  → create request-scoped work directory and Unix control socket
  → launch one QEMU process from verified read-only images
  → wait for bounded guest-ready handshake
  → send one length-prefixed Shimmy envelope over virtio-serial
  → guest agent invokes the configured evaluator
  → receive one bounded result envelope
  → request guest shutdown
  → kill exact QEMU process group if needed
  → verify socket/process/temp cleanup
```

This deliberately pays cold boot cost. It gives a simple freshness claim:

- a new guest kernel and userspace per request;
- no guest process, heap, thread, FD, cache, or writable disk state survives into another request;
- no need to claim in-guest reset completeness.

Out of scope until a separate decision:

- warm reusable VM;
- QEMU `savevm`/`loadvm` restore;
- a pool of prebooted single-use VMs;
- VM migration;
- QEMU guest state capsules;
- request retry/failover between backends.

## 4. Host/guest protocol

### Control transport

Use a custom virtio-serial port named:

```text
org.shimmy.control
```

The Host creates a request-scoped Unix socket beneath its private work directory and attaches it as a QEMU chardev. The guest agent opens:

```text
/dev/virtio-ports/org.shimmy.control
```

No SSH, guest network device, 9p, virtiofs, host directory mount, or inherited Host stdio protocol is required.

### Framing

Each message is:

```text
4-byte unsigned big-endian JSON length
JSON bytes
```

The Host and guest reject:

- zero length;
- lengths above configured request/response caps;
- truncated frames;
- trailing bytes after the single response;
- unknown schema version;
- unknown command;
- malformed JSON.

### Request

```json
{
  "schema_version": 1,
  "request_id": "opaque-bounded-id",
  "command": "eval",
  "params": {
    "response": "student response",
    "answer": "expected answer"
  }
}
```

`command` must be one of `eval`, `preview`, or `healthcheck`, matching the current Shimmy dispatcher contract.

### Response

The guest returns the evaluator's existing Shimmy result map plus bounded lifecycle metadata owned by the QEMU lane. It must not invent DBI/WASI receipts or claim Host capability mediation.

## 5. Guest agent and evaluator contract

The guest agent is a small static Go binary. It does not embed a Python-specific evaluator. Its configuration manifest names an evaluator executable and protocol:

```json
{
  "schema_version": 1,
  "protocol": "shimmy-file-v1",
  "command": "/opt/evaluator/worker",
  "args": [],
  "working_directory": "/opt/evaluator",
  "request_timeout_ms": 30000,
  "max_request_bytes": 1048576,
  "max_response_bytes": 1048576
}
```

For `shimmy-file-v1`, the guest agent mirrors the current `file` adapter:

1. create a request directory on guest tmpfs;
2. write `{"command":...,"params":...}` to a request file;
3. invoke the configured evaluator with request and response paths appended to argv;
4. read and validate the JSON response;
5. delete the request directory;
6. return the bounded response frame.

The sealed guest/evaluator image may contain any ordinary x86-64 Linux runtime. The initial proof matrix includes:

- Python plus NumPy/SciPy-compatible evaluator fixture;
- the pinned Lean native evaluator fixture already used by Shimmy's native/DBI evidence;
- `eval`, `preview`, error, timeout, large-response and freshness canaries.

This is evidence for two runtime families, not a claim that every Linux binary works.

## 6. QEMU command boundary

The command builder must be typed/tested and must not concatenate a shell command. The initial command shape is equivalent to:

```text
qemu-system-x86_64
  -no-user-config
  -nodefaults
  -display none
  -nographic
  -monitor none
  -serial none
  -machine accel=<tcg-or-kvm>
  -smp <bounded-vcpu-count>
  -m <bounded-memory>
  -drive file=<verified-rootfs>,format=qcow2,if=virtio,readonly=on
  -device virtio-serial-pci
  -chardev socket,id=shimmy,path=<private-unix-socket>,server=on,wait=off
  -device virtserialport,chardev=shimmy,name=org.shimmy.control
  -net none
```

Exact boot firmware/direct-kernel inputs are locked in the image manifest. The final Lambda artifact should prefer direct kernel boot if it materially reduces dependencies/startup, but GHA/DoC evidence must not be silently mixed across firmware and direct-kernel modes.

Required boundaries:

- no network device;
- no host filesystem sharing;
- no host environment forwarded into the guest;
- root/evaluator images read-only;
- guest `/tmp`, `/run`, caches and request work on guest tmpfs;
- QEMU runs unprivileged;
- one process group per request;
- hard boot/request/teardown deadlines;
- bounded vCPU/memory/request/response sizes;
- exact image and QEMU identities in evidence;
- QEMU's own seccomp sandbox is capability-detected, not assumed. Current Lambda evidence says application-installed seccomp/no-new-privs is unavailable, so target qualification must not depend on `-sandbox on` succeeding.

## 7. Configuration surface

Planned explicit environment surface:

```text
FUNCTION_INTERFACE=qemu
FUNCTION_QEMU_BINARY=/opt/qemu/bin/qemu-system-x86_64
FUNCTION_QEMU_ROOTFS=/opt/shimmy-qemu/rootfs.qcow2
FUNCTION_QEMU_IMAGE_MANIFEST=/opt/shimmy-qemu/image-manifest.json
FUNCTION_QEMU_EVALUATOR_MANIFEST=/opt/shimmy-qemu/evaluator-manifest.json
FUNCTION_QEMU_ACCELERATOR=tcg|kvm|auto
FUNCTION_QEMU_MEMORY_MB=<bounded integer>
FUNCTION_QEMU_VCPUS=<bounded integer>
FUNCTION_QEMU_BOOT_TIMEOUT=<duration>
FUNCTION_QEMU_SHUTDOWN_TIMEOUT=<duration>
FUNCTION_QEMU_WORK_ROOT=/tmp/shimmy-qemu
FUNCTION_QEMU_MAX_REQUEST_BYTES=<bounded integer>
FUNCTION_QEMU_MAX_RESPONSE_BYTES=<bounded integer>
```

Policy:

- existing `FUNCTION_MAX_PROCS` is the hard upper bound on concurrent QEMU VMs; the dispatcher must acquire capacity before creating a work directory or process and must release it on every exit path;
- `auto` is allowed only in local/GHA/DoC experiments and records the selected accelerator;
- Lambda deployment requires explicit `tcg` unless `/dev/kvm` is positively demonstrated in that exact target;
- missing binary/image/manifest/digest fails startup before serving HTTP;
- DBI wrapping QEMU is rejected; DBI remains a native `file`/`rpc` wrapper;
- QEMU is its own interface and cannot be selected by `FUNCTION_WASM_PROFILE`.

## 8. Artifact and remote storage

### Repository roles

```text
shimmy-wasm:
  implementation, guest agent, image contracts, GHA smoke, DoC harness

shimmy-sandbox-prototypes:
  CodeBuild/Lambda container probe and raw target receipts

shimmy-docs:
  accepted canonical evidence and final bounded interpretation only
```

### DoC Bitbucket layout

```text
/vol/bitbucket/ys25/shimmy/qemu-fallback/
├── sources/
├── images/
│   ├── base/
│   └── sealed/
├── work/<shimmy-commit>/
└── runs/<run-id>/
```

All upstream images, QEMU/rootfs/evaluator bundles, generated images, remote logs, and raw evidence go under this root, not DoC home. Scripts must require `SHIMMY_QEMU_ROOT`, resolve all writes beneath it, reject symlink escape, enforce a declared cache cap, and clean only named experiment work directories.

`shell2` is control/download only. QEMU executes only on an approved compute target such as `batch1`/`batch2`. SSH uses the authenticated DoC `@cert-authority` entry with strict host checking; unknown host keys are never auto-accepted.

## 9. Evidence classes

Keep three environments separate:

### Manual GitHub Actions smoke

Purpose: reproducible functionality and cleanup.

- `workflow_dispatch` only during the spike;
- detect KVM and fall back to TCG with a reason code;
- no stable performance claim;
- upload bounded raw JSON/logs for seven days;
- pinned third-party action revisions.

### Imperial DoC batch evidence

Purpose: controlled x86 mechanism and directional performance.

- one QEMU process at a time initially;
- choose the less-loaded permitted batch host;
- use `nice` plus outer timeout;
- record exact KVM/TCG choice, CPU, memory, QEMU, kernel, image hashes and load;
- compare N={1,2} only after N=1 memory/cleanup gates; N>2 requires a new explicit capacity decision;
- three fresh-VM repeats per accepted fixture;
- shared-host noise remains a limitation.

### Real AWS Lambda x86_64

Purpose: authoritative deployment qualification.

- requires separate explicit approval before CodeBuild/ECR/Lambda writes or paid runs;
- package exact QEMU, firmware/kernel/rootfs/evaluator artifacts in the Lambda container;
- run TCG unless exact target evidence proves another accelerator;
- exercise real Shimmy HTTP `eval` and `preview`;
- prove timeout cleanup and a successful following request in the same warm Lambda environment;
- record cold initialization, boot-ready, evaluator execution, teardown, total HTTP latency, peak process RSS where observable, package/image sizes and Lambda memory/timeout configuration;
- keep credentials and pre-signed URLs out of receipts.

A GHA or DoC pass does not substitute for this target gate.

## 10. Acceptance and stop conditions

### VALIDATED AS SHIMMY TERMINAL FALLBACK

All are required:

- `FUNCTION_INTERFACE=qemu` routes explicitly and no other interface silently enters QEMU;
- startup verifies the complete artifact chain before serving;
- real Shimmy HTTP `eval` and `preview` pass for Python/native Lean fixtures;
- each request uses a distinct QEMU PID and fresh VM identity;
- guest global, process, writable filesystem and kernel state do not cross requests;
- no guest network or Host filesystem/env/secret visibility in the tested configuration;
- timeout/crash leaves no QEMU process group, Unix socket or work directory, and a subsequent request succeeds;
- request/response size caps and malformed frames fail closed;
- GHA smoke, DoC profile and real Lambda x86_64 TCG E2E all retain exact evidence;
- docs label this as terminal compatibility fallback, not fast path or universal package proof.

### PARTIAL

Use when mechanism and compatibility pass but Lambda target evidence, one runtime family, PSS, or a security canary remains unavailable. PARTIAL does not enter recommended deployment recipes as a qualified fallback.

### INVALIDATED

Use when TCG cannot boot/execute within the configured Lambda envelope, cleanup cannot be proven, the guest requires ambient host/network authority, artifacts cannot be pinned, or the requested evaluator semantics cannot cross the bounded protocol.

### Natural stop

After verdict and evidence, stop. No warm VM pool, snapshot restore, automatic backend retry, session state, migration, or broader package matrix without a new owner decision.

---

## Task 0: Retract the cross-project plan and freeze this contract

**Objective:** Ensure Agent Python Runtime no longer advertises QEMU and Shimmy owns the only active plan.

**Files:**
- Remove from Agent Python Runtime: `docs/plans/2026-07-23-qemu-system-compatibility-spike.md`
- Restore Agent Python Runtime: `README.md`, `docs/status.md`
- Create here: `docs/plans/2026-07-23-qemu-ultimate-fallback.md`
- Modify here: `README.md`

**Steps:**

1. Remove the misplaced APR plan and every public link/status claim to it.
2. Run APR documentation contract/link tests and `git diff --check`.
3. Add this plan and one README note saying supervisor-requested/planned/not implemented.
4. Run Shimmy markdown link and diff checks available in the repo.
5. Make separate signed docs-only commits in each repository.

## Task 1: Add the explicit interface contract

**Objective:** Introduce QEMU as an explicit boundary without implementing automatic fallback.

**Files:**
- Modify: `internal/execution/supervisor/models.go`
- Modify: `internal/execution/dispatcher.go`
- Modify: `internal/execution/dispatcher_routing_test.go`
- Create: `internal/execution/qemu/config.go`
- Create: `internal/execution/qemu/config_test.go`

**TDD steps:**

1. RED: `QemuIO == "qemu"`; dispatcher routes it only to a QEMU factory seam.
2. RED: QEMU plus DBI, WASM profile, missing artifacts, unknown accelerator, invalid sizes/timeouts and non-Linux hosts fail closed.
3. RED: errors from another backend are never retried through QEMU.
4. GREEN: implement typed config parsing/validation and a factory seam returning a deliberate not-yet-implemented error.
5. Run focused tests, then `go test ./internal/execution/...`.
6. Commit: `feat(qemu): define explicit fallback interface`.

## Task 2: Implement the bounded frame protocol

**Objective:** Add one reusable Host/guest codec with strict limits.

**Files:**
- Create: `internal/execution/qemu/protocol.go`
- Create: `internal/execution/qemu/protocol_test.go`
- Create: `experiments/qemu-fallback/guest-agent/protocol.go`
- Create: `experiments/qemu-fallback/guest-agent/protocol_test.go`

**TDD steps:**

1. RED fixtures: valid eval/preview/healthcheck, zero/oversized length, truncated body, unknown schema/command, trailing frame, malformed JSON and bounded response.
2. Implement identical length-prefix and envelope contracts on Host and guest sides.
3. Add golden fixture bytes shared by both test suites; reject drift.
4. Fuzz the frame decoder with size caps enabled.
5. Commit: `feat(qemu): add bounded guest protocol`.

## Task 3: Implement the generic guest agent

**Objective:** Execute arbitrary file-protocol evaluators inside the guest without Python-specific Host logic.

**Files:**
- Create: `experiments/qemu-fallback/guest-agent/main.go`
- Create: `experiments/qemu-fallback/guest-agent/manifest.go`
- Create: `experiments/qemu-fallback/guest-agent/executor.go`
- Create tests in the same package
- Create: `experiments/qemu-fallback/fixtures/python-numpy/`
- Create: `experiments/qemu-fallback/fixtures/lean/`

**TDD steps:**

1. RED: manifest validation, argv preservation, request/response file cleanup, malformed evaluator output, nonzero exit, timeout, output cap and command allowlist.
2. Implement `shimmy-file-v1` in a temporary guest directory with a fresh evaluator process.
3. Ensure the agent returns only one bounded frame and never forwards its environment wholesale.
4. Add native unit E2E for Python and Lean fixtures before any VM boot.
5. Build a static Linux amd64 agent and record its SHA-256.
6. Commit: `feat(qemu): add generic guest evaluator agent`.

## Task 4: Lock and build the guest artifact chain

**Objective:** Produce reproducible, hash-bound Linux/evaluator images.

**Files:**
- Create: `experiments/qemu-fallback/sources.lock.json`
- Create: `experiments/qemu-fallback/image-manifest.schema.json`
- Create: `experiments/qemu-fallback/scripts/fetch_source.py`
- Create: `experiments/qemu-fallback/scripts/build_seed.py`
- Create: `experiments/qemu-fallback/scripts/prepare_image.py`
- Create: `experiments/qemu-fallback/README.md`
- Create/modify focused tests under `experiments/qemu-fallback/`

**TDD steps:**

1. RED: reject mutable `latest` URLs, bad hashes, missing licenses, moving package mirrors, path/symlink escape and cache-cap overflow.
2. Pin a dated Debian-family amd64 image, package snapshot/release identity, kernel/firmware inputs, evaluator packages and guest-agent digest.
3. Fetch atomically and verify before/after preparation.
4. Prepare the guest without Docker/root-required local mutation; GHA/DoC may use boot-time NoCloud preparation if needed.
5. Seal the root/evaluator image, remove credentials/cloud-init state, emit a manifest and verify read-only boot.
6. Commit: `build(qemu): lock fallback guest images`.

## Task 5: Implement fail-closed QEMU process lifecycle

**Objective:** Launch, communicate with and destroy one VM per request.

**Files:**
- Create: `internal/execution/qemu/command.go`
- Create: `internal/execution/qemu/process_linux.go`
- Create: `internal/execution/qemu/process_stub.go`
- Create: `internal/execution/qemu/process_test.go`

**TDD steps:**

1. RED command tests require `-nodefaults`, `-net none`, read-only images, private socket, bounded CPU/RAM and no shell interpolation/host mounts.
2. RED lifecycle tests cover boot timeout, malformed ready handshake, request timeout, guest error, QEMU crash, cancellation, occupied/stale socket, SIGTERM failure, SIGKILL escalation and cleanup failure.
3. Implement exact PID/process-group ownership and bounded readiness polling.
4. Verify no orphan process/socket/work directory under every injected failure.
5. Cross-build the non-Linux stub and fail with a stable unsupported error.
6. Commit: `feat(qemu): add disposable vm lifecycle`.

## Task 6: Integrate the QEMU dispatcher

**Objective:** Serve current Shimmy commands through a one-VM-per-request dispatcher.

**Files:**
- Create: `internal/execution/qemu/dispatcher.go`
- Create: `internal/execution/qemu/dispatcher_test.go`
- Modify: `internal/execution/dispatcher.go`
- Modify: `internal/execution/dispatcher_routing_test.go`

**TDD steps:**

1. RED: Start verifies artifacts but does not execute an evaluator request.
2. RED: each Send creates a distinct VM identity; N concurrent requests cannot share socket/work/image writable state.
3. RED: `FUNCTION_MAX_PROCS` strictly bounds concurrent VM creation, queued cancellation does not launch a VM, and every completion/error releases capacity.
4. RED: eval, preview and healthcheck preserve existing Shimmy result/error maps.
5. RED: one timeout does not poison a later request; Shutdown waits for or kills owned VMs only.
6. GREEN: wire `FUNCTION_INTERFACE=qemu` to the package.
7. Run focused race tests and full Go gates.
8. Commit: `feat(qemu): route terminal fallback requests`.

## Task 7: Add semantic/security canaries

**Objective:** Prove the bounded freshness and authority claims before performance work.

**Files:**
- Create: `experiments/qemu-fallback/scripts/run_smoke.py`
- Create: `experiments/qemu-fallback/fixtures/canaries/`
- Modify QEMU integration tests

**Required canaries:**

- guest global/process marker absent on the next request;
- guest `/tmp` and writable file marker absent on the next request;
- guest boot ID differs;
- no network interface/default route capable of egress;
- literal-IP and DNS egress both fail;
- Host canary file, selected environment variable and `/proc` Host data are invisible;
- fork bomb/process count, memory allocation, oversized output and infinite loop are bounded by the available controls;
- QEMU crash/timeout cleanup followed by successful real request.

Do not call passing canaries a universal VM-escape proof.

Commit: `test(qemu): prove fallback freshness boundary`.

## Task 8: Add manual GitHub Actions smoke

**Objective:** Prove reproducible boot/protocol/HTTP behavior on a disposable runner.

**Files:**
- Create: `.github/workflows/qemu-fallback.yml`
- Add workflow contract tests if the repo has no generic validator

**Requirements:**

- `workflow_dispatch` only during spike;
- `ubuntu-24.04`, hard timeout and concurrency cancellation;
- immutable action SHAs for the new workflow;
- KVM detect plus structured TCG fallback;
- source/image verification before boot;
- full Shimmy HTTP eval/preview plus Task 7 canaries;
- raw JSON/log upload with seven-day retention;
- no release/deployment/public image action.

Commit: `ci(qemu): add manual fallback smoke`.

## Task 9: Run the DoC x86 profile

**Objective:** Measure the mechanism on approved CPU hosts without loading shell servers.

**Files:**
- Create: `experiments/qemu-fallback/scripts/probe_host.py`
- Create: `experiments/qemu-fallback/scripts/run_doc_profile.py`
- Create: `experiments/qemu-fallback/evidence.schema.json`

**Controller-only procedure:**

1. Use strict DoC CA-backed SSH identity verification.
2. Probe `batch1`/`batch2` for QEMU/KVM, CPU/RAM/load/free space; defer if both are busy.
3. Sync exact clean source and download/build inputs only under `/vol/bitbucket/ys25/shimmy/qemu-fallback/`.
4. Run one VM at a time for N=1; proceed to N=2 only after memory/load/cleanup gates.
5. Run three fresh-VM repeats for Python and Lean eval/preview.
6. Record boot-ready, guest execute, teardown, total dispatcher and full Shimmy HTTP times separately.
7. Keep KVM and TCG documents separate.
8. Return only bounded manifests/logs/raw JSON to the repository; remove named work directories and keys, retain verified caches/images.
9. Commit accepted raw evidence only after local schema/semantic validation.

## Task 10: Qualify the real Lambda target

**Objective:** Determine whether the fallback is viable inside Shimmy's actual x86_64 Lambda deployment boundary.

**Files:**
- Create in `shimmy-sandbox-prototypes`: `probe-lambda/shimmy-qemu-fallback/`
- Create after acceptance in `shimmy-docs`: canonical raw JSON and report

**Precondition:** obtain explicit owner approval for CodeBuild/ECR/Lambda writes and bounded cost.

**Steps:**

1. Reuse the existing probe repository's redaction, resource naming and cleanup patterns.
2. Build one exact digest-bound Lambda container containing Shimmy, QEMU and guest/evaluator artifacts.
3. Deploy only the named probe function on x86_64 with explicit memory/timeout/ephemeral-storage settings.
4. Run cold and warm-environment HTTP eval/preview for Python and Lean.
5. Run timeout/crash then a successful following request.
6. Capture structured metrics and redacted resource receipts; never persist credentials or pre-signed URLs.
7. Tear down or retain resources according to the approved probe policy and verify actual state.
8. Classify target result independently from GHA/DoC.

## Task 11: Verdict, docs and natural stop

**Objective:** Integrate only evidence-supported wording and close the spike.

**Files:**
- Modify: `README.md`
- Modify: `docs/deployment-recipes.md`
- Create or modify: `docs/qemu-final-fallback.md`
- Modify: this plan
- Update `shimmy-docs/docs/current/` only after accepted target evidence

**Steps:**

1. Classify VALIDATED, PARTIAL or INVALIDATED using Section 10.
2. Record exact commits, workflow/run IDs, image hashes, accelerator, Lambda config, raw evidence and cleanup proof.
3. Add QEMU to recommended deployment recipes only if all VALIDATED gates pass; otherwise retain it as planned/experimental/unsupported with the exact blocker.
4. Preserve the term “terminal compatibility fallback”; do not call it fast, universal, or automatically selected.
5. Run full Go/race/build/workflow/docs/evidence gates across touched repositories.
6. Signed small commits and push; verify remote SHAs/signatures and clean worktrees.
7. Set **No active implementation pointer** unless a new owner decision promotes a next phase.
