# Shimmy-WASM live demo

This repo has a one-command demo for showing Shimmy-WASM end to end:

```bash
scripts/demo-wasm.sh
```

The script builds Shimmy, compiles a tiny Go evaluation function to WASM,
starts `shimmy serve`, sends two HTTP `eval` requests, and verifies the response.

## What it demonstrates

The demo evaluator in `examples/demo-stateful/` intentionally mutates a
module-global counter on every request:

```go
var invocationCount uint32
// each evaluate() call increments invocationCount
```

If warm worker state leaked, two requests to the same evaluator would report:

```text
request 1: guest_invocation_count = 1
request 2: guest_invocation_count = 2
```

With Shimmy-WASM, the host snapshots the WASM module memory after startup and
restores it after every request, so both requests report:

```text
request 1: guest_invocation_count = 1
request 2: guest_invocation_count = 1
```

That is the simple story for supervisors/reviewers: untrusted grading code can
run as a warm evaluator, but mutable guest state does not leak between students.

## Manual equivalent

The one-command script is just this flow:

```bash
# Build Shimmy
go build -trimpath -buildvcs=false -o bin/shimmy-demo .

# Compile a demo evaluator to WASI/WASM
(cd examples/demo-stateful && GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o eval.wasm .)

# Start Shimmy on localhost
FUNCTION_INTERFACE=wasm \
FUNCTION_COMMAND="$PWD/examples/demo-stateful/eval.wasm" \
FUNCTION_MAX_PROCS=1 \
FUNCTION_WORKER_SEND_TIMEOUT=5s \
bin/shimmy-demo serve --host 127.0.0.1 --port 18080
```

Then, from another terminal:

```bash
curl -sS -X POST http://127.0.0.1:18080/ \
  -H 'Content-Type: application/json' \
  -H 'Command: eval' \
  --data '{"response":"42","answer":"42","params":{}}' | python3 -m json.tool
```

Expected response shape:

```json
{
  "command": "eval",
  "result": {
    "is_correct": true,
    "feedback": "Correct — and the guest counter is still 1, so snapshot/restore worked.",
    "guest_invocation_count": 1,
    "snapshot_isolation_ok": true
  }
}
```

## Requirements

- Go 1.24+ with `wasip1/wasm` support
- `curl`
- `python3` for pretty-printing/assertions

The demo does not require Docker, Node.js, Pyodide, AWS credentials, or the large
CPython-WASI runtime. It is intentionally the smallest end-to-end path.
