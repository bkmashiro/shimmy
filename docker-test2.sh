#!/bin/bash
set -e

cd /app/cmd/sandbox-wrapper
go mod tidy
go build -o /sandbox-wrapper . 2>/dev/null

echo "=== Testing fork limit (stricter) ==="
echo "Setting NPROC=2..."
/sandbox-wrapper -cpu 5 -mem 128 -nproc 2 -timeout 10s -- python3 -c "
import os
import sys
pids = []
try:
    for i in range(10):
        pid = os.fork()
        if pid == 0:
            import time
            time.sleep(1)
            os._exit(0)
        pids.append(pid)
        print(f'Forked child {i+1}', flush=True)
except OSError as e:
    print(f'Fork blocked after {len(pids)} children: {e}')
finally:
    # Wait for children
    for p in pids:
        try:
            os.waitpid(p, 0)
        except:
            pass
" 2>&1 || echo "(process exited)"

echo ""
echo "=== Testing seccomp (network block) ==="
cat > /tmp/net_test.py << 'PYEOF'
import socket
try:
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.settimeout(2)
    s.connect(('1.1.1.1', 80))
    print("Network: ALLOWED")
    s.close()
except Exception as e:
    print(f"Network: BLOCKED - {e}")
PYEOF

# Without network block
echo "Without --no-network:"
/sandbox-wrapper -cpu 5 -mem 128 -timeout 5s -- python3 /tmp/net_test.py 2>&1 || true

echo ""
echo "=== Testing file size limit ==="
/sandbox-wrapper -cpu 5 -mem 128 -fsize 1 -timeout 5s -- python3 -c "
try:
    with open('/tmp/bigfile', 'wb') as f:
        for i in range(100):
            f.write(b'A' * 1024 * 1024)  # 1MB
            print(f'Wrote {i+1} MB')
except Exception as e:
    print(f'File write blocked: {e}')
" 2>&1 || echo "(process exited)"

echo ""
echo "=== All tests complete ==="
