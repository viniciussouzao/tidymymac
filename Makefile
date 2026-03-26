BINARY_NAME=tidymymac
BINARY_PATH=bin/$(BINARY_NAME)
CMD_DIR=./cmd/tidymymac

VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS = -ldflags "\
	-X 'github.com/viniciussouzao/tidymymac/internal/buildinfo.Version=$(VERSION)' \
	-X 'github.com/viniciussouzao/tidymymac/internal/buildinfo.Commit=$(COMMIT)' \
	-X 'github.com/viniciussouzao/tidymymac/internal/buildinfo.BuildDate=$(BUILD_DATE)' \
	-s -w"

.PHONY: build test run clean

build:
	@mkdir -p bin
	go build $(LDFLAGS) -o $(BINARY_PATH) $(CMD_DIR)

test:
	go test ./internal/cleaner -v -race -short ./...

run: build
	./$(BINARY_PATH)

clean:
	rm -rf bin	

version:
	@echo "Version: $(VERSION)"
	@echo "Commit: $(COMMIT)"
	@echo "Build Date: $(BUILD_DATE)"