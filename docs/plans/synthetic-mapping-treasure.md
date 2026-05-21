# フェーズ 1 PR2 — DB 基盤と Chi 置換

## Context

PR1 で `internal/config` と `internal/applog` を持つ骨組みサーバが立ち、PR3 で CLAUDE.md と表現規律が整った。次に必要なのはアプリ機能を載せる土台、すなわち **DB レイヤ**。

具体的には以下を一括で投入する：

- `modernc.org/sqlite` での接続確立と `internal/db` パッケージ
- 要件書 § 4 で確定済みの 24 テーブル DDL の SQL 化（6 マイグレーションに分割）
- `golang-migrate/migrate/v4`（pure-Go SQLite ドライバ）での up/down 管理と、起動時の版数チェック
- sqlc を用いた最小クエリ生成パイプラインの稼働（生成物 `internal/repository/*.sql.go` をコミット）
- `appmgr-server` ルータを `chi` に置換（middleware を将来追加する前提の上物）
- `appmgr-create-app-user` のフラグ骨格と Makefile 拡張

PR2 は機能を増やさず「動く DB レイヤと上物の準備」に専念する。Lock ファイル基盤・残りの CLI スケルトン・CI は PR3 へ送る。

## Approach

ブランチ `feat/phase1-pr2-db-foundation` を切り、Plan ファイル先行ルールに従う。TDD サイクルが必要な `internal/db` のテストは RED → GREEN を別コミットで残す。

想定コミット列（12〜14 個）：

1. `docs(plans): フェーズ 1 PR2 DB 基盤の実装プラン` — Plan ファイル先行
2. `feat(migrations): 6 ファイルの up/down SQL を追加` — 24 テーブル DDL + インデックス + `v_license_usage` ビュー
3. `test(db): Open が WAL モードと foreign_keys ON で接続できる` — RED
   - `TestOpen_setsWALAndForeignKeys` で `PRAGMA journal_mode` / `PRAGMA foreign_keys` を `SELECT` し直して検証
4. `feat(db): modernc/sqlite で Open / Close と PRAGMA 適用` — GREEN（このコミットで modernc/sqlite を `go get`）
5. `test(db): MigrateUp / MigrateDown が embed.FS から全テーブル作成・破棄できる` — RED
   - `TestMigrate_upDownRoundTrip` で `t.TempDir()` 上に SQLite を作って Up → 24 テーブル＋ビュー検証 → Down → テーブル数 0 検証
6. `feat(db): embed.FS と golang-migrate/iofs でマイグレーションランナを実装` — GREEN（このコミットで golang-migrate を `go get`）
7. `test(db): CheckVersion が必要版未満で error、一致時に nil を返す` — RED
   - `TestCheckVersion_failsOnStaleSchema` で version 5 のとき error、6 で nil
8. `feat(db): CheckVersion と RequiredMigrationVersion (= 6) を実装` — GREEN
9. `feat(sqlc): sqlc.yaml と最小クエリ、生成物 internal/repository/* をコミット`
10. `feat(server): ルータを chi に置換し RequestID / Recoverer middleware を追加`（このコミットで go-chi/chi を `go get`）
11. `feat(server): 起動時に db.Open → db.CheckVersion、不一致なら exit 1`
12. `feat(create-app-user): フラグ定義のみの骨格バイナリを追加`
13. `chore(make): migrate-up / migrate-down / generate ターゲットを追加`
14. `docs(readme): make migrate-up 手順と必須スキーマ版を追記`

### 主要決定

