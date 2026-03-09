#!/bin/bash
set -e

echo "Building sandbox-wrapper..."
cd /app/cmd/sandbox-wrapper
go mod tidy 2>/dev/null
go build -o /app/sandbox-wrapper . 2>&1

echo "Running integration test..."
cd /app
go run test_integration.go
