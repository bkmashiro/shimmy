VERSION ?= local
COMMIT ?= $(shell git rev-parse --short HEAD)
BUILDTIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

GOLDFLAGS += -X main.Version=$(VERSION)
GOLDFLAGS += -X main.Buildtime=$(BUILDTIME)
GOLDFLAGS += -X main.Commit=$(COMMIT)
GOFLAGS = -ldflags "$(GOLDFLAGS)"

BINARY_NAME ?= shimmy

.PHONY: all build test test-unit lcov install generate-mocks update-schema

all: build

build:
	go build -o ./bin/$(BINARY_NAME) -trimpath -buildvcs=false $(GOFLAGS) .

test: test-unit

test-unit:
	go test -covermode=count -coverprofile=coverage.out ./...
	
lcov:
	gcov2lcov -infile=coverage.out -outfile=lcov.info

install:
	go install

generate-mocks:
	mockery

update-schema:
	scripts/update-schema.sh

# Lambda kernel-feature probe
# Produces a static linux/amd64 binary + zips ready for Lambda upload.
.PHONY: probe-build probe-zip probe-zip-py probe-deploy probe-deploy-py

probe-build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
	  go build -ldflags="-s -w" -o bin/lambda-probe ./tools/lambda-probe/

# provided.al2023 runtime (bootstrap binary)
probe-zip: probe-build
	mkdir -p bin
	cp bin/lambda-probe bin/bootstrap
	zip -j bin/lambda-probe.zip bin/bootstrap
	rm bin/bootstrap
	@echo "Ready: bin/lambda-probe.zip (provided.al2023)"

# Python runtime: handler.py + lambda-probe binary
probe-zip-py: probe-build
	mkdir -p bin
	zip -j bin/lambda-probe-py.zip \
	  tools/lambda-probe/handler.py \
	  bin/lambda-probe
	@echo "Ready: bin/lambda-probe-py.zip (python3.x + handler.lambda_handler)"

# Deploy to provided.al2023 function
# e.g.:  make probe-deploy LAMBDA_FUNC=my-func
probe-deploy: probe-zip
	@test -n "$(LAMBDA_FUNC)" || (echo "set LAMBDA_FUNC=<name>" && exit 1)
	aws lambda update-function-code \
	  --function-name $(LAMBDA_FUNC) \
	  --region eu-west-2 \
	  --zip-file fileb://bin/lambda-probe.zip
	@echo "Invoking..."
	aws lambda invoke \
	  --function-name $(LAMBDA_FUNC) \
	  --region eu-west-2 \
	  --payload '{}' \
	  --log-type Tail \
	  --query 'LogResult' \
	  --output text \
	  /dev/null | base64 -d

# Deploy to Python runtime function (shimmy-probe-py)
# e.g.:  make probe-deploy-py   (uses shimmy-probe-py by default)
PROBE_PY_FUNC ?= shimmy-probe-py
probe-deploy-py: probe-zip-py
	aws lambda update-function-code \
	  --function-name $(PROBE_PY_FUNC) \
	  --region eu-west-2 \
	  --zip-file fileb://bin/lambda-probe-py.zip
	aws lambda update-function-configuration \
	  --function-name $(PROBE_PY_FUNC) \
	  --region eu-west-2 \
	  --handler handler.lambda_handler \
	  --timeout 15 2>/dev/null || true
	@echo "Invoking $(PROBE_PY_FUNC)..."
	aws lambda invoke \
	  --function-name $(PROBE_PY_FUNC) \
	  --region eu-west-2 \
	  --payload '{}' \
	  --log-type Tail \
	  --query 'LogResult' \
	  --output text \
	  /dev/null | base64 -d