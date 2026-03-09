#!/usr/bin/env python3
"""
Test if we can load a new seccomp filter
"""
import sys
print("Script starting...", flush=True)

import ctypes
import struct
import json

print("Imports done", flush=True)

# Seccomp constants
SECCOMP_MODE_FILTER = 2
PR_SET_SECCOMP = 22
PR_GET_SECCOMP = 21
PR_SET_NO_NEW_PRIVS = 38
PR_GET_NO_NEW_PRIVS = 39

# BPF constants
BPF_RET = 0x06
BPF_K = 0x00
SECCOMP_RET_ALLOW = 0x7fff0000

def bpf_stmt(code, k):
    return struct.pack('HBBI', code, 0, 0, k)

def test_seccomp_load():
    results = {}
    
    print("Loading libc...", flush=True)
    try:
        libc = ctypes.CDLL('libc.so.6', use_errno=True)
        print("libc loaded", flush=True)
    except Exception as e:
        print(f"libc error: {e}", flush=True)
        return {'error': str(e)}
    
    # Check current seccomp mode
    print("Checking seccomp mode...", flush=True)
    current_mode = libc.prctl(PR_GET_SECCOMP)
    results['current_seccomp_mode'] = current_mode
    print(f"Current mode: {current_mode}", flush=True)
    
    # Check no_new_privs
    nnp = libc.prctl(PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
    results['no_new_privs'] = nnp
    print(f"no_new_privs: {nnp}", flush=True)
    
    # Create minimal filter (just allow all)
    prog = bpf_stmt(BPF_RET | BPF_K, SECCOMP_RET_ALLOW)
    
    # sock_fprog structure
    class sock_fprog(ctypes.Structure):
        _fields_ = [
            ('len', ctypes.c_ushort),
            ('filter', ctypes.c_void_p)
        ]
    
    filter_buf = ctypes.create_string_buffer(prog)
    fprog = sock_fprog()
    fprog.len = 1
    fprog.filter = ctypes.cast(filter_buf, ctypes.c_void_p).value
    
    # Ensure no_new_privs
    if nnp != 1:
        print("Setting no_new_privs...", flush=True)
        ret = libc.prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0)
        results['set_nnp'] = 'success' if ret == 0 else f'failed: {ctypes.get_errno()}'
    else:
        results['set_nnp'] = 'already set'
    
    # Try to load filter
    print("Attempting to load seccomp filter...", flush=True)
    ret = libc.prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, ctypes.byref(fprog))
    
    if ret == 0:
        results['load_filter'] = 'SUCCESS'
        print("SUCCESS! Can load filters!", flush=True)
    else:
        errno = ctypes.get_errno()
        results['load_filter'] = f'FAILED: errno={errno}'
        print(f"FAILED: errno={errno}", flush=True)
    
    return results

if __name__ == '__main__':
    print("=" * 40, flush=True)
    print("Seccomp Filter Load Test", flush=True)
    print("=" * 40, flush=True)
    
    results = test_seccomp_load()
    print("\nResults:", flush=True)
    print(json.dumps(results, indent=2), flush=True)
    
    if 'SUCCESS' in str(results.get('load_filter', '')):
        print("\n✅ Can add seccomp filters!", flush=True)
    else:
        print("\n❌ Cannot load new filters", flush=True)
