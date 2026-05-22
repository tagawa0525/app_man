# フェーズ 2 PR-B — vendors + products + product_aliases CRUD

## Context

フェーズ 2 PR-A (`docs/plans/snazzy-percolating-micali.md`、`e4cba80` でマージ済) で「templ + HTMX + ダミー認可 + CSRF + 共通レイアウト + handlertest」が揃った。Chi ルータに登録されているのは `/healthz` と `/static/*` のみで、業務ハンドラはまだ 1 つもない。

要件書 §12 の実装順序「2. マスタ系」を進めるフェーズ 2 を 5 PR (A〜E) に分割した方針 (`docs/plans/snazzy-percolating-micali.md` 参照) のうち、本 PR-B は **製品マスタ系 3 テーブル** (`vendors` / `products` / `product_aliases`) の CRUD を初の「業務ハンドラ」として投入する位置づけ。後続の departments (PR-C) / users (PR-D) / devices (PR-E) は同じパターンを踏襲するため、本 PR で「web ハンドラ層の置き方」「テンプレ配置」「ハンドラ統合テストの形」を確立する。

マイグレーション `db/migrations/000001_master.up.sql:16-48` に対象 3 テーブルの DDL は既にあり、`internal/repository/models.go` には `Vendor` / `Product` / `ProductAlias` 構造体が sqlc 生成済 (PR2 でモデル生成済、`app_users.sql` のみクエリを書いた状態)。よって本 PR の作業は「**SQL クエリ追加 → sqlc 再生成 → handler / templ 新規作成 → router 配線**」に閉じる。

承認系 (`default_approval_status` を画面から触る `/admin/global-approvals`)・名寄せキュー (`/products/aliases/pending`)・`product_version_advisories` の UI は本 PR の対象外 (それぞれ承認管理 PR / SKYSEA 取込み PR で扱う)。

## 対象スコープ

### 画面と URL

| URL                                    | メソッド | ロール             | 用途                                |
| -------------------------------------- | -------- | ------------------ | ----------------------------------- |
| `/vendors`                             | GET      | general_user 以上  | 一覧 + 検索ボックス                 |
| `/vendors/new`                         | GET      | license_manager 以上 | 新規フォーム                        |
| `/vendors`                             | POST     | license_manager 以上 | 作成                                |
| `/vendors/{id}`                        | GET      | general_user 以上  | 詳細 (配下 product 一覧含む)        |
| `/vendors/{id}`                        | POST     | license_manager 以上 | 更新                                |
| `/vendors/{id}/delete`                 | POST     | license_manager 以上 | 削除 (FK 違反時 409)                |
| `/products`                            | GET      | general_user 以上  | 一覧 + 検索 (canonical_name / alias) |
| `/products/new`                        | GET      | license_manager 以上 | 新規フォーム                        |
| `/products`                            | POST     | license_manager 以上 | 作成                                |
| `/products/{id}`                       | GET      | general_user 以上  | 詳細 (alias 一覧含む)               |
| `/products/{id}`                       | POST     | license_manager 以上 | 更新                                |
| `/products/{id}/delete`                | POST     | license_manager 以上 | 削除                                |
| `/products/{id}/aliases`               | POST     | license_manager 以上 | alias 追加                          |
| `/products/{id}/aliases/{aid}/delete`  | POST     | license_manager 以上 | alias 削除                          |

### スコープ外 (別 PR 送り)

- `/admin/global-approvals` (system_admin が `default_approval_status` を一括設定する画面) → 承認管理 PR
- `/products/aliases/pending` (名寄せキュー) → SKYSEA 取込み PR (`raw_installations` と一緒に実装するのが自然)
- `product_version_advisories` の UI (MVP 不要、要件書 §4.2 で DDL のみと明記)
- HTMX 部分更新の先例作り (alias の add/remove も含めフルページリロードで実装。HTMX の業務先例は影響範囲が狭い別箇所で作る方が議論が単純)
- ページネーション (LIMIT 200 で打ち切り、超過時のみ警告メッセージ表示)
- 削除確認の HTMX モーダル (素の `confirm()` で済ます)

