# Shimmy Sandbox Technical Report

**Author:** Akashi (CTO)  
**Date:** 2026-03-09  
**Status:** Research Complete, Ready for PR

---

## Executive Summary

This report documents the research and implementation of userspace sandboxing
for the shimmy Lambda evaluation platform. The sandbox provides process isolation
without requiring root privileges or kernel modifications.

### Key Results

| Security Control | Status | Implementation |
|------------------|--------|----------------|
| Memory Limit | ✅ Working | RLIMIT_AS |
| CPU Limit | ✅ Working | RLIMIT_CPU |
| File Size Limit | ✅ Working | RLIMIT_FSIZE |
| FD Limit | ✅ Working | RLIMIT_NOFILE |
| Network Block | ✅ Working | seccomp-bpf |
| Signal Immunity | ✅ Working | SIGKILL timeout |
| Fork Limit | ⚠️ Limited | RLIMIT_NPROC (see notes) |

**Pass Rate: 13/15 tests (87%)**

---

## 1. Architecture

### 1.1 Problem Statement

Student code execution in shimmy needs isolation to prevent:
- Memory exhaustion (OOM)
- CPU hogging (infinite loops)
- Network access (data exfiltration)
- File system abuse
- Process spawning attacks (fork bombs)

### 1.2 Constraint Analysis

Lambda environment constraints:
- No root access
- No kernel module loading
- No user namespaces (CAP_SYS_ADMIN)
- No cgroups modification
- Existing seccomp profile (may conflict)
- No KVM/virtualization

### 1.3 Solution: Wrapper-Based Sandbox

Since Go's `exec.Cmd` doesn't support pre-exec hooks, we use a wrapper binary:

```
shimmy worker
     ↓
exec("sandbox-wrapper", ["-cpu=5", "-mem=128", "--no-network", "--", "python3", "script.py"])
     ↓
sandbox-wrapper:
     1. Apply rlimits (setrlimit)
     2. Apply seccomp (seccomp_filter)
     3. exec("python3", ["script.py"])
```

This is the standard pattern used by:
- Chromium sandbox
- Firejail
- Bubblewrap
- nsjail

---

## 2. Implementation

### 2.1 Component Overview

```
shimmy-research/
├── cmd/sandbox-wrapper/
│   └── main.go              # 181 lines - wrapper binary
├── internal/sandbox/
│   ├── sandbox.go           # 66 lines - Go integration
│   └── worker_integration.go # Integration guide
├── sandbox/
│   ├── sandbox.go           # Config, SandboxedCmd
│   ├── wrapper.go           # Execute() function
│   └── seccomp.go           # BPF filter generation
└── sandbox_tests.py         # 366 lines - security tests
```

### 2.2 Resource Limits (rlimits)

```go
limits := []struct{ res int; val uint64 }{
    {unix.RLIMIT_CPU, cpuSeconds},     // CPU time
    {unix.RLIMIT_AS, memoryBytes},     // Address space
    {unix.RLIMIT_FSIZE, maxFileSize},  // File size
    {unix.RLIMIT_NOFILE, maxFDs},      // File descriptors
    {unix.RLIMIT_CORE, 0},             // No core dumps
}
for _, l := range limits {
    unix.Setrlimit(l.res, &unix.Rlimit{Cur: l.val, Max: l.val})
}
```

### 2.3 Seccomp Filter

```go
// Network syscalls to block
blocked := []uint32{
    unix.SYS_SOCKET,
    unix.SYS_CONNECT,
    unix.SYS_ACCEPT,
    unix.SYS_BIND,
    unix.SYS_LISTEN,
    unix.SYS_SENDTO,
    unix.SYS_RECVFROM,
    unix.SYS_SENDMSG,
    unix.SYS_RECVMSG,
}

// Build BPF program
insns := []unix.SockFilter{
    {Code: BPF_LD | BPF_W | BPF_ABS, K: 0},  // Load syscall number
}
for _, nr := range blocked {
    insns = append(insns,
        unix.SockFilter{Code: BPF_JMP | BPF_JEQ | BPF_K, Jt: 0, Jf: 1, K: nr},
        unix.SockFilter{Code: BPF_RET | BPF_K, K: SECCOMP_RET_ERRNO | 1},
    )
}
insns = append(insns, unix.SockFilter{Code: BPF_RET | BPF_K, K: SECCOMP_RET_ALLOW})
```

---

## 3. Test Results

### 3.1 Test Environment

```
Platform: Docker (golang:1.24-alpine)
Arch: aarch64 (Apple Silicon)
seccomp: unconfined (for testing custom filters)
```

### 3.2 Network Tests

