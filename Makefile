.PHONY: build run clean test

BINARY=relay-service
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

build:
	go build -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY) ./cmd/relay

run: build
	./$(BINARY)

clean:
	rm -f $(BINARY)

test:
	go test ./...

deps:
	go mod tidy

lint:
	go vet ./...
