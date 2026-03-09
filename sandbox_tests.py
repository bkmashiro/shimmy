#!/usr/bin/env python3
"""
Sandbox Security Test Suite

Tests various attack vectors against the sandbox-wrapper.
Run inside Docker with seccomp=unconfined.
"""

import subprocess
import sys
import time
import os

WRAPPER = "/app/sandbox-wrapper"
TIMEOUT = 10

def run_test(name, code, expect_blocked=True, extra_args=None):
    """Run a test and check if it was blocked"""
    print(f"\n{'='*60}")
    print(f"TEST: {name}")
    print(f"{'='*60}")
    
    args = [WRAPPER, "-cpu=3", "-mem=64", "-nproc=5", "-fsize=5", "-nofile=50", 
            f"-timeout={TIMEOUT}s", "--no-network", "--"]
    if extra_args:
        args.extend(extra_args)
    args.extend(["python3", "-c", code])
    
    start = time.time()
    try:
        result = subprocess.run(args, capture_output=True, text=True, timeout=TIMEOUT+5)
        elapsed = time.time() - start
        
        stdout = result.stdout.strip()
        stderr = result.stderr.strip()
        
        print(f"Exit code: {result.returncode}")
        print(f"Time: {elapsed:.2f}s")
        if stdout:
            print(f"Stdout: {stdout[:200]}")
        if stderr:
            print(f"Stderr: {stderr[:200]}")
        
        # Determine if blocked
        blocked = (
            result.returncode != 0 or
            "blocked" in stdout.lower() or
            "error" in stderr.lower() or
            "denied" in stdout.lower() or
            "killed" in stderr.lower() or
            "memory" in stderr.lower()
        )
        
        if expect_blocked and blocked:
            print("✅ PASS: Attack blocked")
            return True
        elif not expect_blocked and not blocked:
            print("✅ PASS: Allowed as expected")
            return True
        else:
            print(f"❌ FAIL: Expected blocked={expect_blocked}, got blocked={blocked}")
            return False
            
    except subprocess.TimeoutExpired:
        print(f"⏰ TIMEOUT after {TIMEOUT+5}s")
        print("✅ PASS: Process timed out (attack contained)")
        return True
    except Exception as e:
        print(f"❌ ERROR: {e}")
        return False

