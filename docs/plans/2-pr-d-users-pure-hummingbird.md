# フェーズ 2 PR-D — users CRUD

## Context

フェーズ 2 PR-C (`docs/plans/pr-c-departments-kind-flute.md`) でマージ済の departments CRUD によって、本 PR-D で **そのまま踏襲できる雛形** が以下のとおり揃った。`internal/handler/web/departments.go` (584 行) と `db/queries/departments.sql` (212 行) が写経元になる。

- 4 種 SQL 切替の一覧 (`?q=` × `?include_inactive=1` の 4 通り) + `truncated` バナー
- 論理削除 + 復活 (`:execrows` で 0 行 → 409 / 404 振り分け) と `showWithFlash` 再描画
- FK の select UI と `PinnedOption` (現役選択肢に含まれない参照先を 1 件 fetch して残す)
- `departmentViewers` (general_user 除外) と `editors` の二段権限
- 統合テスト基盤 `newWebRouter` / `handlertest.PostForm` / `AssertRedirect` etc.

`internal/repository/models.go:268-282` には `User` 構造体が sqlc 生成済 (`db/migrations/000001_master.up.sql:64-78` 由来)。本 PR の作業は **SQL クエリ追加 → sqlc 再生成 → handler / templ 新規 → router 配線 → Nav 追加** に閉じ、新規マイグレーションは無い。

users 固有の差分は次の通り。

- `deactivated_at` は **DATETIME** (departments の `valid_to` は DATE)。SoftDelete SQL は `CURRENT_TIMESTAMP` を立てる
- `username` / `email` / `department_id` は **nullable**。フォーム空欄は `nilIfEmpty` で NULL 化
- 検索は 4 カラム OR (`employee_code` / `username` / `name` / `email`)
- FK 参照は **単一の `department_id`** のみ (自己参照は無い)
- 廃止部署の表示は `name (〜YYYY-MM-DD)` の併記式 (departments の固定文言 `(廃止)` とは違う方針)
- 要件書 §6.1 で `/users` は **viewer 以上** と規定 → `departmentViewers` を流用

AD 同期 (`source='ad'` の編集禁止)、`app_users` 自動生成、`user_department_roles` 付与、`/admin/app-users`、退職者ダッシュボードはすべて別 PR。本 PR は users 単体 CRUD に閉じる。

## 対象スコープ

### 画面と URL

| URL                          | メソッド | ロール                | 用途                                                       |
| ---------------------------- | -------- | --------------------- | ---------------------------------------------------------- |
| `/users`                     | GET      | viewer 以上 (※)       | 一覧 + 検索 (`?q=`) + 退職者表示 (`?include_inactive=1`)   |
| `/users/new`                 | GET      | license_manager 以上  | 新規フォーム                                               |
| `/users`                     | POST     | license_manager 以上  | 作成                                                       |
| `/users/{id}`                | GET      | viewer 以上           | 詳細 (所属部署リンク)                                      |
| `/users/{id}/edit`           | GET      | license_manager 以上  | 編集フォーム                                               |
| `/users/{id}`                | POST     | license_manager 以上  | 更新                                                       |
| `/users/{id}/delete`         | POST     | license_manager 以上  | 論理削除 (`deactivated_at = CURRENT_TIMESTAMP`)            |
| `/users/{id}/restore`        | POST     | license_manager 以上  | 復活 (`deactivated_at = NULL`)                             |

(※) `departmentViewers` (viewer / license_manager / department_security_admin / system_admin) を流用。general_user は閲覧不可。

### スコープ外 (別 PR 送り)

- AD 同期 (`appmgr-sync-directory`) と users の連携、`source='ad'` のレコード編集禁止
- `app_users` 自動生成 (要件書 §4.2) と `user_department_roles` 付与
- `/admin/app-users` (system_admin 専用画面、要件書 §6.1)
- 退職者の未解除割当一覧 (`/dashboard` 系)
- show 画面の `app_users` / `user_assignments` / `devices.primary_user` 関連表示 — 対応リソース PR が出てから別 PR で接続
- 物理削除エンドポイント (出さない)
- HTMX 部分更新 (フルページリロード)
- ページネーション (`listLimit = 200` で打ち切り、超過時バナー)

## 主要決定

