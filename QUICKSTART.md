# Shimmy Sandbox Quick Start

## Build

```bash
cd cmd/sandbox-wrapper
GOOS=linux GOARCH=amd64 go build -o sandbox-wrapper .
```

## Basic Usage

```bash
# Memory + CPU limits
./sandbox-wrapper -cpu 5 -mem 256 -- python3 script.py

# Add network blocking
./sandbox-wrapper -cpu 5 -mem 256 --no-network -- python3 script.py

# Full security (recommended)
./sandbox-wrapper \
    -cpu 5 \
    -mem 256 \
    -fsize 10 \
    -nofile 100 \
    --no-network \
    -clean-env \
    -- python3 script.py
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-cpu` | 5 | CPU time limit (seconds) |
| `-mem` | 256 | Memory limit (MB) |
| `-fsize` | 10 | Max file size (MB) |
| `-nofile` | 100 | Max open files |
| `-nproc` | 10 | Max processes |
| `-timeout` | 30s | Overall timeout |
| `--no-network` | false | Block network syscalls |
| `-clean-env` | false | Clear environment variables |
| `-allow-env` | "" | Comma-separated env vars to keep |

## Integration

```go
import "github.com/lambda-feedback/shimmy/internal/sandbox"

cfg := sandbox.DefaultConfig()
cfg.MaxMemoryMB = 128
cfg.AllowNetwork = false

cmd := sandbox.WrapCommandContext(ctx, "python3", []string{"script.py"}, cfg)
```

## Lambda Deployment

1. Build wrapper for Linux/amd64
2. Include in deployment package
3. Set `SHIMMY_SANDBOX=1`

## Testing

```bash
# Run security tests
python3 sandbox_tests.py

# Run edge case tests
python3 edge_cases.py

# Run benchmarks
python3 benchmark.py
```
