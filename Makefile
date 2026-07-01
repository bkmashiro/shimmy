VERSION ?= local
COMMIT ?= $(shell git rev-parse --short HEAD)
BUILDTIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

GOLDFLAGS += -X main.Version=$(VERSION)
GOLDFLAGS += -X main.Buildtime=$(BUILDTIME)
GOLDFLAGS += -X main.Commit=$(COMMIT)
GOFLAGS = -ldflags "$(GOLDFLAGS)"

BINARY_NAME ?= shimmy
CONTAINER_ENGINE ?= docker

.PHONY: all build test test-unit test-sandbox benchmark-e2e benchmark-e2e-docker benchmark-wasm-e2e lcov install generate-mocks update-schema

all: build

build:
	go build -o ./bin/$(BINARY_NAME) -trimpath -buildvcs=false $(GOFLAGS) .

test: test-unit

test-unit:
	go test -covermode=count -coverprofile=coverage.out ./...

# Run sandbox integration tests inside a privileged container.
# Supports Docker (default) and Podman: CONTAINER_ENGINE=podman make test-sandbox
# On Linux with nsjail installed locally, use:
#   go test -v -run 'TestSandboxedWorker' ./internal/execution/worker/...
test-sandbox:
	$(CONTAINER_ENGINE) build --target test-sandbox -t shimmy-test-sandbox .
	$(CONTAINER_ENGINE) run --rm --privileged \
	  -v $(shell pwd):/workspace \
	  -w /workspace \
	  shimmy-test-sandbox \
	  go test -v -run 'TestSandboxedWorker' ./internal/execution/worker/...

benchmark-e2e:
	scripts/benchmark-e2e.py $(BENCH_ARGS)

benchmark-e2e-docker:
	$(CONTAINER_ENGINE) build --target benchmark-e2e -t shimmy-benchmark-e2e .
	$(CONTAINER_ENGINE) run --rm \
	  -v $(shell pwd):/workspace \
	  -w /workspace \
	  shimmy-benchmark-e2e \
	  make benchmark-e2e BENCH_ARGS="$(BENCH_ARGS)"

benchmark-wasm-e2e:
	scripts/benchmark-wasm-e2e.py $(BENCH_ARGS)
	
lcov:
	gcov2lcov -infile=coverage.out -outfile=lcov.info

install:
	go install

generate-mocks:
	mockery

update-schema:
	scripts/update-schema.sh