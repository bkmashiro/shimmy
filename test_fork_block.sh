#!/bin/bash

cd /app/cmd/sandbox-wrapper
cp main_v2.go main.go
go build -o /app/sandbox-wrapper . 2>&1

echo "=== Testing --no-fork option ==="
echo ""
echo "Test 1: Fork without --no-fork (should work)"
/app/sandbox-wrapper -timeout=5s -- python3 -c "
import os
try:
    pid = os.fork()
    if pid == 0:
        print('child: ok')
        os._exit(0)
    else:
        os.wait()
        print('parent: fork worked')
except Exception as e:
    print(f'blocked: {e}')
"

echo ""
echo "Test 2: Fork WITH --no-fork (should block)"
/app/sandbox-wrapper -timeout=5s --no-fork -- python3 -c "
import os
try:
    pid = os.fork()
    if pid == 0:
        print('child: should not see this')
        os._exit(0)
    else:
        os.wait()
        print('FAIL: fork worked')
except Exception as e:
    print(f'blocked: {e}')
"

echo ""
echo "Test 3: Subprocess WITH --no-fork (should block)"
/app/sandbox-wrapper -timeout=5s --no-fork -- python3 -c "
import subprocess
try:
    result = subprocess.run(['echo', 'hello'], capture_output=True)
    print(f'FAIL: subprocess worked: {result.stdout}')
except Exception as e:
    print(f'blocked: {e}')
"
