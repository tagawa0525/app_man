SHELL := /usr/bin/env bash

BIN_DIR := bin
BINARIES := appmgr-server

GO ?= go
GOFLAGS_BUILD := -trimpath
LDFLAGS := -s -w
export CGO_ENABLED := 0

.PHONY: all build test lint run clean tidy

all: build

build: $(addprefix $(BIN_DIR)/,$(BINARIES))

$(BIN_DIR)/appmgr-server: $(shell find cmd/server internal -type f -name '*.go') go.mod go.sum
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS_BUILD) -ldflags '$(LDFLAGS)' -o $@ ./cmd/server

test:
	$(GO) test ./...

lint:
	golangci-lint run

run: build
	./$(BIN_DIR)/appmgr-server --config config.yml

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BIN_DIR)