| 項目                            | 決定                                                                                                                                                                |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 削除方針                        | `UPDATE users SET deactivated_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND deactivated_at IS NULL`。`:execrows` 0 行 → 409 / 404         |
| 復活操作                        | `UPDATE users SET deactivated_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND deactivated_at IS NOT NULL`。`:execrows` 0 行 → 409                       |
| 一覧フィルタ                    | 既定 `deactivated_at IS NULL` のみ。`?include_inactive=1` で全件。検索 `?q=foo` と組合せ可、4 つの SQL を呼び分け                                                    |
| 検索対象                        | `employee_code` / `username` / `name` / `email` の 4 カラム OR LIKE (nullable カラムへの LIKE は NULL に対し false。検索結果から落ちる挙動を受容)                  |
| 退職者の視認性                  | list / show で「退職 (YYYY-MM-DD HH:MM)」表示。一覧行に `class="row-inactive"`                                                                                      |
| employee_code バリデーション   | 必須、1〜64 文字、`^[A-Za-z0-9_-]+$`。`users.go` 内に独自実装 (departments の `validateDepartmentCode` と共通化しない — 3 回ルール未達)                              |
| username バリデーション         | 任意 (空欄 → NULL)、1〜128 文字。AD `sAMAccountName` 想定                                                                                                            |
| name バリデーション             | 必須、1〜128 文字 (`utf8.RuneCountInString`)                                                                                                                        |
| email バリデーション            | 任意 (空欄 → NULL)、`@` を 1 個以上含むだけの緩いチェック (仕様書に厳格な形式規定なし)                                                                              |
| department_id                  | 任意 FK。`parseInt64Opt` で `*int64` 化。存在しない ID 投入は `isForeignKeyErr` で検知し 400 + flash「指定された部署は存在しません」                                |
| employee_code UNIQUE 違反       | 409 + flash「従業員コードが重複しています」 + フォーム再表示                                                                                                        |
| 廃止部署表記                    | list 所属部署列 / show 所属部署リンク / 編集フォーム select で **共通に** `name (〜YYYY-MM-DD)` を出す。helpers の `departmentLabel(d) string` に集約               |
| PinnedOption                    | 編集中レコードが廃止部署を指す場合、`resolvePinnedDepartmentForUser` で 1 件 fetch して `(〜YYYY-MM-DD)` 付き option として残す                                       |
| source                          | list / show で `sourceLabel(s)` を表示 (departments 同型関数を `users` 配下に複製。3 回ルール未達)。本 PR は全レコード `manual`                                       |
| 関連表示 (show)                 | 所属部署リンクのみ。`app_users` / `user_assignments` / `devices` は対応 PR 後に別 PR で追加                                                                          |
| 権限分離                        | 閲覧は既存の `departmentViewers` を流用 (新規定数追加なし)。編集は `editors`                                                                                        |
| Nav                             | `internal/view/layout/base.templ` の Nav に `/users` リンクを追加。出し分けはしない (押下時 403)                                                                    |
| 並び順                          | list: `employee_code ASC`                                                                                                                                           |
| AD source 制御                   | 本 PR では入れない。`source='ad'` のレコードも編集可能。AD 同期 PR で再判断                                                                                          |

## 利用可能な PR-B / PR-C 資産 (再利用するもの)

| ファイル                                                       | 流用するもの                                                                                                              |
| -------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `internal/handler/web/web.go` の `departmentViewers` / `editors` | 閲覧 / 編集権限の定数をそのまま流用 (新規定数追加なし)                                                                    |
| `internal/handler/web/web.go` の `RegisterRoutes` 構造          | departments と同じ Group + With + RequireRole の積み方                                                                    |
| `internal/handler/web/vendors.go` 末尾の共通ヘルパ群             | `parseInt64Param` / `nilIfEmpty` / `derefString` / `likePattern` / `isUniqueConstraintErr` / `isForeignKeyErr` / `listLimit` |
| `internal/handler/web/departments.go` の `parseInt64Opt`        | nullable FK / 整数のフォーム入力パース                                                                                    |
| `internal/handler/web/departments.go` の `lookupDepartment`     | `*int64` から `*Department` への safe fetch (`sql.ErrNoRows` を nil 扱い)                                                  |
| `internal/handler/web/departments.go` の `resolvePinnedDepartments` パターン | `resolvePinnedDepartmentForUser` (単一 ID 版) として複製。3 回ルール未達のため共通化はしない                              |
| `internal/handler/web/departments.go` の `showWithFlash` パターン | users 版を `users.go` 内に複製                                                                                            |
| `internal/handler/handlertest/handlertest.go`                  | `NewTestDB` / `NewRequest` / `PostForm` / `AssertStatus` / `AssertContains` / `AssertRedirect`                            |
| `internal/handler/web/vendors_test.go` の `newWebRouter`        | `users_test.go` でも同じものを呼ぶ (既に export 済の test helper)                                                          |
| `internal/view/layout/base.templ`                              | Base レイアウト + Flash + CSRFInput                                                                                       |
| `internal/view/departments/{list,form,show}.templ`             | テンプレ構成と select / pinned option / inline form パターンを写経                                                        |
| `internal/view/departments/helpers.go` の `itoa` / `canEdit` / `sourceLabel` / `formatDate` | users 版の `helpers.go` に複製 (パッケージ独立)                                              |
| `db/queries/departments.sql` の `ListActiveDepartments` (既存) | users フォームの department select 選択肢として **そのまま流用** (新規追加不要)                                            |

