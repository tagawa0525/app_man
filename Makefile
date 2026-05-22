BIN_DIR := bin

# 既存 3 バイナリ + フェーズ 1 PR3 で追加した 8 バッチ。
# 計 11 バイナリ。バッチ系は cmd/<name>/ 配下に骨格を持ち、
# 共通起動を internal/clirun に委譲する。
BINARIES := \
	appmgr-server \
	appmgr-create-app-user \
	appmgr-migrate \
	appmgr-sync-directory \
	appmgr-import-skysea \
	appmgr-check-integrity \
	appmgr-notify \
	appmgr-backup \
	appmgr-prune-logs \
	appmgr-generate-meta \
	appmgr-import-bootstrap

GO ?= go
GOFLAGS_BUILD := -trimpath
LDFLAGS := -s -w
export CGO_ENABLED := 0

CONFIG ?= config.yml

.PHONY: all build test lint run clean tidy migrate-up migrate-down generate

all: build

build: $(addprefix $(BIN_DIR)/,$(BINARIES))

$(BIN_DIR)/appmgr-server: $(shell find cmd/server internal db -type f \( -name '*.go' -o -name '*.sql' -o -name '*.templ' -o -name '*.css' -o -name '*.js' \)) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS_BUILD) -ldflags '$(LDFLAGS)' -o $@ ./cmd/server

$(BIN_DIR)/appmgr-create-app-user: $(shell find cmd/create-app-user internal -type f -name '*.go') go.mod go.sum
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS_BUILD) -ldflags '$(LDFLAGS)' -o $@ ./cmd/create-app-user

$(BIN_DIR)/appmgr-migrate: $(shell find cmd/migrate internal db -type f \( -name '*.go' -o -name '*.sql' \)) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS_BUILD) -ldflags '$(LDFLAGS)' -o $@ ./cmd/migrate

# PR3 で追加した 8 バッチ用の汎用パターンルール。
# 上記 3 つの明示ルールが優先されるため、appmgr-server / -migrate /
# -create-app-user には適用されない。$* がパターン部分 (例: backup) に展開される。
# 依存は cmd / internal の全 Go ファイル（過剰だが、骨格バイナリの
# 再ビルドコストは小さいため許容）。
$(BIN_DIR)/appmgr-%: $(shell find cmd internal -type f -name '*.go') go.mod go.sum
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS_BUILD) -ldflags '$(LDFLAGS)' -o $@ ./cmd/$*

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

# sqlc / templ ともに生成物をコミットする運用 (CLAUDE.md「sqlc 生成物の扱い」参照)。
# CI では走らせず、スキーマ・テンプレ変更時にローカルで再生成してから commit する。
generate:
	sqlc generate
	templ generate
