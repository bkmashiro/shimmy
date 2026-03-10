# Changelog

## Unreleased (sandbox feature branch)

### Features
- **Multi-backend sandbox**: sandlock (Linux), wasm (wasmtime WASI), direct (no-op) backends
- **SandboxBackend interface**: unified `WrapCommand` / `Available` / `Name` API
- **Factory with auto-detection**: `NewBackend("")` tries sandlock → wasm → direct
- **Worker integration**: `SHIMMY_SANDBOX=1` + `SHIMMY_SANDBOX_BACKEND` env vars
- **Config overrides**: per-worker `SandboxConfig` with memory, CPU, network, workdir limits

### Fixes
- Removed hardcoded `$HOME` path in sandlock binary resolution; uses `os.UserHomeDir()`
- All backends validate non-empty command name in `WrapCommand`
- `WasmBackend.resolveProgram` checks `.wasm` file existence for all paths
- Sandbox fallback now logs warning instead of silently degrading
- Worker stderr capped at 1MB via `io.LimitReader` to prevent unbounded growth
- Cleaned up TODO comments in worker.go with proper documentation

### Tests
- 217 tests across 8 packages, sandbox package at 92.1% coverage
- Worker package: ExitEvent, WritePipe, getExitEvent, iostream coverage added
- Benchmarks for `WrapCommand` and `NewBackend`
- Security-oriented tests: argument injection, separator validation, empty-name handling
- Race detector passes clean on all sandbox packages
