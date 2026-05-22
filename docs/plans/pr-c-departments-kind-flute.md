# フェーズ 2 PR-C — departments CRUD

## Context

フェーズ 2 PR-B (`1028f21` でマージ済、`docs/plans/phase2-b-indexed-sparkle.md`) で `internal/handler/web/` パッケージと `internal/handler/handlertest/` のテスト基盤、`web.viewers` / `web.editors` の権限定数、`parseInt64Param` / `nilIfEmpty` / `derefString` / `likePattern` / `isUniqueConstraintErr` / `isForeignKeyErr` の共通ヘルパが揃った。本 PR-C は次のマスタ **departments** に同じ雛形を適用する位置づけで、ファイル追加が中心となる。

要件書 §11 (`docs/specs/02_要件定義.md:1118`) で `/departments` は **viewer 以上** と規定されている (vendors / products と権限グリッドが異なる)。本 PR で初めて以下のパターンを `web/` に導入する。後続 PR-D users / PR-E devices も `deactivated_at` / `retired_at` を持つため、これらが雛形となる。

- 論理削除 (`valid_to` を画面操作で立てる) + 復活
- 一覧の `?include_inactive=1` フィルタ
- 自己参照 FK の select UI (`parent_id` / `successor_department_id`)
- 閲覧 viewer / 編集 license_manager の二段権限 (general_user 除外)

`/admin/departments/migrate` (廃止部署のライセンス一括移管) と AD 同期 (`appmgr-sync-directory`) は別 PR に分離する。本 PR はあくまで `departments` 単体の CRUD に閉じる。

`internal/repository/models.go` には `Department` 構造体が sqlc 生成済 (`db/migrations/000001_master.up.sql:1-14` 由来)。よって作業は **SQL クエリ追加 → sqlc 再生成 → handler / templ 新規 → router 配線 → Nav 追加** に閉じる。

## 対象スコープ

### 画面と URL

| URL                              | メソッド | ロール                    | 用途                                                       |
| -------------------------------- | -------- | ------------------------- | ---------------------------------------------------------- |
| `/departments`                   | GET      | viewer 以上 (※)           | 一覧 + 検索 (`?q=`) + 廃止済み表示 (`?include_inactive=1`) |
| `/departments/new`               | GET      | license_manager 以上      | 新規フォーム                                               |
| `/departments`                   | POST     | license_manager 以上      | 作成                                                       |
| `/departments/{id}`              | GET      | viewer 以上               | 詳細 (子部署一覧 + 後継部署リンク)                         |
| `/departments/{id}/edit`         | GET      | license_manager 以上      | 編集フォーム                                               |
| `/departments/{id}`              | POST     | license_manager 以上      | 更新                                                       |
| `/departments/{id}/delete`       | POST     | license_manager 以上      | 論理削除 (`valid_to = DATE('now')`)                        |
| `/departments/{id}/restore`      | POST     | license_manager 以上      | 復活 (`valid_to = NULL`)                                   |

(※) PR-B の `viewers` (general_user 含む) ではなく、新設する `departmentViewers` (viewer / license_manager / department_security_admin / system_admin の 4 ロール) を使う。general_user は閲覧不可。

### スコープ外 (別 PR 送り)

- `/admin/departments/migrate` (廃止部署のライセンス一括移管 UI) — 承認管理 PR
- AD 同期で `source='ad'` のレコードを編集禁止にする制御 — AD 同期 PR が来た時点で再判断 (本 PR では仕込まない)
- 物理削除エンドポイント (出さない、削除は論理削除のみ)
- ページネーション (LIMIT 200 で打ち切り、超過時はバナー表示)
- 親子の循環チェック (A→B→A) — トリガーも実装ロジックも入れない
- HTMX 部分更新 (フルページリロード)
- show 画面の users / devices / licenses 一覧 (PR-D 以降で対応リソースが出てから別 PR で繋ぐ)

## 主要決定

