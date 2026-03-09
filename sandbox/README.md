# Shimmy Sandbox

Userspace sandboxing for Lambda code evaluation.

## ✅ Tested Features

| Feature | Status | Implementation |
|---------|--------|----------------|
| Memory limit | ✅ Working | RLIMIT_AS |
| CPU limit | ✅ Working | RLIMIT_CPU |
| File size limit | ✅ Working | RLIMIT_FSIZE |
| Network block | ✅ Working | seccomp-bpf |
| Timeout | ✅ Working | SIGKILL on timeout |
| Process limit | ⚠️ Limited | RLIMIT_NPROC (per-user) |

## Usage

```go
import "github.com/lambda-feedback/shimmy/sandbox"

cfg := sandbox.DefaultConfig()
cfg.MaxMemoryBytes = 128 * 1024 * 1024  // 128MB
cfg.MaxCPUSeconds = 5
cfg.AllowNetwork = false
cfg.Timeout = 10 * time.Second

result, err := sandbox.Execute("python3", []string{"student.py"}, cfg)
if err != nil {
    log.Fatal(err)
}
if result.Timeout {
    fmt.Println("Execution timed out")
}
```

## Command Line Wrapper

```bash
# Build
go build -o sandbox-wrapper ./cmd/sandbox-wrapper

# Run with restrictions
./sandbox-wrapper -cpu 5 -mem 128 --no-network -- python3 student.py
```

## Integration with shimmy

In `internal/execution/worker/worker_unix.go`, replace:

```go
func initCmd(cmd *exec.Cmd) {
    cmd.SysProcAttr = &syscall.SysProcAttr{
        Setpgid: true,
    }
}
```

With:

```go
func initCmd(cmd *exec.Cmd) {
    // Apply sandbox restrictions
    sandbox.ApplyRlimits(sandboxConfig)
    if !sandboxConfig.AllowNetwork {
        sandbox.ApplyNetworkSeccomp()
    }
    
    cmd.SysProcAttr = &syscall.SysProcAttr{
        Setpgid: true,
    }
}
```

## Test Results

```
=== Test 1: Network WITHOUT --no-network ===
Network: ALLOWED

=== Test 2: Network WITH --no-network (seccomp) ===
Network: BLOCKED - [Errno 1] Operation not permitted

=== Test 3: Combined sandbox (cpu + mem + no-network) ===
Testing network...
OK: socket blocked
Testing memory...
OK: memory limited
Done!
```

## Limitations

1. **RLIMIT_NPROC** limits per-user, not per-process
   - Solution: Use cgroups (requires root) or accept limitation
   
2. **seccomp filter applies to entire process tree**
   - This is actually a feature - child processes are also restricted
   
3. **No namespace isolation**
   - Requires root/CAP_SYS_ADMIN
   - Lambda doesn't allow this anyway
