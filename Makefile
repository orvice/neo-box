.PHONY: build neobox buf test lint front

BUILD_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
SERVER_LDFLAGS := -X main.serverCommit=$(BUILD_COMMIT)

build: neobox

neobox:
	go build -ldflags "$(SERVER_LDFLAGS)" -o bin/neobox ./cmd/neobox

buf:
	buf generate
	protoc-go-inject-tag -input="pkg/proto/neobox/v1/*.pb.go"

test:
	go test ./...

lint:
	buf lint
	golangci-lint run --config=.golangci.yml

front:
	cd front && npm run build