## 主要決定

| 項目                       | 決定                                                                                                                              | 根拠                                                                                                                                                            |
| -------------------------- | --------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| ハンドラ層の配置           | `internal/handler/web/` サブパッケージを **本 PR で新設**                                                                         | 要件書 §8.9 (`docs/specs/02_要件定義.md:1258-1268`) で `web/` / `api/` 分離を明記。業務ハンドラ初投入の今がコスト最小のタイミング。`router.go` は handler 直下に残し `web.RegisterRoutes(r, deps)` を呼ぶ |
| templ 配置                 | `internal/view/vendors/` と `internal/view/products/` の 2 パッケージ。alias は products 詳細画面に同居                           | layout / errors と同階層の平構造                                                                                                                                |
| repository の引き回し      | handler は `repository.New(deps.DB)` を都度呼ぶ。`Deps` に `*Queries` を持たせない                                                | `Queries` は薄いラッパで生成コスト無視できる。トランザクション時は `db.BeginTx` → `q.WithTx(tx)` で直線的に書ける。`Deps` を太らせる利得が薄い                  |
| sqlc クエリ                | 純粋な CRUD のみ (Insert / Update / Delete / Get / List / Search)。UPSERT は使わない                                              | 要件書 §5.1 は「製品の CRUD」のみ規定。UPSERT は SKYSEA 取込み (名寄せ自動学習) で初めて必要になる                                                               |
| 削除方針                   | 物理 `DELETE`。FK 違反時は 409 Conflict + flash で再表示                                                                          | 仕様書 §4.2 の論理削除日時カラム規約は「無効化が監査情報」となる運用テーブル向け。マスタの誤入力削除は物理削除が自然                                              |
| 検索                       | LIKE 部分一致のみ (vendors.name / products.canonical_name OR alias_name)。LIMIT 200。超過時はバナー表示                           | 母集合数百〜千件規模、ページネーションは「壊れる規模になってから」                                                                                              |
| バリデーション             | サーバ側のみ。失敗時は flash + 入力値再表示。`software_type` ∈ `{installed, saas, both}` / `default_approval_status` ∈ `{globally_approved, globally_prohibited, department_discretion, unknown}` / `license_required` は 3-state (true / false / unset) | 要件書 §4.2 の products 列定義 (`db/migrations/000001_master.up.sql:30,32`) と完全一致させる                                                                     |
| alias add/remove           | フルページリロード (POST → 303 See Other → GET `/products/:id`)                                                                   | HTMX 部分更新は別箇所で先例を作る方が議論が単純。alias 操作の頻度は低く体感差が無視できる                                                                       |
| ハンドラのテスト粒度       | **in-memory sqlite (`file::memory:?cache=shared`) + `db.MigrateUp` で都度マイグレ** の統合テストのみ。repository 専用テストは書かない | sqlc 生成 CRUD に専用テストの利得は薄い。HTTP → templ → DB の往復で 1 テストが 1 操作を表現できる。`handlertest.NewTestDB(t)` ヘルパを本 PR で追加              |
| Nav の更新                 | `internal/view/layout/base.templ:44-49` の `Nav` に `/vendors` / `/products` リンクを追加 (全ロール表示)                          | 閲覧は general_user 以上で許可、出し分け不要                                                                                                                    |
| 一覧並び順                 | vendors: `name ASC` / products: `vendor.name, canonical_name, edition ASC NULLS FIRST`                                            | 安定ソート前提                                                                                                                                                  |

## 利用可能な PR-A 資産 (再利用するもの)

