BIN_DIR := bin
BINARIES := appmgr-server appmgr-create-app-user

GO ?= go
GOFLAGS_BUILD := -trimpath
LDFLAGS := -s -w
export CGO_ENABLED := 0

DB_PATH ?= ./data/app.db
DB_URL := sqlite://$(DB_PATH)
MIGRATIONS_DIR := db/migrations

.PHONY: all build test lint run clean tidy migrate-up migrate-down generate

all: build

build: $(addprefix $(BIN_DIR)/,$(BINARIES))

$(BIN_DIR)/appmgr-server: $(shell find cmd/server internal db -type f \( -name '*.go' -o -name '*.sql' \)) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS_BUILD) -ldflags '$(LDFLAGS)' -o $@ ./cmd/server

$(BIN_DIR)/appmgr-create-app-user: $(shell find cmd/create-app-user internal -type f -name '*.go') go.mod go.sum
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS_BUILD) -ldflags '$(LDFLAGS)' -o $@ ./cmd/create-app-user

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

migrate-up:
	migrate -path $(MIGRATIONS_DIR) -database "$(DB_URL)" up

migrate-down:
	migrate -path $(MIGRATIONS_DIR) -database "$(DB_URL)" down -all

generate:
	sqlc generate
