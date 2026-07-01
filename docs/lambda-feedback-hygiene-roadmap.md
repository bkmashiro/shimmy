# Lambda Feedback evaluator hygiene roadmap

This track is intentionally backend-independent. It applies to native/file workers,
future Python reactors, Pyodide, and WASM-backed evaluators, but it is not a WASM
security boundary.

## Positioning

Call this lifecycle hygiene and compatibility hardening, not sandbox security.
These measures reduce accidental evaluator pollution and make CI/demo runs more
deterministic. A malicious native evaluator can still escape this model; stronger
isolation requires WASM, containers, OS users, seccomp, Firecracker, or an
equivalent boundary.

## Useful slices

- [ ] Per-request evaluator context: temporary working directory, cwd restore,
      `sys.path` restore, targeted evaluator-module eviction, stdout/stderr
      capture, and optional environment overlay/restore.
- [ ] Compatibility doctor: a script that checks root/entrypoint importability,
      eval/preview signatures, JSON normalization, repeated invocation behavior,
      and leftover file hygiene.
- [ ] Structured errors: classify failures by phase (`load`, `eval`, `preview`,
      `normalize`) with user-readable messages and diagnostic stderr/traceback.
- [ ] Documentation matrix: distinguish hygiene guarantees from security
      guarantees across file worker, native Python, Pyodide/reactor, and WASM.

## Near-term contract

The first implementation slice should add a small adapter-level API that can run
one evaluator call inside a temporary request workspace and restore the host
process state afterwards. Existing `load_entrypoint()`/`call_function()` remain
available for tests and low-level integrations; higher-level file-worker/runtime
paths can opt into the hygiene context once proven.
