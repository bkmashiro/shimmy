"""
Lambda Capability Probe
Deploy to Lambda to discover actual syscall/capability restrictions
"""
import os
import sys
import json
import ctypes
import subprocess
from datetime import datetime

def probe_capabilities():
    """Probe Linux capabilities"""
    results = {}
    
    # Read /proc/self/status for capabilities
    try:
        with open('/proc/self/status', 'r') as f:
            for line in f:
                if any(x in line for x in ['Cap', 'Seccomp', 'NoNewPrivs']):
                    key, val = line.strip().split(':', 1)
                    results[key] = val.strip()
    except Exception as e:
        results['status_error'] = str(e)
    
    return results

def probe_seccomp():
    """Check seccomp status via prctl"""
    results = {}
    
    try:
        libc = ctypes.CDLL('libc.so.6', use_errno=True)
        
        # prctl(PR_GET_SECCOMP) = prctl(21)
        PR_GET_SECCOMP = 21
        ret = libc.prctl(PR_GET_SECCOMP)
        results['seccomp_mode'] = ret  # 0=off, 1=strict, 2=filter
        
        # prctl(PR_GET_NO_NEW_PRIVS) = prctl(39)
        PR_GET_NO_NEW_PRIVS = 39
        ret = libc.prctl(PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
        results['no_new_privs'] = ret
        
        # Try to set seccomp (will fail if already filtered)
        PR_SET_SECCOMP = 22
        SECCOMP_MODE_FILTER = 2
        # Don't actually try to set, just report current state
        
    except Exception as e:
        results['prctl_error'] = str(e)
    
    return results

def probe_fork():
    """Test if fork() works"""
    results = {}
    
    try:
        pid = os.fork()
        if pid == 0:
            # Child
            os._exit(0)
        else:
            # Parent
            os.waitpid(pid, 0)
            results['fork'] = 'allowed'
    except OSError as e:
        results['fork'] = f'blocked: {e}'
    except Exception as e:
        results['fork'] = f'error: {e}'
    
    return results

def probe_mount():
    """Test if mount-related syscalls work"""
    results = {}
    
    try:
        # Try to create a tmpfs (will likely fail)
        import tempfile
        with tempfile.TemporaryDirectory() as tmpdir:
            ret = subprocess.run(
                ['mount', '-t', 'tmpfs', 'none', tmpdir],
                capture_output=True,
                timeout=5
            )
            results['mount'] = 'allowed' if ret.returncode == 0 else f'blocked: {ret.stderr.decode()}'
    except FileNotFoundError:
        results['mount'] = 'mount command not found'
    except Exception as e:
        results['mount'] = f'error: {e}'
    
    return results

def probe_namespace():
    """Test namespace capabilities"""
    results = {}
    
    try:
        libc = ctypes.CDLL('libc.so.6', use_errno=True)
        
        # unshare(CLONE_NEWNS) 
        CLONE_NEWNS = 0x00020000
        ret = libc.unshare(CLONE_NEWNS)
        if ret == 0:
            results['unshare_newns'] = 'allowed'
        else:
            errno = ctypes.get_errno()
            results['unshare_newns'] = f'blocked: errno={errno}'
    except Exception as e:
        results['unshare_newns'] = f'error: {e}'
    
    return results

def probe_filesystem():
    """Check filesystem access"""
    results = {}
    
    # /tmp access
    try:
        test_file = '/tmp/lambda_probe_test'
        with open(test_file, 'w') as f:
            f.write('test')
        os.remove(test_file)
        results['tmp_write'] = 'allowed'
    except Exception as e:
        results['tmp_write'] = f'blocked: {e}'
    
    # Root filesystem
    try:
        with open('/etc/passwd', 'r') as f:
            f.read(1)
        results['etc_read'] = 'allowed'
    except Exception as e:
        results['etc_read'] = f'blocked: {e}'
    
    # Try to write outside /tmp
    try:
        with open('/var/test', 'w') as f:
            f.write('test')
        results['var_write'] = 'allowed (unexpected!)'
        os.remove('/var/test')
    except Exception as e:
        results['var_write'] = f'blocked: {e}'
    
    return results

def probe_network():
    """Check network capabilities"""
    results = {}
    
    try:
        import socket
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(2)
        s.connect(('1.1.1.1', 80))
        s.close()
        results['outbound_tcp'] = 'allowed'
    except Exception as e:
        results['outbound_tcp'] = f'blocked/timeout: {e}'
    
    return results

def probe_process():
    """Check process-related syscalls"""
    results = {}
    
    # getpid, getuid
    results['pid'] = os.getpid()
    results['uid'] = os.getuid()
    results['gid'] = os.getgid()
    results['euid'] = os.geteuid()
    
    # Check if we can ptrace
    try:
        libc = ctypes.CDLL('libc.so.6', use_errno=True)
        PTRACE_TRACEME = 0
        ret = libc.ptrace(PTRACE_TRACEME, 0, 0, 0)
        if ret == 0:
            results['ptrace'] = 'allowed'
        else:
            errno = ctypes.get_errno()
            results['ptrace'] = f'blocked: errno={errno}'
    except Exception as e:
        results['ptrace'] = f'error: {e}'
    
    return results

def handler(event, context):
    """Lambda handler"""
    results = {
        'timestamp': datetime.utcnow().isoformat(),
        'python_version': sys.version,
        'capabilities': probe_capabilities(),
        'seccomp': probe_seccomp(),
        'fork': probe_fork(),
        'mount': probe_mount(),
        'namespace': probe_namespace(),
        'filesystem': probe_filesystem(),
        'network': probe_network(),
        'process': probe_process(),
    }
    
    return {
        'statusCode': 200,
        'body': json.dumps(results, indent=2, default=str)
    }

if __name__ == '__main__':
    # Local test
    result = handler({}, None)
    print(result['body'])
