#!/usr/bin/env python3
"""
Fork Limit Analysis

RLIMIT_NPROC limits total processes per UID, not per process tree.
In containers, this is often ineffective.

Solutions:
1. seccomp block fork/clone syscalls
2. PID namespace (requires CAP_SYS_ADMIN)
3. cgroups pids controller (requires cgroups access)
"""

import os
import subprocess

print("=== Fork Limit Analysis ===\n")

# Check current limits
print("Current RLIMIT_NPROC:")
result = subprocess.run(["sh", "-c", "ulimit -u"], capture_output=True, text=True)
print(f"  ulimit -u: {result.stdout.strip()}")

print("\nCurrent user processes:")
result = subprocess.run(["ps", "aux"], capture_output=True, text=True)
lines = result.stdout.strip().split('\n')
print(f"  Total processes: {len(lines)-1}")

print("\n=== Solution Options ===")
print("""
1. SECCOMP BLOCK CLONE/FORK
   - Block SYS_CLONE with CLONE_NEWPID flag
   - Or limit clone flags to prevent new processes
   - PRO: Works in Lambda
   - CON: May break legitimate child processes

2. PID NAMESPACE (--pid=host vs isolated)
   - Requires unshare(CLONE_NEWPID)
   - PRO: Clean isolation
   - CON: Requires CAP_SYS_ADMIN, not available in Lambda

3. CGROUPS PIDS CONTROLLER
   - /sys/fs/cgroup/pids.max
   - PRO: Precise limit
   - CON: Requires cgroups access, Lambda uses its own cgroups

4. PROCESS COUNT MONITORING (Best for Lambda)
   - Wrapper monitors child count
   - Kill all on threshold
   - PRO: Works everywhere
   - CON: Reactive, not preventive
   
5. SECCOMP + PTRACE (Complex)
   - Intercept fork/clone syscalls
   - Count and block
   - CON: Very complex, performance impact

RECOMMENDATION FOR LAMBDA:
  Use Option 4 (monitoring) or accept the limitation.
  Lambda's existing limits provide some protection.
""")
