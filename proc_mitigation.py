#!/usr/bin/env python3
"""
/proc Information Leak Analysis and Mitigation

Analyzes what information is leaked through /proc and tests mitigations.
"""

import os
import subprocess

WRAPPER = "/app/sandbox-wrapper"

def check_proc_files():
    """Check what /proc files are readable"""
    print("="*60)
    print("PROC FILE ACCESSIBILITY")
    print("="*60)
    
    files = [
        ("/proc/self/environ", "Environment variables (SECRETS!)"),
        ("/proc/self/cmdline", "Command line arguments"),
        ("/proc/self/cwd", "Current working directory"),
        ("/proc/self/exe", "Executable path"),
        ("/proc/self/fd", "File descriptors"),
        ("/proc/self/maps", "Memory mappings"),
        ("/proc/self/status", "Process status"),
        ("/proc/self/limits", "Resource limits"),
        ("/proc/cpuinfo", "CPU information"),
        ("/proc/meminfo", "Memory information"),
        ("/proc/version", "Kernel version"),
        ("/proc/1/cmdline", "Init process cmdline"),
        ("/proc/1/environ", "Init process env"),
    ]
    
    for path, desc in files:
        try:
            if os.path.islink(path):
                target = os.readlink(path)
                print(f"✅ {path} -> {target}")
            elif os.path.isdir(path):
                contents = os.listdir(path)[:5]
                print(f"📁 {path} ({len(contents)} entries)")
            else:
                with open(path, 'rb') as f:
                    data = f.read(100)
                    preview = data[:50].decode(errors='replace').replace('\n', ' ').replace('\x00', ' ')
                    print(f"✅ {path}: {preview}...")
        except PermissionError:
            print(f"🔒 {path}: Permission denied")
        except FileNotFoundError:
            print(f"❌ {path}: Not found")
        except Exception as e:
            print(f"⚠️ {path}: {e}")

def test_env_sanitization():
    """Test if environment sanitization works"""
    print("\n" + "="*60)
    print("ENVIRONMENT SANITIZATION")
    print("="*60)
    
    # Set some "secret" env vars
    os.environ['AWS_SECRET_KEY'] = 'super-secret-key-12345'
    os.environ['DATABASE_PASSWORD'] = 'db-password-xyz'
    os.environ['API_TOKEN'] = 'token-abc-123'
    
    # Check what child process sees
    print("\nWith full environment:")
    result = subprocess.run(['env'], capture_output=True, text=True)
    secrets = [l for l in result.stdout.split('\n') if 'SECRET' in l or 'PASSWORD' in l or 'TOKEN' in l]
    print(f"  Secrets visible: {len(secrets)}")
    for s in secrets[:3]:
        print(f"    {s[:40]}...")
    
    # Test with env -i
    print("\nWith env -i (cleared):")
    result = subprocess.run(['env', '-i', 'env'], capture_output=True, text=True)
    print(f"  Environment vars: {len(result.stdout.strip().split(chr(10)))}")
    
    # Test with minimal env
    print("\nWith minimal env:")
    minimal_env = {
        'PATH': '/usr/bin:/bin',
        'HOME': '/tmp',
        'USER': 'sandbox',
    }
    result = subprocess.run(['env'], capture_output=True, text=True, env=minimal_env)
    print(f"  Environment vars: {len(result.stdout.strip().split(chr(10)))}")
    for line in result.stdout.strip().split('\n'):
        print(f"    {line}")

def test_seccomp_proc_block():
    """Test if we can block /proc reads with seccomp"""
    print("\n" + "="*60)
    print("SECCOMP /PROC BLOCKING (EXPERIMENTAL)")
    print("="*60)
    
    print("""
Blocking /proc via seccomp is complex because:
1. open("/proc/...") uses SYS_OPENAT
2. Can't easily filter by path in BPF
3. Would need argument inspection

Alternative approaches:
1. Mount /proc with hidepid=2 (requires root)
2. Use mount namespace (requires CAP_SYS_ADMIN)
3. Sanitize environment before exec
4. Use LD_PRELOAD to intercept open()

RECOMMENDATION: Environment sanitization is the practical choice.
""")

def main():
    check_proc_files()
    test_env_sanitization()
    test_seccomp_proc_block()
    
    print("\n" + "="*60)
    print("MITIGATION RECOMMENDATIONS")
    print("="*60)
    print("""
1. ENVIRONMENT SANITIZATION (Implemented)
   - Clear all env vars before exec
   - Pass only required vars explicitly
   - Example: env -i PATH=/usr/bin python3 script.py

2. WRAPPER MODIFICATION (TODO)
   - Add --sanitize-env flag
   - Clear env and set minimal PATH/HOME

3. /PROC RESTRICTIONS (Lambda-specific)
   - Lambda may already restrict /proc/1/environ
   - Test in real Lambda environment

4. DEFENSE IN DEPTH
   - Don't store secrets in env vars
   - Use IAM roles instead of API keys
   - Rotate secrets frequently
""")

if __name__ == "__main__":
    main()
