#!/bin/bash
set -e

echo "=== Building sandbox-wrapper ==="
cd /app/cmd/sandbox-wrapper
go mod tidy
go build -o /sandbox-wrapper .

echo ""
echo "=== Testing basic execution ==="
/sandbox-wrapper -cpu 5 -mem 128 -timeout 5s -- echo "Hello from sandbox"

echo ""
echo "=== Testing memory limit ==="
/sandbox-wrapper -cpu 5 -mem 64 -timeout 5s -- python3 -c "
try:
    x = []
    for i in range(1000):
        x.append('A' * 1024 * 1024)
except MemoryError:
    print('Memory limit hit!')
" || echo "Process terminated (expected)"

echo ""
echo "=== Testing CPU limit ==="
/sandbox-wrapper -cpu 2 -mem 128 -timeout 10s -- python3 -c "
import time
start = time.time()
count = 0
while True:
    count += sum(range(10000))
    if time.time() - start > 5:
        break
print(f'Iterations: {count}')
" || echo "Process terminated (expected - CPU limit)"

echo ""
echo "=== Testing fork limit ==="
/sandbox-wrapper -cpu 5 -mem 128 -nproc 3 -timeout 5s -- python3 -c "
import os
pids = []
try:
    for i in range(10):
        pid = os.fork()
        if pid == 0:
            import time
            time.sleep(10)
            os._exit(0)
        pids.append(pid)
        print(f'Forked {i+1}')
except OSError as e:
    print(f'Fork blocked after: {len(pids)} forks - {e}')
" || echo "Process terminated (expected)"

echo ""
echo "=== All tests complete ==="
