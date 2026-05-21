BIN_DIR := bin
BINARIES := appmgr-server appmgr-create-app-user appmgr-migrate

GO ?= go
GOFLAGS_BUILD := -trimpath
LDFLAGS := -s -w
export CGO_ENABLED := 0

CONFIG ?= config.yml

.PHONY: all build test lint run clean tidy migrate-up migrate-down generate

all: build

build: $(addprefix $(BIN_DIR)/,$(BINARIES))

$(BIN_DIR)/appmgr-server: $(shell find cmd/server internal db -type f \( -name '*.go' -o -name '*.sql' \)) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS_BUILD) -ldflags '$(LDFLAGS)' -o $@ ./cmd/server

$(BIN_DIR)/appmgr-create-app-user: $(shell find cmd/create-app-user internal -type f -name '*.go') go.mod go.sum
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS_BUILD) -ldflags '$(LDFLAGS)' -o $@ ./cmd/create-app-user

$(BIN_DIR)/appmgr-migrate: $(shell find cmd/migrate internal db -type f \( -name '*.go' -o -name '*.sql' \)) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS_BUILD) -ldflags '$(LDFLAGS)' -o $@ ./cmd/migrate

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

migrate-up: $(BIN_DIR)/appmgr-migrate
	./$(BIN_DIR)/appmgr-migrate --config $(CONFIG) --direction up

migrate-down: $(BIN_DIR)/appmgr-migrate
	./$(BIN_DIR)/appmgr-migrate --config $(CONFIG) --direction down

generate:
	sqlc generate
