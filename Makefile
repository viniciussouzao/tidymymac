BINARY_NAME=tidymymac
BINARY_PATH=bin/$(BINARY_NAME)
CMD_DIR=./cmd/tidymymac

.PHONY: build test run clean

build:
	@mkdir -p bin
	go build -ldflags="-s -w" -o $(BINARY_PATH) $(CMD_DIR)

test:
	go test ./internal/cleaner -v -race -short ./...

run: build
	./$(BINARY_PATH)

clean:
	rm -rf bin	