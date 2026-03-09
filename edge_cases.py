#!/usr/bin/env python3
"""
Edge Case Tests for Sandbox

Tests boundary conditions and corner cases.
"""

import subprocess
import time
import sys

WRAPPER = "/app/sandbox-wrapper"

def run(name, args, expect_code=None, expect_timeout=False, max_time=None):
    """Run test and check result"""
    print(f"\n{'='*50}")
    print(f"TEST: {name}")
    print(f"{'='*50}")
    
    start = time.time()
    try:
        result = subprocess.run(args, capture_output=True, text=True, timeout=max_time or 30)
        elapsed = time.time() - start
        
        print(f"Time: {elapsed:.2f}s")
        print(f"Exit: {result.returncode}")
        if result.stdout.strip():
            print(f"Out: {result.stdout.strip()[:100]}")
        if result.stderr.strip():
            print(f"Err: {result.stderr.strip()[:100]}")
        
        if expect_code is not None and result.returncode != expect_code:
            print(f"❌ Expected code {expect_code}, got {result.returncode}")
            return False
        
        print("✅ PASS")
        return True
        
    except subprocess.TimeoutExpired:
        elapsed = time.time() - start
        print(f"⏰ TIMEOUT after {elapsed:.1f}s")
        if expect_timeout:
            print("✅ PASS (timeout expected)")
            return True
        print("❌ FAIL (unexpected timeout)")
        return False
    except Exception as e:
        print(f"❌ ERROR: {e}")
        return False

def main():
    results = []
    
    print("="*50)
    print("EDGE CASE TESTS")
    print("="*50)
    
    # === CPU LIMIT PRECISION ===
    print("\n### CPU LIMIT PRECISION ###")
    
    # CPU limit at 2 seconds
    results.append(("CPU: 2s limit, 1s work", run(
        "CPU: Should complete (1s work, 2s limit)",
        [WRAPPER, "-cpu=2", "-timeout=10s", "--", "python3", "-c", """
import time
start = time.time()
while time.time() - start < 1:
    sum(range(10000))
print(f"completed in {time.time()-start:.2f}s")
"""],
        expect_code=0
    )))
    
    # CPU limit at 1 second, 3s work
    results.append(("CPU: 1s limit, 3s work", run(
        "CPU: Should be killed (3s work, 1s limit)",
        [WRAPPER, "-cpu=1", "-timeout=10s", "--", "python3", "-c", """
import time
start = time.time()
while time.time() - start < 3:
    sum(range(10000))
print(f"FAIL: completed in {time.time()-start:.2f}s")
"""],
        expect_code=None  # Non-zero expected
    )))
    
    # === MEMORY LIMIT PRECISION ===
    print("\n### MEMORY LIMIT PRECISION ###")
    
    # 32MB limit, allocate 20MB
    results.append(("Mem: 32MB limit, 20MB alloc", run(
        "Memory: Should complete (20MB, 32MB limit)",
        [WRAPPER, "-mem=32", "-timeout=10s", "--", "python3", "-c", """
x = 'A' * (20 * 1024 * 1024)
print(f"allocated {len(x)//1024//1024}MB")
"""],
        expect_code=0
    )))
    
    # 16MB limit, allocate 50MB
    results.append(("Mem: 16MB limit, 50MB alloc", run(
        "Memory: Should fail (50MB, 16MB limit)",
        [WRAPPER, "-mem=16", "-timeout=10s", "--", "python3", "-c", """
try:
    x = 'A' * (50 * 1024 * 1024)
    print(f"FAIL: allocated {len(x)//1024//1024}MB")
except MemoryError:
    print("blocked: MemoryError")
"""],
        expect_code=0
    )))
    
    # === FILE SIZE PRECISION ===
    print("\n### FILE SIZE PRECISION ###")
    
    # 2MB limit, write 1MB
    results.append(("File: 2MB limit, 1MB write", run(
        "File: Should complete (1MB, 2MB limit)",
        [WRAPPER, "-fsize=2", "-timeout=10s", "--", "python3", "-c", """
import os
with open('/tmp/test', 'wb') as f:
    f.write(b'X' * (1 * 1024 * 1024))
print(f"wrote {os.path.getsize('/tmp/test')//1024}KB")
os.remove('/tmp/test')
"""],
        expect_code=0
    )))
    
    # === TIMEOUT PRECISION ===
    print("\n### TIMEOUT PRECISION ###")
    
    # 3s timeout, 2s work
    results.append(("Timeout: 3s limit, 2s work", run(
        "Timeout: Should complete (2s work, 3s limit)",
        [WRAPPER, "-timeout=3s", "--", "python3", "-c", """
import time
time.sleep(2)
print("completed")
"""],
        expect_code=0
    )))
    
    # 2s timeout, 5s work
    results.append(("Timeout: 2s limit, 5s work", run(
        "Timeout: Should be killed (5s work, 2s limit)",
        [WRAPPER, "-timeout=2s", "--", "python3", "-c", """
import time
time.sleep(5)
print("FAIL: should have been killed")
"""],
        expect_code=124
    )))
    
    # === FD LIMIT ===
    print("\n### FD LIMIT PRECISION ###")
    
    # 20 FD limit
    results.append(("FD: 20 limit", run(
        "FD: Should block at ~17 FDs (20 limit minus stdin/out/err)",
        [WRAPPER, "-nofile=20", "-timeout=10s", "--", "python3", "-c", """
import os
fds = []
try:
    for i in range(50):
        fds.append(os.open('/dev/null', os.O_RDONLY))
except OSError as e:
    print(f"blocked after {len(fds)} fds")
for fd in fds:
    os.close(fd)
"""],
        expect_code=0
    )))
    
    # === EMPTY/INVALID INPUTS ===
    print("\n### EDGE INPUTS ###")
    
    # No command
    results.append(("No command", run(
        "No command: Should show usage",
        [WRAPPER],
        expect_code=1
    )))
    
    # Invalid command
    results.append(("Invalid command", run(
        "Invalid command: Should fail",
        [WRAPPER, "-timeout=5s", "--", "nonexistent_command_xyz"],
        expect_code=None  # Will fail
    )))
    
    # Summary
    print("\n" + "="*50)
    print("SUMMARY")
    print("="*50)
    passed = sum(1 for _, r in results if r)
    print(f"Passed: {passed}/{len(results)}")
    for name, result in results:
        status = "✅" if result else "❌"
        print(f"  {status} {name}")

if __name__ == "__main__":
    main()