| 項目                              | 決定                                                                                                                                                                                       |
| --------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 削除方針                          | 論理削除 (`UPDATE departments SET valid_to = DATE('now'), updated_at = CURRENT_TIMESTAMP WHERE id = ? AND valid_to IS NULL`)。既に廃止済みなら `:execrows` で 0 行を検知し 409 + flash 付き show 再描画。物理 DELETE は提供しない |
| 復活操作                          | `UPDATE departments SET valid_to = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND valid_to IS NOT NULL`。`:execrows` で 0 行 → 409 (現役を復活しようとした) |
| 一覧フィルタ                      | 既定で `valid_to IS NULL` のみ。`?include_inactive=1` で全件。検索 `?q=foo` (LIKE on `name` OR `code`) と併用可能。query パラメータの組合せで 4 つの SQL を呼び分け |
| 廃止済みの視認性                  | list / show で「廃止 (YYYY-MM-DD)」を表示。一覧では行に `class="row-inactive"` を付け CSS で淡色化                                                                  |
| parent_id / successor の select UI | 現役 (`valid_to IS NULL`) かつ自分自身でない部署のみ列挙。編集中レコードが既に廃止済み部署を参照している場合、その 1 件だけ「(廃止)」付きで option に残す      |
| 循環チェック                      | 実装しない。A→B→A が起きてもバグレポート待ちで対処 (3 回重複してから抽象化原則)                                                                                     |
| code UNIQUE 違反                  | 409 + flash「部署コードが重複しています」+ フォーム再表示 (vendors と同じパターン)                                                                                  |
| code バリデーション               | 必須、1〜64 文字、`^[A-Za-z0-9_-]+$`。`web.go` に `validateDepartmentCode(s string) string` を追加                                                                   |
| name バリデーション               | 必須、1〜128 文字 (UTF-8 文字数で数える: `utf8.RuneCountInString`)                                                                                                  |
| valid_from / valid_to 編集        | フォームに `valid_from` のみ出す (`<input type="date">`, 空欄許可)。`valid_to` は delete / restore ボタンで操作するためフォームには出さない                          |
| successor の参照先                | 自分自身でない、存在する。廃止済みは select から除外 (現在値の場合のみ「(廃止)」表記で残す)                                                                       |
| source の表示                     | list / show に `source` を表示。本 PR では全レコード `manual`。`source_ou` / `last_synced_at` は show のみ                                                          |
| 関連表示                          | 子部署一覧 + 後継部署リンクのみ。users / devices / licenses 一覧は本 PR では出さない                                                                                |
| 権限分離                          | `web.go` に `departmentViewers = []mw.Role{Viewer, LicenseManager, DepartmentSecurityAdmin, SystemAdmin}` を新設。`viewers` / `editors` は触らない                  |
| Nav                               | `internal/view/layout/base.templ` の Nav に `/departments` リンクを追加。表示は無条件 (権限不足ロールは押下時 403)                                                  |
| 並び順                            | list: `code ASC`。子部署一覧: `code ASC`                                                                                                                            |
| 検索                              | LIKE 部分一致 (`name LIKE ?1 OR code LIKE ?1`)、LIMIT 200。`likePattern` を流用                                                                                     |
| テスト                            | PR-B と同じく in-memory sqlite + `handlertest.NewTestDB` の統合テストのみ                                                                                          |

## 利用可能な PR-B 資産 (再利用するもの)

| ファイル                                                  | 流用するもの                                                                                          |
| --------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `internal/handler/web/web.go` の `viewers` / `editors`    | 編集権限は `editors` を流用。閲覧は独自定数を追加                                                     |
| `internal/handler/web/web.go` の `RegisterRoutes` 構造    | Group + With + RequireRole の置き方                                                                   |
| `internal/handler/web/vendors.go` 末尾の共通ヘルパ群      | `parseInt64Param` / `nilIfEmpty` / `derefString` / `likePattern` / `isUniqueConstraintErr` / `isForeignKeyErr` |
| `internal/handler/web/vendors.go:27` の `listLimit = 200` | 検索 / 一覧の上限                                                                                     |
| `internal/handler/handlertest/handlertest.go`             | `NewTestDB` / `NewRequest` / `PostForm` / `AssertStatus` / `AssertContains` / `AssertRedirect`        |
| `internal/handler/web/vendors_test.go` の `newWebRouter`  | テストセットアップ雛形を `departments_test.go` でも再利用                                              |
| `internal/view/layout/base.templ`                         | `Base(BaseProps)` レイアウト + `CSRFInput(token)`                                                     |
| `internal/view/vendors/helpers.go`                        | `itoa(int64) string` パターン (departments 版を同型で新設)                                            |
| `internal/view/products/form.templ` の select 関連        | selected プリセット用テンプレ関数パターン                                                             |
| `internal/view/vendors/show.templ`                        | 編集 / 削除ボタンの inline form パターン                                                              |

