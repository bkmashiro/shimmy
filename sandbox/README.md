# Shimmy Sandbox

Userspace sandboxing for Lambda code evaluation, with pluggable backends and automatic fallback.

## Architecture

The sandbox system uses a `SandboxBackend` interface with three implementations:

| Backend | Platform | Binary Required | Description |
|---------|----------|-----------------|-------------|
| `direct` | All | None | No sandboxing (passthrough) |
| `sandlock` | Linux | `sandlock` | rlimit + seccomp via wrapper binary |
| `wasm` | All | `wasmtime` | WASI runtime for precompiled wasm |

If a requested backend is unavailable, the system automatically falls back to `direct`.

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `SHIMMY_SANDBOX` | Set to `1` to enable sandboxing | (disabled) |
| `SHIMMY_SANDBOX_BACKEND` | Backend name: `direct`, `sandlock`, `wasm` | `direct` |
| `SHIMMY_SANDLOCK_PATH` | Custom path to sandlock binary | auto-detect |
| `SHIMMY_WASMTIME_PATH` | Custom path to wasmtime binary | auto-detect |

### Config Defaults

```go
sandbox.DefaultConfig() → Config{
    MaxMemoryMB:  256,
    CPUTimeSecs:  10,
    AllowNetwork: false,
    WorkDir:      "",
    EnvVars:      nil,
}
```

## Usage

### Programmatic (via SandboxBackend interface)

```go
import "github.com/lambda-feedback/shimmy/internal/sandbox"

// Auto-select backend from SHIMMY_SANDBOX_BACKEND env var
backend := sandbox.NewBackendFromEnv()

// Or select explicitly
backend = sandbox.NewBackend("sandlock") // falls back to direct if unavailable

cfg := sandbox.DefaultConfig()
cfg.WorkDir = "/path/to/workdir"

cmd, err := backend.WrapCommand(ctx, "python3", []string{"script.py"}, cfg)
if err != nil {
    log.Fatal(err)
}
out, err := cmd.Output()
```

### Via Worker (shimmy integration)

```bash
# Enable sandboxing with sandlock backend
export SHIMMY_SANDBOX=1
export SHIMMY_SANDBOX_BACKEND=sandlock

# Or with wasm backend
export SHIMMY_SANDBOX=1
export SHIMMY_SANDBOX_BACKEND=wasm
```

The worker's `createCmd()` automatically wraps commands with the selected backend when `SHIMMY_SANDBOX=1`. If the backend is unavailable or `WrapCommand` fails, it falls back to direct execution.

### Command Line Wrapper (standalone, Linux only)

```bash
# Build (requires libseccomp-dev)
go build -o sandbox-wrapper ./cmd/sandbox-wrapper

# Run with restrictions
./sandbox-wrapper --cpu 5 --mem 128 --no-network -- python3 student.py
```

Flags: `--cpu`, `--mem`, `--timeout`, `--no-network`, `--no-fork`, `--clean-env`, `--allow-env`, `--nproc`, `--fsize`, `--nofile`.

## Backend Details

### SandlockBackend

Wraps commands with the `sandlock` binary, which enforces:
- CPU time limit (`RLIMIT_CPU`)
- Memory limit (`RLIMIT_AS`)
- Network blocking (seccomp-bpf)
- Working directory isolation

Binary resolution order:
1. Configured `binaryPath` (internal)
2. `SHIMMY_SANDLOCK_PATH` env var
3. `sandlock` in `$PATH`
4. Default path

### WasmBackend

Runs precompiled `.wasm` binaries with `wasmtime`:
- CPU limiting via fuel metering (1 second = 1,000,000 fuel)
- Directory access via `--dir`
- Automatic `.wasm` suffix resolution

Binary resolution order:
1. Configured `wasmtimePath` (internal)
2. `SHIMMY_WASMTIME_PATH` env var
3. `wasmtime` in `$PATH`

## Tested Features

| Feature | Status | Implementation |
|---------|--------|----------------|
| Memory limit | Working | RLIMIT_AS (sandlock) |
| CPU limit | Working | RLIMIT_CPU (sandlock), fuel (wasm) |
| File size limit | Working | RLIMIT_FSIZE (sandbox-wrapper) |
| Network block | Working | seccomp-bpf (sandlock) |
| Timeout | Working | SIGKILL on timeout (sandlock) |
| Process limit | Limited | RLIMIT_NPROC (per-user, not per-process) |

## Limitations

1. **RLIMIT_NPROC** limits per-user, not per-process (would need cgroups + root)
2. **seccomp filter** applies to entire process tree (actually a feature)
3. **No namespace isolation** (requires root/CAP_SYS_ADMIN, not available on Lambda)
4. **WasmBackend** requires programs to be precompiled to WASI-compatible `.wasm`