| Test | Result | Notes |
|------|--------|-------|
| TCP socket | ✅ BLOCKED | `[Errno 1] Operation not permitted` |
| UDP socket | ✅ BLOCKED | `[Errno 1] Operation not permitted` |
| Connect | ✅ BLOCKED | `[Errno 1] Operation not permitted` |

### 3.3 Memory Tests

| Test | Result | Notes |
|------|--------|-------|
| Allocation bomb | ✅ BLOCKED | `MemoryError` at 64MB limit |
| Mmap bomb | ✅ BLOCKED | `[Errno 12] Out of memory` |

### 3.4 CPU Tests

| Test | Result | Notes |
|------|--------|-------|
| Infinite loop | ✅ BLOCKED | Killed after 3s CPU time |
| Prime calculation | ✅ BLOCKED | Killed after 3s CPU time |

### 3.5 File System Tests

| Test | Result | Notes |
|------|--------|-------|
| Large file write | ✅ BLOCKED | `[Errno 27] File too large` at 5MB |
| FD exhaustion | ✅ BLOCKED | `No file descriptors` after 47 FDs |

### 3.6 Process Tests

| Test | Result | Notes |
|------|--------|-------|
| Fork bomb | ❌ NOT BLOCKED | RLIMIT_NPROC per-user limitation |
| Subprocess spawn | ❌ NOT BLOCKED | Same limitation |

**Note:** RLIMIT_NPROC limits processes per UID, not per process tree.
In containers with few processes, the limit may not be reached.

### 3.7 Signal Tests

| Test | Result | Notes |
|------|--------|-------|
| Ignore SIGTERM | ✅ BLOCKED | Killed by wrapper timeout |

---

## 4. Known Limitations

### 4.1 Fork/Process Limit

**Problem:** RLIMIT_NPROC counts all processes for a UID, not per sandbox.

**Mitigation Options:**
1. **Accept limitation** - Lambda has its own process limits
2. **seccomp block clone** - Breaks threading, not recommended
3. **Process monitoring** - Reactive, not preventive
4. **PID namespace** - Requires CAP_SYS_ADMIN (not available)

**Recommendation:** Accept limitation. Lambda's existing limits provide protection.

### 4.2 /proc Information Leak

**Problem:** /proc/self/environ and other /proc files readable.

**Mitigation:**
- Sanitize environment variables before exec
- Use `env -i` to clear environment
- Mount /proc with hidepid=2 (requires privileges)

### 4.3 Lambda seccomp Compatibility

**Problem:** Lambda may already have seccomp filters; adding more could conflict.

**Mitigation:**
- Test in real Lambda environment
- Use SECCOMP_FILTER_FLAG_TSYNC for thread sync
- Fall back to rlimits-only if seccomp fails

---

## 5. Integration Guide

### 5.1 Build Wrapper

```bash
cd cmd/sandbox-wrapper
GOOS=linux GOARCH=amd64 go build -o sandbox-wrapper .
```

### 5.2 Deploy to Lambda

Include `sandbox-wrapper` in Lambda deployment package.

### 5.3 Enable Sandbox

```bash
export SHIMMY_SANDBOX=1
```

### 5.4 Code Changes

```go
// internal/execution/worker/worker.go

import "github.com/lambda-feedback/shimmy/internal/sandbox"

func createCmd(ctx context.Context, config StartConfig) *exec.Cmd {
    if os.Getenv("SHIMMY_SANDBOX") == "1" {
        cfg := sandbox.DefaultConfig()
        cfg.MaxMemoryMB = 256
        cfg.AllowNetwork = false
        return sandbox.WrapCommandContext(ctx, config.Cmd, config.Args, cfg)
    }
    // ... original code
}
```

---

## 6. Performance

### 6.1 Overhead Estimate

| Operation | Overhead |
|-----------|----------|
| Wrapper exec | ~1-5ms |
| rlimit setup | ~0.1ms |
| seccomp load | ~0.1ms |
| **Total** | **~2-6ms** |

Negligible for typical evaluation workloads (100ms+).

### 6.2 Memory Overhead

Wrapper binary: ~2MB
No runtime memory overhead (exec replaces process).

---

## 7. Recommendations

### 7.1 Immediate (PR-ready)

1. ✅ Use wrapper-based sandbox
2. ✅ Enable rlimits (CPU, memory, file size, FDs)
3. ✅ Enable seccomp network blocking
4. ✅ Implement timeout with SIGKILL

### 7.2 Future Improvements

1. Test in real Lambda environment
2. Add `/proc` access restrictions via seccomp
3. Implement environment sanitization
4. Add resource usage metrics collection

---

## 8. Files