- `internal/handler/middleware/auth.go:71-85` の `RequireRole(...Role)` → `r.With(...)` で認可ゲート
- `internal/handler/middleware/auth.go:61-66` の `RoleFrom(ctx)` → ハンドラ内で role 取得し templ へ渡す
- `internal/handler/middleware/csrf.go` の `DummyCSRFToken` 定数 + `CSRFMiddleware` (POST 自動検証)
- `internal/view/layout/base.templ:18-39` の `Base(BaseProps{Title, Role, Flash, CSRFToken})` レイアウト
- `internal/view/layout/base.templ:55-57` の `CSRFInput(token)` (form 内 hidden input)
- `internal/view/errors/` の NotFound (404 描画は `router.go:58-66` 経由で chi に渡る)
- `internal/handler/handlertest/handlertest.go:24-78` の `NewRequest` / `PostForm` / `AssertStatus` / `AssertContains` / `AssertRedirect`
- `internal/db.Open(cfg)` (現状 `internal/db/sqlite.go`)
- `internal/db.MigrateUp` (新規追加または既存 API の流用、テストヘルパで使う)

## 実装ステップ (コミット列)

ブランチ: `feat/phase2-pr-b-vendors-products` (mainからは触らない。CLAUDE.md「最初のコミットを Plan ファイルにする」を守る)

| # | コミット件名                                                                       | サイクル |
| - | ---------------------------------------------------------------------------------- | -------- |
| 1 | `docs(plans): フェーズ 2 PR-B vendors + products + product_aliases の実装プラン`   | —        |
| 2 | `feat(handler/handlertest): NewTestDB (in-memory sqlite + MigrateUp) ヘルパ`       | —        |
| 3 | `feat(db/queries): vendors / products / product_aliases の SQL + make generate`    | —        |
| 4 | `test(handler/web/vendors): 一覧・新規フォーム描画 (RED)`                          | RED      |
| 5 | `feat(handler/web/vendors): List / NewForm ハンドラ + templ + RegisterRoutes 配線` | GREEN    |
| 6 | `test(handler/web/vendors): Create / Show / Update / Delete + FK 違反 409 (RED)`   | RED      |
| 7 | `feat(handler/web/vendors): Create / Show / Update / Delete ハンドラ + form templ` | GREEN    |
| 8 | `test(handler/web/products): 一覧・新規フォーム・詳細描画 (RED)`                   | RED      |
| 9 | `feat(handler/web/products): List (検索含む) / NewForm / Show ハンドラ + templ`    | GREEN    |
|10 | `test(handler/web/products): Create / Update / Delete (RED)`                       | RED      |
|11 | `feat(handler/web/products): Create / Update / Delete ハンドラ`                    | GREEN    |
|12 | `test+feat(handler/web/products): alias の POST add / POST delete`                 | RED+GREEN|
|13 | `feat(view/layout): Nav に /vendors / /products リンク追加`                        | —        |

コミット 12 は 1 行〜数行 RED コミットを分けると不自然に細かくなるためまとめる (CLAUDE.md「揺れも履歴に残す」精神は維持しつつ過剰分割を避ける)。

## ファイル構成