## ファイル構成

| パス                                                  | 概要                                                                                          | 区分        |
| ----------------------------------------------------- | --------------------------------------------------------------------------------------------- | ----------- |
| `docs/plans/2-pr-d-users-pure-hummingbird.md`         | 本 Plan                                                                                       | 新規        |
| `db/queries/users.sql`                                | List / ListIncludingInactive / Search / SearchIncludingInactive / Get / Create / Update / SoftDelete / Restore (9 クエリ) | 新規 |
| `internal/repository/users.sql.go`                    | sqlc 生成                                                                                     | 新規 (生成) |
| `internal/handler/web/users.go`                       | 8 ハンドラ + decode / validate / lookup / showWithFlash / resolvePinned 一式                  | 新規        |
| `internal/handler/web/users_test.go`                  | 一覧 + 検索 + 新規フォーム + 権限 (RED 1 本目)                                                | 新規        |
| `internal/handler/web/users_crud_test.go`             | Create / Show / Update / SoftDelete / Restore / UNIQUE 違反 / FK 違反 / pinned department (RED 2 本目) | 新規 |
| `internal/view/users/list.templ`                      | 検索 + include_inactive + テーブル + 「新規」ボタン                                            | 新規        |
| `internal/view/users/form.templ`                      | new / edit 兼用 (employee_code / username / name / email / department_id select)              | 新規        |
| `internal/view/users/show.templ`                      | 詳細 + 所属部署リンク + delete/restore ボタン                                                  | 新規        |
| `internal/view/users/helpers.go`                      | `itoa` / `canEdit` / `formatDate` / `formatDateTime` / `sourceLabel` / `departmentLabel`      | 新規        |
| `internal/view/users/*_templ.go`                      | templ 生成物                                                                                  | 新規 (生成) |
| `internal/view/layout/base.templ`                     | Nav に `/users` リンク追加                                                                    | 編集        |
| `internal/handler/web/web.go`                         | `RegisterRoutes` に users ルート群追加 (新規定数追加なし)                                      | 編集        |

`sqlc.yaml` / `internal/handler/router.go` は変更不要。

## SQL クエリ詳細 (`db/queries/users.sql`)

departments と同様に SELECT 列は全カラム明示。

```sql
-- name: ListUsers :many
SELECT
  id, employee_code, username, name, email, department_id,
  deactivated_at, source, source_dn, ad_modified_at, last_synced_at,
  created_at, updated_at
FROM users
WHERE deactivated_at IS NULL
ORDER BY employee_code
LIMIT 200;

-- name: ListUsersIncludingInactive :many
SELECT
  id, employee_code, username, name, email, department_id,
  deactivated_at, source, source_dn, ad_modified_at, last_synced_at,
  created_at, updated_at
FROM users
ORDER BY employee_code
LIMIT 200;

-- name: SearchUsers :many
SELECT
  id, employee_code, username, name, email, department_id,
  deactivated_at, source, source_dn, ad_modified_at, last_synced_at,
  created_at, updated_at
FROM users
WHERE deactivated_at IS NULL
  AND (employee_code LIKE ?1 OR username LIKE ?1 OR name LIKE ?1 OR email LIKE ?1)
ORDER BY employee_code
LIMIT 200;

-- name: SearchUsersIncludingInactive :many
SELECT
  id, employee_code, username, name, email, department_id,
  deactivated_at, source, source_dn, ad_modified_at, last_synced_at,
  created_at, updated_at
FROM users
WHERE employee_code LIKE ?1 OR username LIKE ?1 OR name LIKE ?1 OR email LIKE ?1
ORDER BY employee_code
LIMIT 200;

-- name: GetUser :one
SELECT
  id, employee_code, username, name, email, department_id,
  deactivated_at, source, source_dn, ad_modified_at, last_synced_at,
  created_at, updated_at
FROM users
WHERE id = ?
LIMIT 1;

-- name: CreateUser :one
INSERT INTO users (
  employee_code, username, name, email, department_id, source
) VALUES (
  ?, ?, ?, ?, ?, 'manual'
)
RETURNING
  id, employee_code, username, name, email, department_id,
  deactivated_at, source, source_dn, ad_modified_at, last_synced_at,
  created_at, updated_at;

-- name: UpdateUser :one
UPDATE users
SET
  employee_code = ?,
  username = ?,
  name = ?,
  email = ?,
  department_id = ?,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING
  id, employee_code, username, name, email, department_id,
  deactivated_at, source, source_dn, ad_modified_at, last_synced_at,
  created_at, updated_at;
-- 注: deactivated_at は touch しない (delete/restore で別 SQL)

-- name: SoftDeleteUser :execrows
UPDATE users
SET
  deactivated_at = CURRENT_TIMESTAMP,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND deactivated_at IS NULL;

-- name: RestoreUser :execrows
UPDATE users
SET
  deactivated_at = NULL,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND deactivated_at IS NOT NULL;
```

