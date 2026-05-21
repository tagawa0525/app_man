# フェーズ 1「基盤整備」実装プラン

## Context

`docs/specs/02_要件定義.md` § 12 で定めた実装フェーズの **1. 基盤整備** を着手する段階。リポジトリは Nix flake で開発環境（go, sqlc, go-migrate, templ, golangci-lint, air, sqlite）が整備済みだが、`go.mod` 未初期化・`cmd/` / `internal/` 等のディレクトリ未作成・コード一切なしの状態。

このプランで導入する基盤の上に、フェーズ 2 以降（マスタ系・認証・AD 連携・SKYSEA 取込み等）の実装を積み上げる。よって本フェーズの成果物は「機能としては何もしないが、設定読込・ロギング・DB 接続・マイグレーション・CLI 排他制御が共通基盤として動く」状態を目指す。

## Decisions（確定値）

| 項目 | 決定 |
|---|---|
| Go module 名 | `github.com/tagawa0525/app_man` |
| Go バージョン | `1.22` |
| Web フレームワーク | `chi`（薄め・net/http 互換・templ との接続が直接） |
| Web FW 導入タイミング | PR2 以降（PR1 は `net/http.ServeMux`） |
| マイグレーション粒度 | 論理単位 6 ファイル分割（マスタ／ライセンス／インストール／承認／アプリ系／インデックス・ビュー） |
| 起動時自動マイグレーション | しない（README に `make migrate-up` を明示。`appmgr-server` は版数チェックのみ） |
| CI（GitHub Actions） | PR3 で導入（最初の 2 PR ではローカル `make test`／`make lint` で確認） |
| CGO | `CGO_ENABLED=0` を Makefile・CI で固定 |
| Web FW 抽象化 | しない。Chi の `chi.Router` をそのまま使う（早すぎる抽象化の回避） |
| ログローテーション | PR1 は未実装。PR2 で外部依存（`lumberjack.v2`）導入可否を判断 |

## PR 分割（3 本構成）

### PR1: プロジェクト初期化 ＋ 設定読込 ＋ ロギング ＋ /healthz

**「動くもの」**: `make build && ./bin/appmgr-server --config config.yml` で起動し `curl localhost:8180/healthz` が `ok` を返す。

**範囲**:

- `go mod init` と Makefile・README 雛形
- `internal/config`: YAML パース、`*_env` キーの環境変数展開、必須キーバリデーション
- `internal/applog`: `log/slog` ベースのロガー初期化（JSON 出力、`binary`・`pid` 属性）
- `cmd/server/main.go`: config 読込 → logger 初期化 → `net/http.ServeMux` で `/healthz` → グレースフルシャットダウン
- `config.example.yml`: 基盤に必要な最小キーのみ（`server` / `database` / `locks` / `logging`）
- `.golangci.yml`: フェーズ 1 でリント基準を決めておく（PR3 の CI で活用）

**受け入れ**:

- `go build ./...` が通る
- `appmgr-server` が config を読み listen、`/healthz` が 200 / `ok`
- ログが `logs/appmgr-server.log` に JSON で出力、`binary=appmgr-server` を含む
- SIGTERM でグレースフル終了、shutdown ログ記録
- `go test ./...` 緑（`internal/config`・`internal/applog` のテーブルテスト）

### PR2: DB 接続 ＋ マイグレーション ＋ `appmgr-create-app-user` 雛形

**「動くもの」**: `make migrate-up` で 6 ファイルのマイグレーションが順に適用され、`appmgr-server` が DB を開いて WAL モードで接続できる。`appmgr-create-app-user --help` が表示される。

**範囲**:

- `internal/db`: `modernc.org/sqlite` で接続、`PRAGMA journal_mode=WAL` / `PRAGMA foreign_keys=ON` 適用、close 処理
- `db/migrations/` に 6 ファイル（後述）の up/down を配置
- `sqlc.yaml` 配置と最小クエリ 1〜2 件（`appmgr-create-app-user` 用の `GetAppUserByUsername`、`CreateAppUser`）
- `cmd/create-app-user/main.go`: CLI 引数パース骨格（`--username`・`--role`・`--reset-password`・`--notify-email` をフラグ定義のみ。実処理は次フェーズで実装）
- `cmd/server/main.go` を Chi に置き換え（このタイミングで Chi 採用）
- `appmgr-server` 起動時の DB 版数チェック：必須版に達していなければ exit 1
- Makefile に `migrate-up` / `migrate-down` / `generate`（sqlc）ターゲット追加

