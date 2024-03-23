VERSION ?= local
COMMIT ?= $(shell git rev-parse --short HEAD)
BUILDTIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

GOLDFLAGS += -X main.Version=$(VERSION)
GOLDFLAGS += -X main.Buildtime=$(BUILDTIME)
GOLDFLAGS += -X main.Commit=$(COMMIT)
GOFLAGS = -ldflags "$(GOLDFLAGS)"

.PHONY: all build clean test test-unit test-race test-msan staticcheck vet

all: build

build:
	go build -o ./bin/shimmy $(GOFLAGS) .

test: test-unit

test-unit:
	go test -covermode=count -coverprofile=coverage.out ./...
	
lcov:
	gcov2lcov -infile=coverage.out -outfile=lcov.info

install:
	go install

serve:
	go run main.go serve
