MODULE := github.com/michaelvillegas/myrman
BIN := myrman
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(MODULE)/internal/version.Version=$(VERSION) -X $(MODULE)/internal/version.Commit=$(COMMIT) -X $(MODULE)/internal/version.Date=$(DATE)

.PHONY: build test tidy vet run clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/myrman

test:
	go test ./...

tidy:
	go mod tidy

vet:
	go vet ./...

run: build
	./$(BIN)

clean:
	rm -f $(BIN)