**マイグレーション 6 ファイルの分割案**:

| ファイル | 含むテーブル |
|---|---|
| `000001_master.up.sql` | `vendors`、`products`、`product_aliases`、`departments`、`users`、`devices` |
| `000002_licenses.up.sql` | `licenses`、`license_documents`、`user_assignments`、`device_assignments` |
| `000003_inventory.up.sql` | `installations`、`raw_installations`、`import_logs` |
| `000004_approvals.up.sql` | `department_product_approvals`、`approval_scope_users`、`approval_scope_devices`、`approval_requests`、`product_version_advisories` |
| `000005_app.up.sql` | `app_users`、`user_department_roles`、`audit_logs`、`inventory_audits`、`app_settings`、`notifications` |
| `000006_indexes_views.up.sql` | 全インデックスと `v_license_usage` ビュー |

各 up に対応する down を逆順 DROP で作成。

**受け入れ**:

- `migrate up` と `migrate down` の両方が完走する
- `sqlc generate` で `internal/repository/*.sql.go` が生成され、コミット済み
- `appmgr-server` が起動時に DB をオープン・PRAGMA 設定し、終了時に close する
- `appmgr-create-app-user --help` が usage を表示する

### PR3: lock ファイル基盤 ＋ 全 CLI スケルトン ＋ CI

**「動くもの」**: 全 10 バイナリが `make build` で生成され、`appmgr-import-skysea` を 2 つ同時起動すると 2 つ目が exit 2 で落ちる。`appmgr-import-skysea` 実行中の `appmgr-backup` 起動も exit 2。

**範囲**:

- `internal/lockfile`: `lockfile_unix.go` / `lockfile_windows.go` の 2 ファイル構成
  - `Acquire(name string, mode Mode) (*Lock, error)`、`Release()` を公開
  - `ModeShared`（通常）と `ModeGlobal`（backup 専用、他全バッチ lock を取得）
  - PID 書込・stale 検出（`os.FindProcess` + `Signal(0)`）・JSON フォーマット
- `internal/clirun`: 各 CLI バイナリの共通起動ヘルパー
  - フラグ（`--config`、`--dry-run`）パース → config → logger → lock 取得 → shutdown context → release
  - 失敗時の exit code 規約（取得失敗 = 2、設定エラー = 3、実行エラー = 1）
- `cmd/sync-directory/`、`cmd/import-skysea/`、`cmd/check-integrity/`、`cmd/notify/`、`cmd/backup/`、`cmd/prune-logs/`、`cmd/generate-meta/`、`cmd/import-bootstrap/` の 8 種に main.go 雛形を作成（実処理は空、lock を取って解放するだけ）
- `appmgr-backup` のみ `lockfile.ModeGlobal` で起動
- `appmgr-server` 用の lock は `cmd/server/main.go` 側で別管理（多重起動防止のみ、グローバルロック対象外）
- `.github/workflows/ci.yml`: `go test ./...`・`golangci-lint run`・`go build ./cmd/...` を実行

**受け入れ**:

- 受け入れ基準 11.2-5「`cmd/` 配下が機能別ディレクトリに分かれ、各々が独立してビルドできる」
- 受け入れ基準 18「`appmgr-import-skysea` 実行中に `appmgr-backup` を起動するとロック取得失敗で exit 2」の lock 部分
- CI がデフォルトブランチに対して緑

## Critical Files（最初に確定する 5 ファイル）

PR1 で確定し、以降の PR でも継続して参照される基幹ファイル：

- `/home/tagawa/github/app_man/go.mod`
- `/home/tagawa/github/app_man/cmd/server/main.go`
- `/home/tagawa/github/app_man/internal/config/config.go`
- `/home/tagawa/github/app_man/internal/applog/logger.go`
- `/home/tagawa/github/app_man/config.example.yml`

