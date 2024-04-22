#!/bin/bash

# This script fetches the request and response schemas from the
# lambda-feedback/request-response-schemas repository and updates
# the local schema files in runtime/schema directory.

# Usage: ./scripts/update-schema.sh [REF]
# REF: The branch, tag or commit to fetch the schema from. Default is master.

REF=${1:-master}

SCHEMA_DIR=runtime/schema

BASE_URL=https://raw.githubusercontent.com/lambda-feedback/request-response-schemas/$REF

curl -s $BASE_URL/request/eval.json > $SCHEMA_DIR/request-eval.json
curl -s $BASE_URL/request/preview.json > $SCHEMA_DIR/request-preview.json

curl -s $BASE_URL/response/eval.json > $SCHEMA_DIR/response-eval.json
curl -s $BASE_URL/response/preview.json > $SCHEMA_DIR/response-preview.json