| 項目 | 決定 | 根拠 |
|---|---|---|
| マイグ粒度 | 6 ファイル分割（master / licenses / inventory / approvals / app / indexes_views）に `000001`〜`000006` で連番 | rustling-discovering-beaver.md 確定済。FK 依存順を連番で明示し、論理単位で巻き戻し可能に。 |
| migrate ライブラリ | `github.com/golang-migrate/migrate/v4` + **pure-Go** SQLite ドライバ（`database/sqlite`、`sqlite3` ではない方）+ `source/iofs` | CGO_ENABLED=0 維持。`modernc.org/sqlite` と同じ実装系の `database/sqlite` を選ぶ。 |
| マイグの参照方式 | サーバ側は `embed.FS` に同梱、CLI（`make migrate-up/down`）は外部ファイル直読み | DDL の 2 重管理を避けつつ、開発時の編集サイクルを切らない。`db/migrations/embed.go` で `//go:embed *.sql` した `embed.FS` を `package migrations` が export し、`internal/db` が `iofs.New(fs, ".")` で読む。 |
| sqlc.yaml | `version: "2"`、`engine: "sqlite"`、`emit_interface: false`、`emit_pointers_for_null_types: true`、`emit_json_tags: false` | CLAUDE.md「早すぎる抽象化禁止」のため `Querier` interface は出さない（必要になってから有効化）。NULL 日時カラム規約のためポインタ emit は必要。 |
| 起動時版数チェックの場所 | `internal/db.CheckVersion(db, embedFS) error` と `RequiredMigrationVersion() uint = 6` を置き、`cmd/server/main.go` が `db.Open` 直後に呼ぶ | DDL の知識を cmd/server に持たせない。 |
| Chi の middleware | `chi.Router` 採用、middleware は `RequestID`、`Recoverer` の最小 2 つのみ | ロガー middleware は applog と組合せる別 PR の専用設計に回す（予防抽象を避ける）。 |
| `cmd/create-app-user` | フラグ定義 + `fmt.Println("not implemented")` で exit 0 | 実処理は次フェーズ。help 表示と exit code 経路だけ通す。 |
| `internal/repository` | sqlc 生成物のみ。手書きラッパは作らない | CLAUDE.md 規約。 |
| go.mod の依存追加方針 | 各実装コミット内で `go get` を実行し、import と require を同コミットにまとめる | `go mod tidy` が未使用 require を削除する性質上、依存追加だけを単独コミットにすると次のコミットで消える。`modernc.org/sqlite` は GREEN コミット(4)、`golang-migrate/migrate/v4` + `database/sqlite` + `source/iofs` は GREEN コミット(6)、`github.com/go-chi/chi/v5` は chi 置換コミット(10) に含める。sqlc バイナリは Nix shell 経由なので go.mod には不要。 |

## Critical Files

**新規**：

- `docs/plans/synthetic-mapping-treasure.md` — 本 Plan
- `db/migrations/000001_master.{up,down}.sql` — departments, vendors, products, product_aliases, users, devices
- `db/migrations/000002_licenses.{up,down}.sql` — licenses, license_documents, user_assignments, device_assignments
- `db/migrations/000003_inventory.{up,down}.sql` — installations, raw_installations, import_logs
- `db/migrations/000004_approvals.{up,down}.sql` — department_product_approvals, approval_scope_users, approval_scope_devices, approval_requests, product_version_advisories
- `db/migrations/000005_app.{up,down}.sql` — app_users, user_department_roles, audit_logs, inventory_audits, app_settings, notifications
- `db/migrations/000006_indexes_views.{up,down}.sql` — インデックス + `v_license_usage` ビュー
- `db/migrations/embed.go` — `//go:embed *.sql` を保持する `package migrations`
- `internal/db/sqlite.go` — `Open(cfg config.DatabaseConfig) (*sql.DB, func() error, error)`
- `internal/db/migrate.go` — `MigrateUp`、`MigrateDown`、`CheckVersion`、`RequiredMigrationVersion`
- `internal/db/sqlite_test.go`
- `internal/db/migrate_test.go`
- `sqlc.yaml`
- `db/queries/app_users.sql` — `GetAppUserByUsername`、`CreateAppUser`
- `internal/repository/{db,models,querier,app_users.sql}.go` — sqlc 生成（コミット対象）
- `cmd/create-app-user/main.go`

