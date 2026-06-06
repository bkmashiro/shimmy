# shimmy-wasm Documentation

| File | Description |
|---|---|
| [architecture.md](architecture.md) | System overview: layered stack diagram, backend selection, pool model, and numbered request lifecycle |
| [guest-abi.md](guest-abi.md) | Full contract for the `alloc`/`evaluate` exports, request JSON envelope, response binary layout, and working examples in Go, Rust, and C |
| [snapshot-restore.md](snapshot-restore.md) | Why snapshot/restore is needed, the `SnapshotStrategy` interface, all four strategies (memcpy, soft-dirty, mprotect, uffd), selection logic, and performance data |
| [python-backends.md](python-backends.md) | The three Python execution paths (per-request, resident, reactor), their isolation models, latency profiles, and pool sizing |
| [security.md](security.md) | Threat model, wazero sandbox guarantees, per-request memory reset, resource limits, host function surface area, and what the sandbox does not protect against |
| [configuration.md](configuration.md) | Exhaustive environment variable reference grouped by section, with Docker and AWS Lambda deployment examples |
| [adding-a-language.md](adding-a-language.md) | Step-by-step guide for implementing a new evaluation function language targeting wasm32-wasip1, including Zig, AssemblyScript, and Swift examples |
| [demo.md](demo.md) | One-command end-to-end demo: build Shimmy, compile a stateful WASM evaluator, serve HTTP, and prove snapshot/restore isolation |
| [demo-scenarios.md](demo-scenarios.md) | Multi-scenario demo matrix: Go/Rust/C/C++ WASI modules, real Lambda Feedback source references, and Python/Linux notes |
| [writing-eval-functions.md](writing-eval-functions.md) | Guide for authors writing Python evaluation functions (`evaluation_function` / `preview_function`) |
