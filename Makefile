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