**編集**：

- `go.mod` / `go.sum` — 依存追加
- `cmd/server/main.go` — `chi.Router` 置換 + `db.Open` + `db.CheckVersion` + `defer closeDB()`（`closeDB` は `closeLog` より **後** に defer 登録：LIFO で `closeDB → closeLog` の順に実行され、DB クローズ中のエラーも logger が生きているうちに記録できる）
- `Makefile` — `BINARIES := appmgr-server appmgr-create-app-user`、`migrate-up` / `migrate-down` / `generate` ターゲット追加、`cmd/create-app-user` のビルドルール追加
- `README.md` — 起動手順に `make migrate-up` と必須スキーマ版 (=6) を追記

## Branch / PR 運用

- ブランチ：`feat/phase1-pr2-db-foundation`
- 最初のコミット：`docs(plans): フェーズ 1 PR2 DB 基盤の実装プラン`（Plan ファイル先行ルール）
- TDD サイクル必要箇所（`internal/db` の 3 テストセット）は RED → GREEN を別コミットで残す
- マージ：`gh pr merge --merge`。マージコミットは **Why / What / Impact** 形式

## Verification

### 手元での動作確認

```sh
nix develop

# ビルド (2 バイナリ)
make build
ls bin/                                   # appmgr-server, appmgr-create-app-user

# sqlc 生成物の再現性（差分が出ないことを確認）
make generate
git diff --exit-code internal/repository/

# テスト・レース・lint
make test
go test -race ./...
make lint

# マイグレーション手動確認
cp config.example.yml config.yml
make migrate-up
sqlite3 ./data/app.db ".tables"           # 24 テーブル + v_license_usage
sqlite3 ./data/app.db "SELECT version, dirty FROM schema_migrations;"  # version=6, dirty=0

# サーバ起動 + /healthz
make run &
curl -sf http://localhost:8180/healthz    # ok
kill %1

# 版数不一致での exit 1 を確認
make migrate-down
./bin/appmgr-server --config config.yml; echo "exit=$?"   # exit=1（ログに "schema version mismatch" 系）

# create-app-user 骨格
./bin/appmgr-create-app-user --help       # --username / --role / --reset-password / --notify-email
```

### CI 不在環境での代替確認（CI 導入は PR3 以降）

- PR 提出前に必ず `make test && make lint && make build` を通す
- `go test ./... -count=2` で flaky でないことを確認（マイグレ統合テストは tmpdir 利用なので衝突なしのはず）
- `git grep -l 'CGO_ENABLED=1'` でヒット 0 件
- `file bin/appmgr-server` で statically linked を確認（modernc/sqlite が pure Go である間接証明）

## 留意点

- 24 テーブル DDL の写経は要件書 `docs/specs/02_要件定義.md` § 4.2 を正本とする。論理削除は **必ず日時カラム**（`*_at` / `valid_to`）で、boolean フラグへの変換は厳禁（CLAUDE.md 規約）
- `internal/db/migrate_test.go` は `t.TempDir()` で一時 SQLite を作り、本番 `./data/app.db` を汚さない
- sqlc.yaml の queries パスは `db/queries/`、schema パスは `db/migrations/`（DDL を schema として再利用）
- `000006_indexes_views.down.sql` は `DROP VIEW IF EXISTS v_license_usage` を先に書き、その後 `DROP INDEX IF EXISTS ...`（VIEW がインデックスを参照している場合の依存順を明示）
- マージコミット例：「Why: 機能実装のための DB レイヤが必要。What: 24 テーブル DDL を 6 マイグレーション化、modernc/sqlite 接続、起動時版数チェック、chi 置換、appmgr-create-app-user 骨格、Makefile 拡張。Impact: 既存 /healthz の挙動は不変。`make migrate-up` 未実行で `appmgr-server` は exit 1 で停止する」