## ファイル構成

| パス                                                  | 概要                                                                                              | 区分         |
| ----------------------------------------------------- | ------------------------------------------------------------------------------------------------- | ------------ |
| `docs/plans/pr-c-departments-kind-flute.md`           | 本 Plan                                                                                           | 新規         |
| `db/queries/departments.sql`                          | List / ListIncludingInactive / Search / SearchIncludingInactive / Get / Create / Update / SoftDelete / Restore / ListChildren / ListActiveExcept | 新規 |
| `internal/repository/departments.sql.go`              | sqlc 生成                                                                                         | 新規 (生成)  |
| `internal/handler/web/departments.go`                 | 8 ハンドラ (List / NewForm / Create / Show / EditForm / Update / Delete / Restore) + `decodeDepartmentForm` + `validateDepartmentCode` + `validateDate` | 新規 |
| `internal/handler/web/departments_test.go`            | 一覧・新規フォーム描画 + 権限テスト (RED 1 本目)                                                  | 新規         |
| `internal/handler/web/departments_crud_test.go`       | Create / Show / Update / SoftDelete / Restore / UNIQUE 違反 / parent 自己ループ拒否 / 廃止済みフィルタ (RED 2 本目) | 新規 |
| `internal/view/departments/list.templ`                | 検索ボックス + include_inactive チェック + テーブル + 「新規」ボタン                              | 新規         |
| `internal/view/departments/form.templ`                | new / edit 兼用 (code / name / parent_id select / successor select / valid_from date)             | 新規         |
| `internal/view/departments/show.templ`                | 詳細 + 子部署一覧 + 後継部署 + delete/restore ボタン                                              | 新規         |
| `internal/view/departments/helpers.go`                | `itoa(int64) string` + `formatDate(*time.Time) string` + `sourceLabel(string) string`            | 新規         |
| `internal/view/departments/*_templ.go`                | templ 生成物                                                                                       | 新規 (生成)  |
| `internal/view/layout/base.templ`                     | Nav に `/departments` リンク追加                                                                  | 編集         |
| `internal/handler/web/web.go`                         | `departmentViewers` 追加 + `RegisterRoutes` に departments ルート群追加                            | 編集         |

`internal/handler/router.go` は変更不要 (PR-B で既に `web.RegisterRoutes` を呼んでいる)。

## SQL クエリ詳細 (`db/queries/departments.sql`)

すべて sqlc 命名規約に従う。SELECT 列は全カラム明示。

```sql
-- name: ListDepartments :many
SELECT * FROM departments
WHERE valid_to IS NULL
ORDER BY code
LIMIT 200;

-- name: ListDepartmentsIncludingInactive :many
SELECT * FROM departments ORDER BY code LIMIT 200;

-- name: SearchDepartments :many
SELECT * FROM departments
WHERE valid_to IS NULL AND (name LIKE ?1 OR code LIKE ?1)
ORDER BY code LIMIT 200;

-- name: SearchDepartmentsIncludingInactive :many
SELECT * FROM departments
WHERE name LIKE ?1 OR code LIKE ?1
ORDER BY code LIMIT 200;

-- name: GetDepartment :one
SELECT * FROM departments WHERE id = ? LIMIT 1;

-- name: ListChildDepartments :many
SELECT * FROM departments
WHERE parent_id = ? AND valid_to IS NULL
ORDER BY code LIMIT 200;

-- name: ListActiveDepartmentsExcept :many
-- parent_id / successor select 用。除外したい ID を渡せば自分以外、NULL 相当 (-1) を渡せば全件
SELECT * FROM departments
WHERE valid_to IS NULL AND id <> coalesce(?1, -1)
ORDER BY code;

-- name: CreateDepartment :one
INSERT INTO departments (code, name, parent_id, successor_department_id,
  valid_from, valid_to, source)
VALUES (?, ?, ?, ?, ?, NULL, 'manual')
RETURNING *;

-- name: UpdateDepartment :one
UPDATE departments
SET code = ?, name = ?, parent_id = ?, successor_department_id = ?,
    valid_from = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;
-- 注: valid_to は touch しない (delete/restore で別 SQL)

-- name: SoftDeleteDepartment :execrows
UPDATE departments
SET valid_to = DATE('now'), updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND valid_to IS NULL;

-- name: RestoreDepartment :execrows
UPDATE departments
SET valid_to = NULL, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND valid_to IS NOT NULL;
```

