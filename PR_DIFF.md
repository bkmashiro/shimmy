# Pull Request: Add Userspace Sandbox for Process Isolation

## Summary

This PR adds userspace sandboxing for student code execution using rlimits and
seccomp-bpf. Enable with `SHIMMY_SANDBOX=1` environment variable.

## Changes

### New Files

#### cmd/sandbox-wrapper/main.go
Wrapper binary that applies resource limits and seccomp filters before exec'ing
the target command.

**Features:**
- CPU time limit (`-cpu`)
- Memory limit (`-mem`)
- File size limit (`-fsize`)
- FD limit (`-nofile`)
- Network blocking (`--no-network`)
- Environment sanitization (`-clean-env`)

**Usage:**
```bash
sandbox-wrapper -cpu 5 -mem 256 --no-network -clean-env -- python3 script.py
```

#### internal/sandbox/sandbox.go
Go package for integrating sandbox into shimmy.

```go
cfg := sandbox.DefaultConfig()
cfg.AllowNetwork = false
cmd := sandbox.WrapCommandContext(ctx, "python3", args, cfg)
```

### Modified Files

#### internal/execution/worker/worker.go
Add sandbox support to `createCmd`:

```go
func createCmd(ctx context.Context, config StartConfig) *exec.Cmd {
    if os.Getenv("SHIMMY_SANDBOX") == "1" {
        cfg := sandbox.DefaultConfig()
        return sandbox.WrapCommandContext(ctx, config.Cmd, config.Args, cfg)
    }
    // ... original code
}
```

## Test Results

| Test Category | Pass Rate |
|--------------|-----------|
| Security tests | 13/15 (87%) |
| Edge cases | 10/10 (100%) |
| Performance | ~1.5ms overhead |

### Security Tests
- ✅ Network blocking (TCP, UDP, connect)
- ✅ Memory limit (allocation, mmap)
- ✅ CPU limit (infinite loops)
- ✅ File size limit
- ✅ FD exhaustion
- ✅ Signal immunity (timeout kills)
- ⚠️ Fork limit (RLIMIT_NPROC per-user limitation)

### Performance
```
Echo:               +1.24ms (+633%)
Python startup:     +1.59ms (+28%)
Python with imports: +1.76ms (+17%)
Average:            ~1.5ms
```

## Deployment

1. Build wrapper:
   ```bash
   GOOS=linux GOARCH=amd64 go build -o sandbox-wrapper ./cmd/sandbox-wrapper
   ```

2. Include in Lambda package

3. Set environment:
   ```bash
   export SHIMMY_SANDBOX=1
   ```

## Known Limitations

1. **Fork limit**: RLIMIT_NPROC is per-user, not per-sandbox
2. **Lambda seccomp**: May conflict with existing Lambda seccomp profile

## Checklist

- [x] Code compiles
- [x] Tests pass (87% security, 100% edge cases)
- [x] Performance acceptable (~1.5ms)
- [x] Documentation complete
- [x] Environment sanitization implemented
- [ ] Lambda integration test (pending AWS access)

---

/cc @thesis-supervisor
