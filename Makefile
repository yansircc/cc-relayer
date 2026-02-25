VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build run clean test deps lint ui dev-ui dev-go

build: ui
	go build -ldflags="-s -w -X main.version=$(VERSION)" -o cc-relayer ./cmd/relay

run: build
	./cc-relayer

clean:
	rm -f cc-relayer
	rm -rf internal/ui/dist

test:
	go test ./...

deps:
	go mod tidy

lint:
	go vet ./...

ui:
	cd web && npm install && npm run build

dev-ui:
	cd web && npm run dev

dev-go:
	go run ./cmd/relay
