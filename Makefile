BINARY_NAME := loramapr-receiverd
BIN_DIR := bin
VERSION ?= dev
CHANNEL ?= stable
GO ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)

.PHONY: build run test test-m5-corpus fmt tidy release clean

build:
	@set -e; build_started=$$(date +%s); \
		echo "[build] preparing $(BIN_DIR)/"; \
		mkdir -p $(BIN_DIR); \
		echo "[build] compiling $(BINARY_NAME)..."; \
		$(GO) build -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/loramapr-receiverd; \
		build_elapsed=$$(( $$(date +%s) - $$build_started )); \
		echo "[build] wrote $(BIN_DIR)/$(BINARY_NAME)"; \
		echo "[build] complete in $${build_elapsed}s"

run:
	$(GO) run ./cmd/loramapr-receiverd

test:
	$(GO) test ./...

test-m5-corpus:
	$(GO) test -count=1 ./internal/contracts/protocolevents/v1 ./internal/meshcore ./internal/outbox ./internal/receiverevents ./internal/cloudclient ./internal/homeautosession ./internal/runtime ./internal/meshtastic

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

release:
	packaging/release/build-artifacts.sh $(VERSION) $(CHANNEL)

clean:
	rm -rf $(BIN_DIR)