| パス                                                  | 概要                                                                              | 区分         |
| ----------------------------------------------------- | --------------------------------------------------------------------------------- | ------------ |
| `docs/plans/phase2-b-indexed-sparkle.md`              | 本 Plan                                                                           | 新規         |
| `db/queries/vendors.sql`                              | List / Search / Get / Create / Update / Delete + `CountProductsByVendor`          | 新規         |
| `db/queries/products.sql`                             | List (vendor JOIN) / Search (alias JOIN DISTINCT) / Get / ListByVendor / CRUD     | 新規         |
| `db/queries/product_aliases.sql`                      | ListByProduct / Create / Delete                                                    | 新規         |
| `internal/repository/vendors.sql.go`                  | sqlc 生成                                                                         | 新規 (生成)  |
| `internal/repository/products.sql.go`                 | sqlc 生成                                                                         | 新規 (生成)  |
| `internal/repository/product_aliases.sql.go`          | sqlc 生成                                                                         | 新規 (生成)  |
| `internal/handler/web/web.go`                         | `RegisterRoutes(r chi.Router, deps handler.Deps)` のエントリ + 内部 `Viewers/Editors` 定数 | 新規 |
| `internal/handler/web/vendors.go`                     | 6 ハンドラ (List / NewForm / Create / Show / Update / Delete)                     | 新規         |
| `internal/handler/web/vendors_test.go`                | 統合テスト 7-8 件                                                                  | 新規         |
| `internal/handler/web/products.go`                    | 8 ハンドラ (CRUD + alias add/delete)                                              | 新規         |
| `internal/handler/web/products_test.go`               | 統合テスト 10-12 件                                                                | 新規         |
| `internal/handler/web/forms.go`                       | `decodeVendor(r)` / `decodeProduct(r)` (`(input, errs map[string]string)`)        | 新規         |
| `internal/view/vendors/list.templ`                    | 検索ボックス + テーブル + 「新規」ボタン (license_manager 以上)                    | 新規         |
| `internal/view/vendors/form.templ`                    | new / edit 兼用                                                                    | 新規         |
| `internal/view/vendors/show.templ`                    | 詳細 + 配下 product 一覧                                                          | 新規         |
| `internal/view/products/list.templ`                   | 検索ボックス + テーブル                                                            | 新規         |
| `internal/view/products/form.templ`                   | new / edit 兼用 (vendor select / software_type select / approval status select)   | 新規         |
| `internal/view/products/show.templ`                   | 詳細 + alias 一覧 + alias 追加フォーム                                            | 新規         |
| `internal/view/{products,vendors}/*_templ.go`         | templ 生成                                                                         | 新規 (生成)  |
| `internal/handler/handlertest/db.go`                  | `NewTestDB(t *testing.T) *sql.DB`                                                  | 新規         |
| `internal/handler/router.go`                          | `web.RegisterRoutes(r, deps)` 呼び出しを追加                                       | 編集         |
| `internal/view/layout/base.templ`                     | `Nav` に `/vendors` / `/products` を追加                                          | 編集         |

## SQL クエリ詳細

### `db/queries/vendors.sql`

- `ListVendors :many` — `SELECT * FROM vendors ORDER BY name LIMIT 200`
- `SearchVendors :many` — `WHERE name LIKE ?1 ESCAPE '\' ORDER BY name LIMIT 200`
- `GetVendor :one` — by id
- `CreateVendor :one` — `INSERT ... RETURNING *`
- `UpdateVendor :one` — `UPDATE ... SET name=?, url=?, note=?, updated_at=CURRENT_TIMESTAMP WHERE id=? RETURNING *`
- `DeleteVendor :exec`
- `CountProductsByVendor :one` — 削除可否判定用

### `db/queries/products.sql`

- `ListProducts :many` — `vendors JOIN products` 全件、`ORDER BY vendors.name, canonical_name, edition NULLS FIRST LIMIT 200`
- `SearchProducts :many` — `canonical_name LIKE ?1 OR EXISTS (SELECT 1 FROM product_aliases WHERE product_id=products.id AND alias_name LIKE ?1)` で DISTINCT 不要
- `GetProduct :one` — vendor JOIN 版
- `ListProductsByVendor :many` — vendor 詳細用 (vendor.id = ?)
- `CreateProduct :one`
- `UpdateProduct :one`
- `DeleteProduct :exec`

### `db/queries/product_aliases.sql`

- `ListAliasesByProduct :many`
- `CreateAlias :one` — `source='manual'` ハードコード
- `DeleteAlias :exec`

UPSERT は使わない (SKYSEA 取込み PR で別途検討)。

## ハンドラ層の構造 (擬似コード)

