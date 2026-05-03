BINARY    := trail
BUILD_DIR := ./tmp
CMD       := .

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

.PHONY: all build install run test test-race test-perf test-e2e test-all lint tidy clean snapshot

all: build

## build: compile the binary into ./tmp/trail
build:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) $(CMD)

## install: build and install to $GOPATH/bin for local development
install:
	CGO_ENABLED=0 go install $(LDFLAGS) $(CMD)

## run: build then print the binary path so you can quickly invoke it
run: build
	@echo "built: $(BUILD_DIR)/$(BINARY)"

## test: run unit tests (skips slow perf + e2e via -short)
test:
	go test -short ./...

## test-race: run unit tests with the race detector
test-race:
	go test -race -short ./...

## test-perf: run the perf benchmarks + budget tests against the store
test-perf:
	go test ./internal/store -count=1 -run "TestRead_Performance|TestRead_ConcurrentWith" -v
	go test ./internal/store -count=3 -bench "BenchmarkRead" -benchmem -run '^$$'

## test-e2e: build the binary, spawn `trail mcp` as subprocess, hammer with 100K-entry load
test-e2e:
	go test ./tests/... -count=1 -v -timeout 120s

## test-all: run unit + perf + e2e
test-all: test test-perf test-e2e

## lint: run go vet
lint:
	go vet ./...

## tidy: tidy and verify go.mod
tidy:
	go mod tidy
	go mod verify

## clean: remove build artifacts
clean:
	rm -rf $(BUILD_DIR)

## snapshot: build binaries for macOS (arm64+amd64) and Linux (arm64+amd64)
snapshot:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 $(CMD)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 $(CMD)
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-arm64  $(CMD)
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-amd64  $(CMD)