`:execrows` を使うのは「対象行が無かった」(既に廃止済みを再削除、現役を復活、不正な ID) を 0 行で検知して 409/404 に分岐するため。`db/queries/product_aliases.sql` の `DeleteAlias :execrows` と同じパターン。

## ハンドラ層の構造

### `internal/handler/web/web.go` への追加 (擬似)

```go
// /departments 系の閲覧権限。要件書 §11 で viewer 以上と規定 (general_user は除外)。
var departmentViewers = []mw.Role{
    mw.RoleViewer,
    mw.RoleLicenseManager,
    mw.RoleDepartmentSecurityAdmin,
    mw.RoleSystemAdmin,
}

func RegisterRoutes(r chi.Router, deps Deps) {
    v := &vendorHandlers{db: deps.DB, logger: deps.Logger}
    p := &productHandlers{db: deps.DB, logger: deps.Logger}
    d := &departmentHandlers{db: deps.DB, logger: deps.Logger}

    // 既存の vendors / products viewers / editors グループはそのまま

    r.With(mw.RequireRole(departmentViewers...)).Group(func(r chi.Router) {
        r.Get("/departments", d.list)
        r.Get("/departments/{id}", d.show)
    })
    r.With(mw.RequireRole(editors...)).Group(func(r chi.Router) {
        r.Get("/departments/new", d.newForm)
        r.Post("/departments", d.create)
        r.Get("/departments/{id}/edit", d.editForm)
        r.Post("/departments/{id}", d.update)
        r.Post("/departments/{id}/delete", d.delete)
        r.Post("/departments/{id}/restore", d.restore)
    })
}
```

### `departments.go` のハンドラ列 (シグネチャのみ)

```go
type departmentHandlers struct {
    db     *sql.DB
    logger *slog.Logger
}

func (h *departmentHandlers) list(w, r)       // ?q= と ?include_inactive=1 を 4 通り分岐
func (h *departmentHandlers) newForm(w, r)    // ListActiveDepartmentsExcept(NULL) を select に
func (h *departmentHandlers) create(w, r)     // decode → CreateDepartment → 303 /departments/:id
func (h *departmentHandlers) show(w, r)       // GetDepartment + ListChildDepartments + successor 解決
func (h *departmentHandlers) editForm(w, r)   // GetDepartment + ListActiveDepartmentsExcept(&id) + 廃止 parent/successor を 1 件だけ追加 fetch して残す
func (h *departmentHandlers) update(w, r)     // decode → UpdateDepartment、UNIQUE / parent_self / not_found 判定
func (h *departmentHandlers) delete(w, r)     // SoftDeleteDepartment → affected==0 で 409 + flash
func (h *departmentHandlers) restore(w, r)    // RestoreDepartment → affected==0 で 409 + flash
```

`showWithFlash` は `products.go` の同型を departments 版として複製する (3 回重複していないので抽象化はしない)。

`decodeDepartmentForm` は PR-B `productInput` と同じ「フォーム入力構造体 + view 用詰替え + sqlc params 詰替え」の三点セット。

## コミット列 (TDD サイクル)

ブランチ: `feat/phase2-pr-c-departments` (CLAUDE.md「最初のコミットを Plan ファイルにする」)

| #  | コミット件名                                                                       | サイクル  |
| -- | ---------------------------------------------------------------------------------- | --------- |
| 1  | `docs(plans): フェーズ 2 PR-C departments CRUD の実装プラン`                       | —         |
| 2  | `feat(db/queries): departments の CRUD + 論理削除 SQL を追加`                      | —         |
| 3  | `test(handler/web/departments): 一覧・新規フォーム描画 + 権限 (RED)`               | RED       |
| 4  | `feat(handler/web/departments): List / NewForm ハンドラ + templ + 配線`            | GREEN     |
| 5  | `test(handler/web/departments): Create / Show / Update + UNIQUE 違反 (RED)`        | RED       |
| 6  | `feat(handler/web/departments): Create / Show / EditForm / Update ハンドラ`        | GREEN     |
| 7  | `test(handler/web/departments): 論理削除 / 復活 / 二重削除 409 (RED)`              | RED       |
| 8  | `feat(handler/web/departments): SoftDelete / Restore ハンドラ`                     | GREEN     |
| 9  | `test(handler/web/departments): include_inactive フィルタ + 廃止表示 (RED)`        | RED       |
| 10 | `feat(handler/web/departments): include_inactive 分岐と廃止行スタイル`             | GREEN     |
| 11 | `test+feat(handler/web/departments): parent 自己参照拒否 + 廃止 parent option`     | RED+GREEN |
| 12 | `feat(view/layout): Nav に /departments リンク追加`                                | —         |

