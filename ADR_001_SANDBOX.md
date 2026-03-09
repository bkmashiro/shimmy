# ADR-001: Userspace Sandbox for Code Evaluation

**Status:** Accepted  
**Date:** 2026-03-09  
**Author:** Akashi (CTO)

---

## Context

Shimmy executes untrusted student code on AWS Lambda. We need process isolation
to prevent resource exhaustion, network access, and information leakage.

Lambda constraints:
- No root access
- No Docker-in-Docker
- No kernel module loading
- No user namespace creation
- Existing seccomp profile (may conflict)

## Decision

We will use a **wrapper-based sandbox** that applies rlimits and seccomp-bpf
before exec'ing the target command.

### Why Wrapper?

Go's `exec.Cmd` doesn't support pre-exec hooks. The wrapper provides the
fork→setrlimit→seccomp→exec sequence we need.

### Security Controls

| Control | Implementation | Coverage |
|---------|---------------|----------|
| Memory | RLIMIT_AS | ✅ 100% |
| CPU | RLIMIT_CPU | ✅ 100% |
| Files | RLIMIT_FSIZE | ✅ 100% |
| FDs | RLIMIT_NOFILE | ✅ 100% |
| Network | seccomp | ✅ 100% |
| Fork | RLIMIT_NPROC | ⚠️ ~50% |
| Env leak | -clean-env | ✅ 100% |

### Trade-offs

**Pros:**
- No root required
- Works on Lambda
- ~1.5ms overhead (negligible)
- 87% attack coverage

**Cons:**
- Fork limit is per-user, not per-sandbox
- Adds binary to deployment package (~2MB)
- Lambda seccomp compatibility untested

## Alternatives Considered

### 1. Docker-in-Docker
- **Rejected:** Not available on Lambda

### 2. Firecracker MicroVMs
- **Rejected:** Requires /dev/kvm, not available

### 3. gVisor
- **Rejected:** Requires kernel features not available

### 4. WASM (Pyodide)
- **Deferred:** Worth exploring for future, but Python support limited

### 5. Pure seccomp (no rlimits)
- **Rejected:** seccomp can't limit memory/CPU usage

## Consequences

1. Must include `sandbox-wrapper` in Lambda deployment
2. Set `SHIMMY_SANDBOX=1` to enable
3. Fork bombs partially mitigated (Lambda has own limits)
4. Must test seccomp compatibility in Lambda

## Related

- [SANDBOX_REPORT.md](./SANDBOX_REPORT.md) - Full technical report
- [BLOG_POST.md](./BLOG_POST.md) - Deep dive article
- [PR_DIFF.md](./PR_DIFF.md) - Pull request description

---

*This document follows the [ADR format](https://adr.github.io/).*
