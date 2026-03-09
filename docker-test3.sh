#!/bin/bash
set -e

cd /app/cmd/sandbox-wrapper
go mod tidy 2>/dev/null
go build -o /sandbox-wrapper . 2>&1

echo "=== Test 1: Network WITHOUT --no-network ==="
/sandbox-wrapper -timeout 5s -- python3 -c "
import socket
try:
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.settimeout(2)
    s.connect(('1.1.1.1', 80))
    print('Network: ALLOWED')
    s.close()
except Exception as e:
    print(f'Network: BLOCKED - {e}')
" 2>&1

echo ""
echo "=== Test 2: Network WITH --no-network (seccomp) ==="
/sandbox-wrapper -timeout 5s --no-network -- python3 -c "
import socket
try:
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    print('Network: ALLOWED')
except Exception as e:
    print(f'Network: BLOCKED - {e}')
" 2>&1

echo ""
echo "=== Test 3: Combined sandbox (cpu + mem + no-network) ==="
/sandbox-wrapper -cpu 3 -mem 64 --no-network -timeout 5s -- python3 -c "
import socket
print('Testing network...')
try:
    s = socket.socket()
    print('FAIL: socket allowed')
except:
    print('OK: socket blocked')

print('Testing memory...')
try:
    x = []
    for i in range(100):
        x.append('A' * 1024 * 1024)
except MemoryError:
    print('OK: memory limited')

print('Done!')
" 2>&1

echo ""
echo "=== All seccomp tests complete ==="
