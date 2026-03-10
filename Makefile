BINARY_NAME := loramapr-receiverd
BIN_DIR := bin
VERSION ?= dev
GO ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)

.PHONY: build run test fmt tidy release clean

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/loramapr-receiverd

run:
	$(GO) run ./cmd/loramapr-receiverd

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

release:
	packaging/release/build-artifacts.sh $(VERSION)

clean:
	rm -rf $(BIN_DIR)