コミット 11 は parent 関連の 2 つの細かい挙動を 1 コミットにまとめる (RED-only コミットを過剰に細分化しない)。

## 受け入れ基準

- [ ] `make generate` 後 `git status` がクリーン (sqlc / templ 生成物含む)
- [ ] `make build` で 11 バイナリ全てビルド可能
- [ ] `make test` / `go test -race ./...` 緑
- [ ] `make lint` 緑
- [ ] `make run` 起動後、以下が成功:
  - `curl -i -H 'X-User-Role: general_user' http://localhost:8180/departments` → **403** (新パターン)
  - `curl -H 'X-User-Role: viewer' http://localhost:8180/departments` → 200、「新規」ボタンは描画されない
  - `curl -H 'X-User-Role: license_manager' http://localhost:8180/departments/new` → 200、code / name / parent select 描画
  - 部署作成 (`DEPT001` / `営業部`) → 子部署作成 (`DEPT002` / `営業1課`, parent_id=1) → DEPT002 show で「親: 営業部」表示
  - `POST /departments/1/delete` → 303 → show で「廃止 (YYYY-MM-DD)」と「復活」ボタン表示
  - `POST /departments/1/delete` を再投: 409 + flash「既に廃止されています」
  - `POST /departments/1/restore` → 303 → 「廃止」表記が消える
  - `/departments?include_inactive=1` で廃止済みが行に表示される
- [ ] code 重複: 同じ code で `POST /departments` → 409 + flash + 入力値再表示
- [ ] parent_id に自身の ID を渡して `POST /departments/{id}` → 400 + flash「自身を親に設定できません」
- [ ] CSRF トークン無し POST が 403 になることを 1 ハンドラで確認
- [ ] Nav に `/departments` リンクが出る
- [ ] `/admin/departments/migrate` は本 PR で実装していないことを PR 本文に明記

## 動作検証手順

1. `nix develop`
2. `make generate` (sqlc + templ)
3. `make build`
4. `make migrate-up` (`./data/app.db` 初期化)
5. `make run`
6. 別端末で:

   ```sh
   curl -i -H 'X-User-Role: viewer' http://localhost:8180/departments
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d 'code=DEPT001&name=営業部&_csrf=dummy-csrf-token' \
        http://localhost:8180/departments
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d '_csrf=dummy-csrf-token' \
        http://localhost:8180/departments/1/delete
   curl -i -H 'X-User-Role: viewer' \
        'http://localhost:8180/departments?include_inactive=1'
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d '_csrf=dummy-csrf-token' \
        http://localhost:8180/departments/1/restore
   ```

7. ブラウザで `/departments` を開き、Nav リンク・`include_inactive` チェックボックス・廃止部署の淡色表示を目視
8. `make test` で in-memory sqlite ベースの統合テストが完走

## PR-D 以降への引き継ぎ

- `departmentViewers` の追加で「URL ごとに viewer / editor の閾値が変わる」運用を確立。PR-D users (`/users` は viewer 以上、`/admin/app-users` は system_admin) も同様に独自定数を追加する形になる
- 論理削除 + 復活 (`SoftDelete*` / `Restore*` + `:execrows` で 0 行を 409 化) のパターンを確立。PR-D users (`deactivated_at`) / PR-E devices (`retired_at`) でそのまま流用 (列名差し替えのみ)
- `?include_inactive=1` フィルタも同じ命名で users / devices に展開
- 自己参照 FK の select (`ListActiveDepartmentsExcept(&id)`) パターンは現状 departments 固有。users の supervisor 等の自己参照が将来出てきた時の雛形
- show 画面の「関連リソース」セクションは本 PR では子部署のみ。PR-D users が出たら departments show に「所属ユーザ一覧」を追加する PR を別立てするのが自然 (本 PR の責務外)
- AD 同期 PR が来た時点で「`source='ad'` のレコードは編集禁止」を `decodeDepartmentForm` / `update` ハンドラに 1 行追加する形で導入可能 (本 PR では仕込まない)
- `/admin/departments/migrate` は承認管理 PR で初実装。本 PR では URL も予約しない