department select 用は **既存の `ListActiveDepartments`** をそのまま再利用する (`db/queries/departments.sql:114-130`)。`ListActiveDepartmentsExceptID` は自己参照用なので users では使わない。

## ハンドラ層の構造

### `internal/handler/web/web.go` への追加 (擬似)

```go
func RegisterRoutes(r chi.Router, deps Deps) {
    v := &vendorHandlers{db: deps.DB, logger: deps.Logger}
    p := &productHandlers{db: deps.DB, logger: deps.Logger}
    d := &departmentHandlers{db: deps.DB, logger: deps.Logger}
    u := &userHandlers{db: deps.DB, logger: deps.Logger}

    // 既存 vendors / products / departments グループはそのまま

    r.With(mw.RequireRole(departmentViewers...)).Group(func(r chi.Router) {
        r.Get("/departments", d.list)
        r.Get("/departments/{id}", d.show)
        r.Get("/users", u.list)
        r.Get("/users/{id}", u.show)
    })
    r.With(mw.RequireRole(editors...)).Group(func(r chi.Router) {
        // ... departments 既存
        r.Get("/users/new", u.newForm)
        r.Post("/users", u.create)
        r.Get("/users/{id}/edit", u.editForm)
        r.Post("/users/{id}", u.update)
        r.Post("/users/{id}/delete", u.delete)
        r.Post("/users/{id}/restore", u.restore)
    })
}
```

### `users.go` のハンドラ列 (シグネチャのみ)

```go
type userHandlers struct {
    db     *sql.DB
    logger *slog.Logger
}

func (h *userHandlers) list(w, r)       // ?q= と ?include_inactive=1 を 4 通り分岐
func (h *userHandlers) newForm(w, r)    // ListActiveDepartments を select に
func (h *userHandlers) create(w, r)     // decode → CreateUser → 303 /users/:id、UNIQUE / FK 違反分岐
func (h *userHandlers) show(w, r)       // GetUser + lookupDepartmentForUser
func (h *userHandlers) editForm(w, r)   // GetUser + ListActiveDepartments + 廃止 department を pinned に
func (h *userHandlers) update(w, r)     // decode → UpdateUser、UNIQUE / FK / not_found 判定
func (h *userHandlers) delete(w, r)     // SoftDeleteUser → affected==0 で 409 + showWithFlash
func (h *userHandlers) restore(w, r)    // RestoreUser → affected==0 で 409 + showWithFlash

// ヘルパ群 (users.go 内)
func decodeUserForm(r) (FormInput, userParsed, map[string]string)
func formInputFromUser(u repository.User) FormInput
func (h *userHandlers) showWithFlash(w, r, id, status, flash)
func (h *userHandlers) renderForm(w, r, status, props)
func resolvePinnedDepartmentForUser(r, q, parents, deptID) (*Department, error)
func lookupDepartmentForUser(r, q, id) (*Department, error)  // departments の lookupDepartment と同型を複製
var employeeCodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
func validateEmployeeCode(s string) string   // 必須、1〜64、正規表現
func validateUsername(s string) string       // 任意、1〜128
func validateUserName(s string) string       // 必須、1〜128
func validateEmail(s string) string          // 任意、@ を含む
```

