BINARY_NAME := loramapr-receiverd
BIN_DIR := bin

.PHONY: build run test fmt tidy clean

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/loramapr-receiverd

run:
	go run ./cmd/loramapr-receiverd

test:
	go test ./...

fmt:
	go fmt ./...

tidy:
	go mod tidy

clean:
	rm -rf $(BIN_DIR)