PR2 で追加される基幹ファイル：

- `/home/tagawa/github/app_man/db/migrations/000001_master.up.sql` ほか 6 ファイル
- `/home/tagawa/github/app_man/internal/db/sqlite.go`
- `/home/tagawa/github/app_man/sqlc.yaml`
- `/home/tagawa/github/app_man/cmd/create-app-user/main.go`

PR3 で追加される基幹ファイル：

- `/home/tagawa/github/app_man/internal/lockfile/lockfile_unix.go`
- `/home/tagawa/github/app_man/internal/lockfile/lockfile_windows.go`
- `/home/tagawa/github/app_man/internal/clirun/run.go`
- `/home/tagawa/github/app_man/.github/workflows/ci.yml`

## TDD コミット履歴の例（PR1）

ブランチ `feat/phase1-pr1-bootstrap` で以下のサイクルを履歴に残す：

1. `chore: go module を初期化`
2. `test(config): YAML パースの RED テストを追加`
3. `feat(config): Config struct と Load を実装`
4. `test(config): *_env キーの環境変数展開の RED テストを追加`
5. `feat(config): 環境変数展開を実装`
6. `test(applog): slog 初期化の RED テストを追加`
7. `feat(applog): slog ロガー初期化を実装`
8. `feat(server): /healthz とグレースフルシャットダウンを実装`
9. `docs: README に build/run 手順、Makefile に target 追加`

RED → GREEN の対が `config` と `applog` で 2 回履歴に残ること、`feat(server)` 直前までユニットテストが先行することがレビュー時のチェックポイント。

## config.yml 最小スキーマ（PR1 用）

```yaml
server:
  listen: 0.0.0.0:8180
  base_url: http://localhost:8180

database:
  path: ./data/app.db
  wal: true                 # PR2 で参照

locks:
  base_dir: ./data/locks    # PR3 で参照

logging:
  level: info               # debug/info/warn/error
  base_dir: ./logs
  format: json              # json/text
```

要件書 § 10 の `file_store`・`import`・`directory`・`auth`・`notifier`・`backup` は **PR1 では含めない**。`Config` struct にも将来用の空フィールドを置かない（早すぎる抽象化を避ける）。

## 設計上の注意

- `internal/service`・`internal/domain`・`internal/handler/api` などの空ディレクトリは作らない。必要な PR で初出ファイルと同時に作成する
- `internal/clock`・`internal/idgen` などの抽象は入れない。`time.Now()` を直接使い、3 回重複してから抽象化を検討
- sqlc 生成物（`*.sql.go`）はコミットする（`.gitignore` でも除外していない）
- Web FW 抽象は導入しない。`chi.Router` をそのまま `cmd/server` で組み立てる
- `internal/repository` の手書きラッパは作らない。sqlc の生成 `Querier` をそのまま使う

## Verification（フェーズ 1 完了確認）

PR3 マージ後に main ブランチで以下を実行し、すべて成功することを確認する：

```bash
nix develop
make build                              # bin/ に 10 バイナリが生成される
ls bin/ | wc -l                         # 10
make test                               # 全テスト緑
make lint                               # golangci-lint 緑

# 設定とマイグレーション
cp config.example.yml config.yml
make migrate-up                         # 6 マイグレーションが順に適用
sqlite3 ./data/app.db ".tables"         # 全テーブル＋ビューが存在

# 起動と /healthz
./bin/appmgr-server --config config.yml &
SERVER_PID=$!
curl -sf http://localhost:8180/healthz  # "ok"
kill $SERVER_PID                        # ログに shutdown 記録

# CLI lock 衝突
./bin/appmgr-import-skysea --config config.yml &
sleep 1
./bin/appmgr-import-skysea --config config.yml ; echo "exit=$?"  # exit=2
./bin/appmgr-backup        --config config.yml ; echo "exit=$?"  # exit=2
wait

# down マイグレーション
make migrate-down                       # 全テーブル DROP まで完走
```

この時点で要件書受け入れ基準 11.2-2、11.2-4、11.2-5、11.3-1、18（lock 部分）が部分的に充足され、フェーズ 2「マスタ系」に着手可能な状態となる。
