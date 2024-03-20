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

staticcheck:
	staticcheck ./...

vet:
	go vet ./...

test: test-unit test-race

test-snap:
	UPDATE_SNAPS=true go test -covermode=count -coverprofile=coverage.out ./...

test-unit:
	go test -covermode=count -coverprofile=coverage.out ./...

test-race:
	go test -race ./...

test-msan:
	go test -msan ./...

install:
	go install

serve:
	go run main.go serve
