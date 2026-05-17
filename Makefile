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
# Produces a static linux/amd64 binary + a zip ready for Lambda upload.
.PHONY: probe-build probe-zip probe-deploy

probe-build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
	  go build -ldflags="-s -w" -o bin/lambda-probe ./tools/lambda-probe/

probe-zip: probe-build
	mkdir -p bin
	cp bin/lambda-probe bin/bootstrap
	zip -j bin/lambda-probe.zip bin/bootstrap
	rm bin/bootstrap
	@echo "Ready: bin/lambda-probe.zip"

# One-shot Lambda invoke — requires AWS_LAMBDA_FUNCTION_NAME or LAMBDA_FUNC env var.
# e.g.:  make probe-deploy LAMBDA_FUNC=shimmy-dev
probe-deploy: probe-zip
	@test -n "$(LAMBDA_FUNC)" || (echo "set LAMBDA_FUNC=<name>" && exit 1)
	aws lambda update-function-code \
	  --function-name $(LAMBDA_FUNC) \
	  --zip-file fileb://bin/lambda-probe.zip
	@echo "Invoking..."
	aws lambda invoke \
	  --function-name $(LAMBDA_FUNC) \
	  --payload '{}' \
	  --log-type Tail \
	  --query 'LogResult' \
	  --output text \
	  /dev/null | base64 -d