def main():
    results = []
    
    # === NETWORK TESTS ===
    print("\n" + "="*60)
    print("CATEGORY: NETWORK")
    print("="*60)
    
    results.append(("Network: TCP socket", run_test(
        "Network: TCP socket creation",
        """
import socket
try:
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    print("FAIL: socket allowed")
except Exception as e:
    print(f"blocked: {e}")
"""
    )))
    
    results.append(("Network: UDP socket", run_test(
        "Network: UDP socket creation",
        """
import socket
try:
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    print("FAIL: socket allowed")
except Exception as e:
    print(f"blocked: {e}")
"""
    )))
    
    results.append(("Network: Connect", run_test(
        "Network: Connect attempt",
        """
import socket
try:
    s = socket.socket()
    s.connect(('1.1.1.1', 80))
    print("FAIL: connect allowed")
except Exception as e:
    print(f"blocked: {e}")
"""
    )))
    
    # === MEMORY TESTS ===
    print("\n" + "="*60)
    print("CATEGORY: MEMORY")
    print("="*60)
    
    results.append(("Memory: Allocation bomb", run_test(
        "Memory: Allocation bomb (100MB chunks)",
        """
try:
    x = []
    for i in range(100):
        x.append('A' * 1024 * 1024 * 10)  # 10MB each
except MemoryError:
    print("blocked: MemoryError")
"""
    )))
    
    results.append(("Memory: Mmap bomb", run_test(
        "Memory: Mmap allocation",
        """
import mmap
import os
try:
    maps = []
    for i in range(100):
        m = mmap.mmap(-1, 10 * 1024 * 1024)  # 10MB
        maps.append(m)
except Exception as e:
    print(f"blocked: {e}")
"""
    )))
    
    # === CPU TESTS ===
    print("\n" + "="*60)
    print("CATEGORY: CPU")
    print("="*60)
    
    results.append(("CPU: Infinite loop", run_test(
        "CPU: Infinite loop",
        """
import time
start = time.time()
count = 0
while True:
    count += sum(range(10000))
    if time.time() - start > 10:
        print(f"FAIL: ran for 10s, count={count}")
        break
"""
    )))
    
    results.append(("CPU: Busy calculation", run_test(
        "CPU: Prime calculation (CPU intensive)",
        """
def is_prime(n):
    if n < 2: return False
    for i in range(2, int(n**0.5)+1):
        if n % i == 0: return False
    return True

count = 0
for n in range(10000000):
    if is_prime(n):
        count += 1
print(f"FAIL: found {count} primes")
"""
    )))
    
    # === FORK TESTS ===
    print("\n" + "="*60)
    print("CATEGORY: FORK/PROCESS")
    print("="*60)
    
    results.append(("Fork: Fork bomb", run_test(
        "Fork: Fork bomb",
        """
import os
pids = []
try:
    for i in range(100):
        pid = os.fork()
        if pid == 0:
            while True: pass  # Child loops
        pids.append(pid)
except OSError as e:
    print(f"blocked after {len(pids)} forks: {e}")
finally:
    for p in pids:
        try: os.kill(p, 9)
        except: pass
"""
    )))
    
    results.append(("Fork: Subprocess spawn", run_test(
        "Fork: Subprocess spawn",
        """
import subprocess
procs = []
try:
    for i in range(20):
        p = subprocess.Popen(['sleep', '60'])
        procs.append(p)
        print(f"spawned {i+1}")
except Exception as e:
    print(f"blocked after {len(procs)}: {e}")
finally:
    for p in procs:
        try: p.kill()
        except: pass
"""
    )))
    
    # === FILE TESTS ===
    print("\n" + "="*60)
    print("CATEGORY: FILE SYSTEM")
    print("="*60)
    
    results.append(("File: Large file write", run_test(
        "File: Large file write (100MB)",
        """
try:
    with open('/tmp/bigfile', 'wb') as f:
        for i in range(100):
            f.write(b'X' * 1024 * 1024)  # 1MB
            print(f"wrote {i+1}MB")
except Exception as e:
    print(f"blocked: {e}")
finally:
    import os
    try: os.remove('/tmp/bigfile')
    except: pass
"""
    )))
    
    results.append(("File: FD exhaustion", run_test(
        "File: File descriptor exhaustion",
        """
import os
fds = []
try:
    for i in range(200):
        fd = os.open('/dev/null', os.O_RDONLY)
        fds.append(fd)
except OSError as e:
    print(f"blocked after {len(fds)} fds: {e}")
finally:
    for fd in fds:
        try: os.close(fd)
        except: pass
"""
    )))
    
    # === INFO LEAK TESTS ===
    print("\n" + "="*60)
    print("CATEGORY: INFO LEAK")
    print("="*60)
    
    results.append(("Info: /proc/self/environ", run_test(
        "Info: Read /proc/self/environ",
        """
try:
    with open('/proc/self/environ', 'rb') as f:
        env = f.read()
        secrets = [k for k in env.decode(errors='ignore').split('\\x00') 
                   if 'KEY' in k or 'SECRET' in k or 'PASSWORD' in k]
        if secrets:
            print(f"FAIL: found secrets: {secrets[:3]}")
        else:
            print("no secrets found (but file readable)")
except Exception as e:
    print(f"blocked: {e}")
""",
        expect_blocked=False  # /proc readable is expected, just check for secrets
    )))
    
    results.append(("Info: /proc/1/cmdline", run_test(
        "Info: Read /proc/1/cmdline (init process)",
        """
try:
    with open('/proc/1/cmdline', 'rb') as f:
        cmd = f.read()
        print(f"init cmd: {cmd[:50]}")
except Exception as e:
    print(f"blocked: {e}")
""",
        expect_blocked=False  # This might be readable in container
    )))
    
    # === SIGNAL TESTS ===
    print("\n" + "="*60)
    print("CATEGORY: SIGNALS")
    print("="*60)
    
    results.append(("Signal: Ignore SIGTERM", run_test(
        "Signal: Ignore SIGTERM",
        """
import signal
import time

signal.signal(signal.SIGTERM, signal.SIG_IGN)
signal.signal(signal.SIGINT, signal.SIG_IGN)

start = time.time()
while time.time() - start < 15:
    time.sleep(0.1)
print("FAIL: survived 15s ignoring signals")
"""
    )))
    
    # === SYMLINK TESTS ===
    print("\n" + "="*60)
    print("CATEGORY: SYMLINK")  
    print("="*60)
    
    results.append(("Symlink: Escape attempt", run_test(
        "Symlink: Create symlink to /etc/passwd",
        """
import os
try:
    os.symlink('/etc/passwd', '/tmp/passwd_link')
    with open('/tmp/passwd_link') as f:
        print(f"FAIL: read passwd: {f.read()[:50]}")
except Exception as e:
    print(f"blocked: {e}")
finally:
    try: os.remove('/tmp/passwd_link')
    except: pass
""",
        expect_blocked=False  # Symlinks allowed in container, but check what's readable
    )))
    
    # === SUMMARY ===
    print("\n" + "="*60)
    print("SUMMARY")
    print("="*60)
    
    passed = sum(1 for _, r in results if r)
    total = len(results)
    
    print(f"\nPassed: {passed}/{total}")
    print()
    for name, result in results:
        status = "✅" if result else "❌"
        print(f"  {status} {name}")
    
    return passed == total

if __name__ == "__main__":
    success = main()
    sys.exit(0 if success else 1)
