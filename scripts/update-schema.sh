#!/bin/bash

REF=${1:-master}

SCHEMA_DIR=runtime/schema

BASE_URL=https://raw.githubusercontent.com/lambda-feedback/request-response-schemas/$REF

curl -s $BASE_URL/request/eval.json > $SCHEMA_DIR/request-eval.json
curl -s $BASE_URL/request/preview.json > $SCHEMA_DIR/request-preview.json

curl -s $BASE_URL/response/eval.json > $SCHEMA_DIR/response-eval.json
curl -s $BASE_URL/response/preview.json > $SCHEMA_DIR/response-preview.json