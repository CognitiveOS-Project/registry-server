SHELL := /bin/sh
.SHELLFLAGS := -eu -o pipefail -c
.ONESHELL:
.DELETE_ON_ERROR:

BUILD_DIR := build
BIN_DIR := $(BUILD_DIR)/bin
GO := go

.PHONY: build test lint clean

build: $(BIN_DIR)/registry-server

$(BIN_DIR)/registry-server:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -o $@ ./cmd/registry-server
	@echo "  -> $@"

test:
	$(GO) test ./... -v -count=1

lint:
	$(GO) vet ./...

clean:
	rm -rf $(BUILD_DIR)