```go
// internal/handler/web/web.go
package web

import (
    "github.com/go-chi/chi/v5"
    "github.com/tagawa0525/app_man/internal/handler"
    mw "github.com/tagawa0525/app_man/internal/handler/middleware"
)

var viewers = []mw.Role{
    mw.RoleGeneralUser, mw.RoleViewer, mw.RoleLicenseManager,
    mw.RoleDepartmentSecurityAdmin, mw.RoleSystemAdmin,
}
var editors = []mw.Role{
    mw.RoleLicenseManager, mw.RoleDepartmentSecurityAdmin, mw.RoleSystemAdmin,
}

func RegisterRoutes(r chi.Router, deps handler.Deps) {
    v := &vendorHandlers{db: deps.DB, logger: deps.Logger}
    p := &productHandlers{db: deps.DB, logger: deps.Logger}

    r.With(mw.RequireRole(viewers...)).Group(func(r chi.Router) {
        r.Get("/vendors", v.list)
        r.Get("/vendors/{id}", v.show)
        r.Get("/products", p.list)
        r.Get("/products/{id}", p.show)
    })
    r.With(mw.RequireRole(editors...)).Group(func(r chi.Router) {
        r.Get("/vendors/new", v.newForm)
        r.Post("/vendors", v.create)
        r.Post("/vendors/{id}", v.update)
        r.Post("/vendors/{id}/delete", v.delete)
        r.Get("/products/new", p.newForm)
        r.Post("/products", p.create)
        r.Post("/products/{id}", p.update)
        r.Post("/products/{id}/delete", p.delete)
        r.Post("/products/{id}/aliases", p.aliasCreate)
        r.Post("/products/{id}/aliases/{aid}/delete", p.aliasDelete)
    })
}
```

`router.go` は `web.RegisterRoutes(r, deps)` を 1 行追加するだけ。

## 受け入れ基準

- [ ] `make generate` 実行後に `git status` がクリーン (sqlc / templ 生成物含む)
- [ ] `make build` で 11 バイナリ全てがビルド可能
- [ ] `make test` 緑、`go test -race ./...` 緑
- [ ] `make lint` 緑
- [ ] `make run` 起動後、以下が手動で確認できる:
  - `curl -H 'X-User-Role: general_user' http://localhost:8180/vendors` → 200, HTML, 「新規」ボタンは描画されない
  - `curl -H 'X-User-Role: license_manager' http://localhost:8180/vendors/new` → 200, フォーム描画
  - `curl -H 'X-User-Role: general_user' -i http://localhost:8180/vendors/new` → 403
  - vendor 作成 → product 作成 (vendor select) → alias 追加 → alias 削除 → product 削除 → vendor 削除の一連操作が成功
  - vendor 配下に product がある状態で `POST /vendors/:id/delete` → 409 + flash「配下に製品があるため削除できません」と再表示
- [ ] 全ハンドラに role 認可テストが許可 / 拒否それぞれ 1 件以上
- [ ] CSRF トークン無し POST が 403 になることを 1 ハンドラで確認 (代表 1 件、middleware は PR-A で検証済み)
- [ ] Nav に `/vendors` / `/products` リンクが出る
- [ ] 検索: vendors / products それぞれで部分一致が効く (alias 経由検索も 1 件)
- [ ] 名寄せキュー / 全社禁止承認は本 PR の対象外と PR 本文に明記

## 動作検証手順

1. `nix develop` で開発環境に入る
2. `make generate` (sqlc + templ)
3. `make build`
4. `make migrate-up` (`./data/app.db` を初期化)
5. `make run`
6. 別端末で受け入れ基準の `curl` を順に実行
7. ブラウザ (Chrome devtools) で:
   - `/static/htmx.min.js` の読込先がローカルであること (外部 CDN 不参照)
   - `<meta name="csrf-token">` と `<body hx-headers=...>` に固定ダミートークンが入っていること
8. `make test` で in-memory sqlite ベースの統合テストが完走することを確認

## PR-C 以降への引き継ぎ

- `internal/handler/web/` パッケージは本 PR で確立。PR-C departments / PR-D users / PR-E devices は同パッケージにファイル追加で済む
- `handlertest.NewTestDB` は PR-C 以降の handler テストで再利用
- `view/products/form.templ` の「select + サーバ側バリデーション + 入力値再表示」パターンが他 PR の form 雛形
- UPSERT / HTMX 部分更新 / ページネーション は「3 回重複してから抽象化」原則に従い、後続 PR で需要が確認された時点で導入
- `/admin/global-approvals` (承認管理 PR) と `/products/aliases/pending` (SKYSEA 取込み PR) は本 PR で URL だけ予約せず、それぞれの PR で初登場させる