`userParsed` 構造体:

```go
type userParsed struct {
    EmployeeCode string
    Username     *string  // nilIfEmpty(in.Username)
    Name         string
    Email        *string  // nilIfEmpty(in.Email)
    DepartmentID *int64   // parseInt64Opt(in.DepartmentID)
}
```

`FormInput` / `FormProps` / `ShowProps` (`internal/view/users/`):

```go
type FormInput struct {
    EmployeeCode string
    Username     string
    Name         string
    Email        string
    DepartmentID string
}

type FormProps struct {
    Action        string
    Title         string
    Submit        string
    Input         FormInput
    Errors        map[string]string
    Departments   []repository.Department
    PinnedOption  *repository.Department  // 単一 (parent/successor の 2 つあった departments と違う)
}

type ShowProps struct {
    User       repository.User
    Department *repository.Department  // ラベルは departmentLabel() で生成
    Flash      string
}
```

`departmentLabel(d repository.Department) string` (`internal/view/users/helpers.go`):

```go
// 廃止済みなら "営業部 (〜2026-04-01)"、現役なら "営業部"。
// list の所属部署列、show の所属部署リンクテキスト、form の select option
// で共通利用。
func departmentLabel(d repository.Department) string {
    if d.ValidTo != nil {
        return d.Name + " (〜" + d.ValidTo.Format("2006-01-02") + ")"
    }
    return d.Name
}
```

`formatDateTime(*time.Time) string` (`deactivated_at` 表示用、`YYYY-MM-DD HH:MM`)。

## コミット列 (TDD サイクル)

ブランチ: `feat/phase2-pr-d-users` (CLAUDE.md「最初のコミットを Plan ファイルにする」)

| #  | コミット件名                                                                            | サイクル  |
| -- | --------------------------------------------------------------------------------------- | --------- |
| 1  | `docs(plans): フェーズ 2 PR-D users CRUD の実装プラン`                                  | —         |
| 2  | `feat(db/queries): users の CRUD + 論理削除 SQL を追加`                                 | —         |
| 3  | `test(handler/web/users): 一覧・新規フォーム描画 + 権限 (RED)`                          | RED       |
| 4  | `feat(handler/web/users): List / NewForm ハンドラ + templ + 配線`                       | GREEN     |
| 5  | `test(handler/web/users): Create / Show / Update + UNIQUE 違反 (RED)`                   | RED       |
| 6  | `feat(handler/web/users): Create / Show / EditForm / Update ハンドラ`                   | GREEN     |
| 7  | `test(handler/web/users): 論理削除 / 復活 / 二重削除 409 (RED)`                         | RED       |
| 8  | `feat(handler/web/users): SoftDelete / Restore ハンドラ`                                | GREEN     |
| 9  | `test(handler/web/users): include_inactive フィルタ + 退職表示 (RED)`                   | RED       |
| 10 | `feat(handler/web/users): include_inactive 分岐と退職行スタイル`                        | GREEN     |
| 11 | `test+feat(handler/web/users): department_id PinnedOption + 廃止部署ラベル併記`          | RED+GREEN |
| 12 | `feat(view/layout): Nav に /users リンク追加`                                           | —         |

PR-C と同様、コミット 11 は「廃止 department を pinned に残す」と「`name (〜YYYY-MM-DD)` 表記」の 2 つの細かい挙動を 1 コミットにまとめる。FK 違反 (400) のテストは Create / Update のどちらかに含める形で #5 / #6 に混ぜ込む (独立コミット化しない)。

## 受け入れ基準

- [ ] `make generate` 後 `git status` がクリーン (sqlc / templ 生成物含む)
- [ ] `make build` で全バイナリビルド可能
- [ ] `make test` / `go test -race ./...` 緑
- [ ] `make lint` 緑
- [ ] `make run` 起動後、以下が成功:
  - `curl -i -H 'X-User-Role: general_user' http://localhost:8180/users` → **403** (departments と同じパターン)
  - `curl -H 'X-User-Role: viewer' http://localhost:8180/users` → 200、「新規」ボタンは描画されない
  - `curl -H 'X-User-Role: license_manager' http://localhost:8180/users/new` → 200、employee_code / username / name / email / department select 描画
  - ユーザ作成 (`E001` / `田川太郎`、department_id は事前作成した部署) → show で「所属: 営業部」表示
  - `POST /users/1/delete` → 303 → show で「退職 (YYYY-MM-DD HH:MM)」と「復活」ボタン表示
  - `POST /users/1/delete` 再投: 409 + flash「このユーザは既に退職扱いです」
  - `POST /users/1/restore` → 303 → 「退職」表記が消える
  - `/users?include_inactive=1` で退職者が行表示される
