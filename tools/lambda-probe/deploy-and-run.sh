#!/usr/bin/env bash
# One-shot: build → package → deploy → invoke → print results
# Usage: ./deploy-and-run.sh [RUNTIME] [REGION] [OUTPUT_JSON]
#   RUNTIME      lambda python runtime, e.g. python3.11 (AL2) or python3.12 (AL2023)
#   REGION       AWS region, default eu-west-2
#   OUTPUT_JSON  result path, default tools/lambda-probe/last-results.json
#
# Requires: aws cli, go (cross-compile)
set -euo pipefail

RUNTIME="${1:-python3.12}"
REGION="${2:-eu-west-2}"
OUTPUT_JSON="${3:-}"
FUNCTION_NAME="shimmy-lambda-probe"
ROLE_ARN="${LAMBDA_PROBE_ROLE_ARN:-}"   # set this env var, or edit below
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PROBE_DIR="$REPO_ROOT/tools/lambda-probe"
BUILD_DIR="$(mktemp -d)"
trap "rm -rf $BUILD_DIR" EXIT

echo "=== Building lambda-probe (linux/amd64, static) ==="
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -ldflags="-s -w" \
  -o "$BUILD_DIR/lambda-probe" \
  "$REPO_ROOT/tools/lambda-probe"

echo "=== Packaging ==="
cp "$PROBE_DIR/handler.py" "$BUILD_DIR/handler.py"
(cd "$BUILD_DIR" && zip -q probe.zip lambda-probe handler.py)

echo "=== Deploying to Lambda ($FUNCTION_NAME, $RUNTIME, $REGION) ==="
if aws lambda get-function --function-name "$FUNCTION_NAME" --region "$REGION" &>/dev/null; then
  aws lambda update-function-code \
    --function-name "$FUNCTION_NAME" \
    --region "$REGION" \
    --zip-file "fileb://$BUILD_DIR/probe.zip" \
    --output text --query 'FunctionArn' | cat

  aws lambda wait function-updated --function-name "$FUNCTION_NAME" --region "$REGION" 2>/dev/null || sleep 5

  # Update runtime in case it changed
  aws lambda update-function-configuration \
    --function-name "$FUNCTION_NAME" \
    --region "$REGION" \
    --runtime "$RUNTIME" \
    --output text --query 'FunctionArn' | cat
else
  if [[ -z "$ROLE_ARN" ]]; then
    echo "ERROR: Function does not exist and LAMBDA_PROBE_ROLE_ARN is not set."
    echo "Either create the function manually or export LAMBDA_PROBE_ROLE_ARN=arn:aws:iam::..."
    exit 1
  fi
  aws lambda create-function \
    --function-name "$FUNCTION_NAME" \
    --region "$REGION" \
    --runtime "$RUNTIME" \
    --handler "handler.lambda_handler" \
    --role "$ROLE_ARN" \
    --zip-file "fileb://$BUILD_DIR/probe.zip" \
    --timeout 30 \
    --memory-size 256 \
    --output text --query 'FunctionArn' | cat
fi

echo "=== Waiting for function to be active ==="
aws lambda wait function-updated --function-name "$FUNCTION_NAME" --region "$REGION" 2>/dev/null || true
sleep 2

echo "=== Invoking ==="
RESP_FILE="${OUTPUT_JSON:-$PROBE_DIR/last-results.json}"
mkdir -p "$(dirname "$RESP_FILE")"
aws lambda invoke \
  --function-name "$FUNCTION_NAME" \
  --region "$REGION" \
  --payload '{}' \
  "$RESP_FILE" >/dev/null

echo ""
echo "=== Results ==="
python3 - "$RESP_FILE" <<'EOF'
import json, sys

with open(sys.argv[1]) as f:
    r = json.load(f)

print(f"Python runtime : {r.get('python_version','?')}  |  {r.get('platform','?')}")
print()

probes = r.get("probes", [])
width = max((len(p["probe"]) for p in probes), default=20)
for p in probes:
    icon = "✅" if p["ok"] else "❌"
    warn = f"  ⚠  {p['warn']}" if p.get("warn") else ""
    print(f"  {icon}  {p['probe']:<{width}}  {p.get('detail','')}{warn}")

print()
s = r.get("summary", {})
print(f"Summary: passed={s.get('passed',0)} failed={s.get('failed',0)} overall={'OK' if s.get('ok') else 'FAIL'}")

if r.get("stderr"):
    print("\nstderr:", r["stderr"][:500])
EOF
