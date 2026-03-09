# Shimmy Sandbox Integration

## Problem

Student code execution in shimmy needs isolation to prevent:
- Memory exhaustion
- CPU hogging
- Network access
- File system abuse
- Process spawning attacks

## Solution: Wrapper-Based Sandbox

Since Go's `exec.Cmd` doesn't support pre-exec hooks, we use a wrapper binary
that applies restrictions before exec'ing the target command:

```
shimmy → sandbox-wrapper → python3 student.py
              ↓
         1. Apply rlimits
         2. Apply seccomp
         3. exec target
```

## Components

### 1. sandbox-wrapper (cmd/sandbox-wrapper/)

A minimal Go binary that:
- Applies RLIMIT_CPU, RLIMIT_AS, RLIMIT_FSIZE, etc.
- Applies seccomp filter to block network syscalls
- Exec's the target command

```bash
sandbox-wrapper -cpu 5 -mem 256 --no-network -- python3 student.py
```

### 2. internal/sandbox/ package

Go package for shimmy integration:

```go
import "github.com/lambda-feedback/shimmy/internal/sandbox"

cfg := sandbox.DefaultConfig()
cfg.MaxMemoryMB = 128
cfg.AllowNetwork = false

cmd := sandbox.WrapCommandContext(ctx, "python3", []string{"student.py"}, cfg)
```

### 3. Worker Integration

Modify `internal/execution/worker/worker.go`:

```go
func createCmd(ctx context.Context, config StartConfig) *exec.Cmd {
    if os.Getenv("SHIMMY_SANDBOX") == "1" {
        sandboxCfg := sandbox.DefaultConfig()
        cmd := sandbox.WrapCommandContext(ctx, config.Cmd, config.Args, sandboxCfg)
        cmd.Env = append(os.Environ(), config.Env...)
        cmd.Dir = config.Cwd
        initCmd(cmd)
        return cmd
    }
    // ... original code
}
```

## Verified Features

| Feature | Status | Implementation |
|---------|--------|----------------|
| Memory limit | ✅ | RLIMIT_AS |
| CPU limit | ✅ | RLIMIT_CPU |
| File size limit | ✅ | RLIMIT_FSIZE |
| Network block | ✅ | seccomp-bpf |
| Timeout | ✅ | SIGKILL |

## Lambda Compatibility

All features work in Lambda:
- No root required
- No kernel modules
- No user namespaces
- Uses standard syscalls (setrlimit, seccomp)

## Deployment

1. Build sandbox-wrapper for Lambda runtime (Linux/amd64):
   ```bash
   GOOS=linux GOARCH=amd64 go build -o sandbox-wrapper ./cmd/sandbox-wrapper
   ```

2. Include in Lambda deployment package

3. Set `SHIMMY_SANDBOX=1` environment variable

## Files Changed

```
cmd/sandbox-wrapper/main.go     # New: wrapper binary
internal/sandbox/sandbox.go     # New: Go integration package
internal/execution/worker/worker.go  # Modified: use sandbox
internal/execution/worker/models.go  # Modified: add SandboxConfig
```

## Testing

```bash
# Build wrapper
go build -o sandbox-wrapper ./cmd/sandbox-wrapper

# Test locally
./sandbox-wrapper -cpu 5 -mem 128 --no-network -- python3 -c "
import socket
try:
    s = socket.socket()
    print('FAIL: network allowed')
except:
    print('OK: network blocked')
"
```

Expected output:
```
OK: network blocked
```