- [ ] employee_code 重複: 同じコードで `POST /users` → 409 + flash + 入力値再表示
- [ ] 存在しない department_id で `POST /users` → 400 + flash「指定された部署は存在しません」
- [ ] 部署を廃止後、その部署所属ユーザの編集フォーム → select に「営業部 (〜YYYY-MM-DD)」が pinned 表示
- [ ] CSRF トークン無し POST が 403 になることを 1 ハンドラで確認
- [ ] Nav に `/users` リンクが出る
- [ ] AD source 制御 / app_users 自動生成 / 退職者ダッシュボードは本 PR で実装していないことを PR 本文に明記

## 動作検証手順

1. `nix develop`
2. `make generate` (sqlc + templ)
3. `make build`
4. `make migrate-up` (`./data/app.db` 初期化)
5. `make run`
6. 別端末で:

   ```sh
   # 事前に部署を 1 件作る
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d 'code=DEPT001&name=営業部&_csrf=dummy-csrf-token' \
        http://localhost:8180/departments

   # 一覧 (空)
   curl -i -H 'X-User-Role: viewer' http://localhost:8180/users

   # 新規作成
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d 'employee_code=E001&name=田川太郎&email=tagawa@example.com&department_id=1&_csrf=dummy-csrf-token' \
        http://localhost:8180/users

   # 退職処理
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d '_csrf=dummy-csrf-token' \
        http://localhost:8180/users/1/delete

   # 二重 delete → 409
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d '_csrf=dummy-csrf-token' \
        http://localhost:8180/users/1/delete

   # 退職者表示
   curl -i -H 'X-User-Role: viewer' \
        'http://localhost:8180/users?include_inactive=1'

   # 復活
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d '_csrf=dummy-csrf-token' \
        http://localhost:8180/users/1/restore

   # 部署廃止後の編集フォームに pinned option が残ること
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d '_csrf=dummy-csrf-token' \
        http://localhost:8180/departments/1/delete
   curl -i -H 'X-User-Role: license_manager' \
        http://localhost:8180/users/1/edit | grep '(〜'

   # general_user は 403
   curl -i -H 'X-User-Role: general_user' http://localhost:8180/users
   ```

7. ブラウザで `/users` を開き、Nav リンク・`include_inactive` チェックボックス・退職者の淡色表示を目視
8. `make test` で in-memory sqlite ベースの統合テストが完走

## PR-E 以降への引き継ぎ

- 論理削除 + 復活パターン (`deactivated_at` / `retired_at` 等、列名違いの DATETIME) を **2 度目** 適用したことで雛形が安定。PR-E devices (`retired_at`) は users.go と departments.go の双方を参照しつつ、users 側に近い形 (DATETIME + `CURRENT_TIMESTAMP`) で書ける
- `?include_inactive=1` フィルタ命名も users で 2 度目。devices でそのまま展開
- `departmentLabel(d)` の「廃止部署を `(〜YYYY-MM-DD)` 表記で残す」パターンは PR-E devices (`department_id` を持つ) でも同じ helper を移植可能。3 度目で `internal/view/common/` 等へ抽象化を検討
- `PinnedOption` 単一 ID 版 (`resolvePinnedDepartmentForUser`) と複数 ID 版 (`resolvePinnedDepartments`) の 2 形が出揃った。devices の `primary_user_id` でも単一 ID 版が必要になるので、3 度目で抽象化候補
- `validateEmployeeCode` / `validateDepartmentCode` の 2 種が出揃った。devices の `asset_code` で 3 度目を書く時点で `validateAsciiCode(field, max, val)` 等への共通化を検討
- AD 同期 PR が来た時点で `source='ad'` のレコード編集禁止を users と departments の両方の `update` / `delete` / `restore` に 1 行ずつ追加する形で導入可能。本 PR では仕込まない
- show 画面の「関連リソース」セクションは本 PR では空 (所属部署のみ)。後続で `app_users` / `user_assignments` / `devices.primary_user_id` の対応 PR が出るたびに show を拡張する別 PR が立つ想定
- `/admin/app-users` (system_admin 専用) は本 PR で URL も予約しない。app_users PR で新規導入