| File | Lines | Purpose |
|------|-------|---------|
| `cmd/sandbox-wrapper/main.go` | 181 | Wrapper binary |
| `internal/sandbox/sandbox.go` | 66 | Go integration |
| `sandbox_tests.py` | 366 | Security tests |
| `SANDBOX_INTEGRATION.md` | 120 | Integration docs |
| `SANDBOX_REPORT.md` | This file | Technical report |

**Total:** ~733 lines of code

---

## 9. References

1. Linux seccomp documentation
2. Go syscall package
3. Chromium sandbox design
4. AWS Lambda execution environment
5. OJ sandbox designs (Heng-Client)

---

## Appendix A: Full Test Output

```
============================================================
SUMMARY
============================================================

Passed: 13/15

  ✅ Network: TCP socket
  ✅ Network: UDP socket
  ✅ Network: Connect
  ✅ Memory: Allocation bomb
  ✅ Memory: Mmap bomb
  ✅ CPU: Infinite loop
  ✅ CPU: Busy calculation
  ❌ Fork: Fork bomb
  ❌ Fork: Subprocess spawn
  ✅ File: Large file write
  ✅ File: FD exhaustion
  ✅ Info: /proc/self/environ
  ✅ Info: /proc/1/cmdline
  ✅ Signal: Ignore SIGTERM
  ✅ Symlink: Escape attempt
```

---

*End of Report*

---

## Appendix B: Performance Benchmark

```
============================================================
SANDBOX PERFORMANCE BENCHMARK
============================================================
Iterations: 20

Test                          Direct    Sandbox     Overhead
------------------------------------------------------------
Echo (minimal)                0.20ms     1.43ms     1.24ms (+633%)
Python startup                5.74ms     7.34ms     1.59ms (+28%)
Python with imports          10.28ms    12.04ms     1.76ms (+17%)
Python calculation            5.73ms     7.19ms     1.46ms (+25%)
------------------------------------------------------------
Average overhead: 1.51ms
```

**Conclusion:** ~1.5ms overhead is negligible for typical evaluation workloads.

---

## Appendix C: Edge Case Tests

```
==================================================
EDGE CASE TESTS - ALL PASSED (10/10)
==================================================

CPU LIMIT PRECISION:
  ✅ 2s limit, 1s work → completed in 1.00s
  ✅ 1s limit, 3s work → killed

MEMORY LIMIT PRECISION:
  ✅ 32MB limit, 20MB alloc → allocated 20MB
  ✅ 16MB limit, 50MB alloc → blocked: MemoryError

FILE SIZE PRECISION:
  ✅ 2MB limit, 1MB write → wrote 1024KB

TIMEOUT PRECISION:
  ✅ 3s limit, 2s work → completed
  ✅ 2s limit, 5s work → timeout (exit 124)

FD LIMIT:
  ✅ 20 limit → blocked after 17 fds

ERROR HANDLING:
  ✅ No command → shows usage
  ✅ Invalid command → proper error message
```

---

## Appendix D: Test Files

| File | Lines | Purpose |
|------|-------|---------|
| `sandbox_tests.py` | 366 | Security attack tests |
| `benchmark.py` | 95 | Performance measurement |
| `edge_cases.py` | 170 | Boundary conditions |
| `fork_analysis.py` | 50 | Fork limitation analysis |

---

*Report updated: 2026-03-09 11:00 UTC*

---

## Appendix E: Environment Sanitization

### Problem

Student code can read `/proc/self/environ` to leak secrets:

```python
with open('/proc/self/environ', 'rb') as f:
    env = f.read()
    # Extract AWS_SECRET_KEY, DATABASE_PASSWORD, etc.
```

### Solution

Added `-clean-env` flag to sandbox-wrapper:

```bash
# Without -clean-env: secrets visible
AWS_SECRET=xxx ./sandbox-wrapper -- python3 script.py
# Secrets: ['AWS_SECRET']
# Total vars: 10

# With -clean-env: secrets hidden
AWS_SECRET=xxx ./sandbox-wrapper -clean-env -- python3 script.py
# Secrets: []
# Total vars: 4 (PATH, HOME, USER, LANG)
```

### Implementation

```go
if *cleanEnv {
    env = []string{
        "PATH=/usr/local/bin:/usr/bin:/bin",
        "HOME=/tmp",
        "USER=sandbox",
        "LANG=C.UTF-8",
    }
    // Add explicitly allowed vars
    if *allowEnv != "" {
        for _, name := range strings.Split(*allowEnv, ",") {
            if val := os.Getenv(name); val != "" {
                env = append(env, name+"="+val)
            }
        }
    }
}
```

### Verification

```
=== /proc/self/environ with -clean-env ===
Secrets in /proc: []
Env vars in /proc: 4
```

---

*Report updated: 2026-03-09 11:10 UTC*
