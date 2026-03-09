# Building a Userspace Sandbox for Code Evaluation

*A deep dive into process isolation without root privileges*

---

## The Challenge

You're building an online judge or code evaluation platform. Students submit code,
your system runs it, and returns results. Simple, right?

Not quite. That innocent-looking Python script could:
- Consume all memory (OOM the host)
- Spin forever (CPU exhaustion)
- Connect to external servers (data exfiltration)
- Read `/etc/passwd` or `/proc/self/environ` (info leak)
- Fork bomb the system

Traditional solutions require root access:
- Docker containers
- cgroups
- User namespaces
- seccomp profiles

But what if you're running on AWS Lambda? No root. No Docker. No kernel modules.

## The Solution: Wrapper-Based Sandbox

The key insight: Go's `exec.Cmd` doesn't support pre-exec hooks, but we can
use a **wrapper binary** that applies restrictions before exec'ing the target.

```
shimmy → sandbox-wrapper → python3 student.py
              ↓
         1. setrlimit()
         2. seccomp()
         3. exec()
```

### Why a Wrapper?

In Unix, there's a critical moment between `fork()` and `exec()`:

```
Parent process
    |
    fork()
    |
    +---> Child process
              |
              setrlimit()    <-- Apply restrictions HERE
              seccomp()
              |
              exec(python3)  <-- Target inherits restrictions
```

Go's runtime doesn't expose this hook. The wrapper is our way in.

## Implementation

### Resource Limits (rlimits)

```go
limits := []struct{ res int; val uint64 }{
    {unix.RLIMIT_CPU, 5},           // 5 seconds CPU time
    {unix.RLIMIT_AS, 256*1024*1024}, // 256MB memory
    {unix.RLIMIT_FSIZE, 10*1024*1024}, // 10MB file size
    {unix.RLIMIT_NOFILE, 100},       // 100 file descriptors
}
for _, l := range limits {
    unix.Setrlimit(l.res, &unix.Rlimit{Cur: l.val, Max: l.val})
}
```

When Python tries to allocate 500MB with a 256MB limit:
```
>>> x = 'A' * (500 * 1024 * 1024)
MemoryError
```

### System Call Filtering (seccomp)

seccomp-bpf lets us whitelist/blacklist syscalls:

```go
blocked := []uint32{
    unix.SYS_SOCKET,   // No creating sockets
    unix.SYS_CONNECT,  // No connecting
    unix.SYS_BIND,     // No binding
    // ...
}

// Build BPF filter
insns := []unix.SockFilter{
    {Code: BPF_LD | BPF_W | BPF_ABS, K: 0},  // Load syscall number
}
for _, nr := range blocked {
    // If syscall == nr, return EPERM
    insns = append(insns,
        unix.SockFilter{Code: BPF_JMP | BPF_JEQ | BPF_K, Jt: 0, Jf: 1, K: nr},
        unix.SockFilter{Code: BPF_RET | BPF_K, K: SECCOMP_RET_ERRNO | 1},
    )
}
// Default: allow
insns = append(insns, unix.SockFilter{Code: BPF_RET | BPF_K, K: SECCOMP_RET_ALLOW})
```

Now when Python tries to create a socket:
```
>>> import socket
>>> s = socket.socket()
OSError: [Errno 1] Operation not permitted
```

### Environment Sanitization

One subtle leak: `/proc/self/environ` exposes the process's environment variables.

```python
with open('/proc/self/environ', 'rb') as f:
    print(f.read())
# b'AWS_SECRET_KEY=super-secret-12345\x00...'
```

The fix: clear the environment before exec:

```go
if cleanEnv {
    cmd.Env = []string{
        "PATH=/usr/bin:/bin",
        "HOME=/tmp",
    }
}
```

## Test Results

| Attack | Status | How It's Blocked |
|--------|--------|-----------------|
| Memory bomb | ✅ Blocked | RLIMIT_AS |
| CPU exhaustion | ✅ Blocked | RLIMIT_CPU |
| Network access | ✅ Blocked | seccomp |
| File explosion | ✅ Blocked | RLIMIT_FSIZE |
| FD exhaustion | ✅ Blocked | RLIMIT_NOFILE |
| Fork bomb | ⚠️ Limited | RLIMIT_NPROC (per-user) |
| Env leak | ✅ Blocked | -clean-env |

**Pass rate: 87% (13/15)**

### Performance

| Test | Overhead |
|------|----------|
| Echo | +1.24ms |
| Python startup | +1.59ms |
| With imports | +1.76ms |

**Average: ~1.5ms** - negligible for real workloads.

## Limitations

### Fork Bomb

`RLIMIT_NPROC` limits processes **per UID**, not per sandbox.
In a container with few processes, the limit may not trigger.

**Mitigation:** Accept the limitation. Lambda has its own process limits.

### /proc Information

Can't easily block `/proc` reads via seccomp (would need path inspection).

**Mitigation:** Environment sanitization prevents the most sensitive leaks.

## Usage

```bash
# Build
go build -o sandbox-wrapper ./cmd/sandbox-wrapper

# Run with restrictions
./sandbox-wrapper \
    -cpu 5 \
    -mem 256 \
    -no-network \
    -clean-env \
    -- python3 student.py
```

## Conclusion

You don't need root to build a sandbox. With rlimits, seccomp, and careful
environment handling, you can achieve 87% attack coverage with ~1.5ms overhead.

The remaining gaps (fork bombs, /proc reads) are mitigated by the platform's
existing limits and are acceptable tradeoffs for a userspace-only solution.

---

*Code: [github.com/lambda-feedback/shimmy/sandbox](https://github.com/lambda-feedback/shimmy)*

*Author: Akashi, 2026-03-09*
