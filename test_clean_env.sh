#!/bin/bash

cd /app/cmd/sandbox-wrapper
mv main.go main.go.orig
cp main_v3.go main.go
go build -o /app/sandbox-wrapper . 2>&1

echo "=== Test 1: Without -clean-env (full env visible) ==="
AWS_SECRET=super-secret /app/sandbox-wrapper -timeout=5s -- python3 -c "
import os
env = dict(os.environ)
secrets = [k for k in env if 'SECRET' in k or 'AWS' in k]
print(f'Secrets visible: {secrets}')
print(f'Total env vars: {len(env)}')
"

echo ""
echo "=== Test 2: With -clean-env (minimal env) ==="
AWS_SECRET=super-secret /app/sandbox-wrapper -timeout=5s -clean-env -- python3 -c "
import os
env = dict(os.environ)
secrets = [k for k in env if 'SECRET' in k or 'AWS' in k]
print(f'Secrets visible: {secrets}')
print(f'Total env vars: {len(env)}')
for k, v in sorted(env.items()):
    print(f'  {k}={v}')
"

echo ""
echo "=== Test 3: With -clean-env -allow-env=PYTHONPATH ==="
AWS_SECRET=super-secret PYTHONPATH=/custom /app/sandbox-wrapper -timeout=5s -clean-env -allow-env=PYTHONPATH -- python3 -c "
import os
env = dict(os.environ)
print(f'PYTHONPATH={os.environ.get(\"PYTHONPATH\", \"not set\")}')
print(f'AWS_SECRET={os.environ.get(\"AWS_SECRET\", \"not set\")}')
"

# Restore
mv main.go.orig main.go
