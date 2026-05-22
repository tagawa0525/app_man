# フェーズ 2 PR-E — devices CRUD

## Context

フェーズ 2 PR-D (`docs/plans/2-pr-d-users-pure-hummingbird.md`、`477fa62` でマージ済) で users CRUD が確立し、「nullable FK + 論理削除 (DATETIME + `CURRENT_TIMESTAMP`) + 検索 4 通り + PinnedOption + showWithFlash」の雛形が完成した。本 PR-E はフェーズ 2「マスタ系」の **最後のテーブル** である devices を同型で実装し、最終 2 コミットで **3 度目の登場となる共通要素を抽象化** する。

雛形は users 実装 (`internal/handler/web/users.go` 607 行 + `db/queries/users.sql` 172 行) を一次写経元とする。`internal/repository/models.go:102-112` には `Device` 構造体が sqlc 生成済 (`db/migrations/000001_master.up.sql:80-90` 由来) なので、`db/queries/devices.sql` を書いて `make generate` を回すだけで repository は整う。

devices 固有の差分:

- `retired_at` は **DATETIME** (users の `deactivated_at` と同型)。SoftDelete SQL は `CURRENT_TIMESTAMP` を立てる
- `hostname` / `primary_user_id` / `department_id` は **nullable**
- **`source` カラムが無い** (`SKYSEA` 由来情報は `last_seen_at` の有無で代替する設計、要件書 §3 p.358-362)。INSERT で `source = 'manual'` を入れる必要なし、`sourceLabel` も使わない
- **`last_seen_at` カラム** — SKYSEA バッチ専用の read-only 列。show でのみ `formatDateTime` で表示。list / form では一切触らず、INSERT/UPDATE の SET 句にも含めない
- 検索は **2 カラム OR** (`asset_code` / `hostname`)
- FK は **2 種類同時** (`primary_user_id` + `department_id`)。select も pinned も 2 系統
- 退職 user が主利用者の場合の pinned 表記は「田川太郎 (退職)」(廃止部署の `(〜YYYY-MM-DD)` とは別書式)
- 要件書 §6.1 (`docs/specs/02_要件定義.md:1116`) で `/devices` は viewer 以上 → `departmentViewers` を流用
- 論理削除 URL は **`/devices/{id}/retire`** (退役) と命名。`delete` は使わない (退職/退役の語彙統一)

SKYSEA 取込み連携・`installations` / `device_assignments` 表示・自動退役判定はすべて別 PR。

## 対象スコープ

### 画面と URL

| URL                          | メソッド | ロール                | 用途                                                       |
| ---------------------------- | -------- | --------------------- | ---------------------------------------------------------- |
| `/devices`                   | GET      | viewer 以上 (※)       | 一覧 + 検索 (`?q=`) + 退役表示 (`?include_inactive=1`)     |
| `/devices/new`               | GET      | license_manager 以上  | 新規フォーム                                               |
| `/devices`                   | POST     | license_manager 以上  | 作成                                                       |
| `/devices/{id}`              | GET      | viewer 以上           | 詳細 (主利用者 + 所属部署 + `last_seen_at`)                |
| `/devices/{id}/edit`         | GET      | license_manager 以上  | 編集フォーム                                               |
| `/devices/{id}`              | POST     | license_manager 以上  | 更新                                                       |
| `/devices/{id}/retire`       | POST     | license_manager 以上  | 退役 (`retired_at = CURRENT_TIMESTAMP`)                    |
| `/devices/{id}/restore`      | POST     | license_manager 以上  | 復活 (`retired_at = NULL`)                                 |

(※) `departmentViewers` (viewer / license_manager / department_security_admin / system_admin)。general_user は閲覧不可。

### スコープ外 (別 PR 送り)

- SKYSEA 取込み (`appmgr-import-skysea`) と `last_seen_at` 自動更新
- `installations` / `device_assignments` の show 関連表示
- `last_seen_at` 閾値判定による自動退役 / 未確認デバイスダッシュボード
- 物理削除エンドポイント
- HTMX 部分更新 (フルページリロード)
- ページネーション (`listLimit = 200` で打ち切り、超過時バナー)
- `source` カラム後付けマイグレーション (SKYSEA 取込み PR と同時に判断)

## 主要決定

| 項目                              | 決定                                                                                                                                                                              |
| --------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 削除方針                          | `UPDATE devices SET retired_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND retired_at IS NULL`。`:execrows` 0 行 → 409 / 404                              |
| 復活操作                          | `UPDATE devices SET retired_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND retired_at IS NOT NULL`。`:execrows` 0 行 → 409                                            |
| URL 命名                          | 論理削除は `/devices/{id}/retire`。ハンドラ名も `retire` / `restore` (users の `delete` / `restore` と語彙差別化)                                                                  |
| 一覧フィルタ                      | 既定 `retired_at IS NULL`。`?include_inactive=1` で全件。検索 `?q=foo` と組合せ、4 つの SQL を呼び分け                                                                              |
| 検索対象                          | `asset_code` / `hostname` の 2 カラム OR LIKE。nullable hostname への LIKE は NULL に対し false (検索結果から落ちる挙動を受容)                                                       |
| 退役端末の視認性                  | list / show で「退役 (YYYY-MM-DD HH:MM)」表示。一覧行に `class="row-inactive"`                                                                                                       |
| asset_code バリデーション         | 必須、1〜64 文字、`^[A-Za-z0-9_-]+$`。**3 度目のため最終コミットで `validateAsciiCode` に共通化**                                                                                    |
| hostname バリデーション           | 任意 (空欄 → NULL)、1〜255 文字 (Windows NetBIOS 15 文字を超える FQDN を考慮)                                                                                                       |
| primary_user_id                   | 任意 FK。`parseInt64Opt` で `*int64` 化。存在しない ID 投入は `isForeignKeyErr` で検知し 400 + flash「指定されたユーザは存在しません」                                                |
| department_id                     | 任意 FK。同上、flash「指定された部署は存在しません」                                                                                                                                |
| asset_code UNIQUE 違反            | 409 + flash「資産コードが重複しています」 + フォーム再表示                                                                                                                          |
| 廃止部署表記                      | list 所属列 / show 所属リンク / form select で `name (〜YYYY-MM-DD)`。**3 度目のため最終コミットで `common.DepartmentLabel` に共通化**                                              |
| 退職 user 表記                    | list 主利用者列 / show 主利用者リンク / form select で `田川太郎 (退職)`。**初登場のため `internal/view/devices/helpers.go` 内に `userLabel` として配置 (共通化対象外)**             |
| PinnedOption                      | 廃止部署: 既存 `resolvePinnedDepartmentForUser` を最終コミットで `resolvePinnedDepartment` に共通化。退職 user: 新規 `resolvePinnedUser` を devices.go 内に定義 (初登場)             |
| `last_seen_at`                    | INSERT / UPDATE の SET 句に含めない。show のみで read-only 表示 (NULL は「(未確認)」)                                                                                                |
| source カラム                     | **存在しない**。`sourceLabel` 呼び出し無し、INSERT 文に `source` 列を入れない                                                                                                        |
| 関連表示 (show)                   | 主利用者リンク + 所属部署リンク + `last_seen_at`。`installations` / `device_assignments` は別 PR                                                                                    |
| 権限分離                          | 閲覧 `departmentViewers` / 編集 `editors` (新規定数追加なし)                                                                                                                        |
| Nav                               | `internal/view/layout/base.templ` の Nav に `/devices` リンクを追加。出し分けはしない (押下時 403)                                                                                  |
| 並び順                            | list: `asset_code ASC`                                                                                                                                                            |

## 利用可能な PR-B / PR-C / PR-D 資産

| ファイル                                                       | 流用するもの                                                                                                                  |
| -------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| `internal/handler/web/web.go` の `departmentViewers` / `editors` | 閲覧 / 編集権限の定数をそのまま流用 (新規定数追加なし)                                                                        |
| `internal/handler/web/vendors.go` 末尾の共通ヘルパ群             | `parseInt64Param` / `nilIfEmpty` / `derefString` / `likePattern` / `isUniqueConstraintErr` / `isForeignKeyErr` / `listLimit` |
| `internal/handler/web/departments.go` の `parseInt64Opt`        | nullable FK / 整数のフォーム入力パース                                                                                        |
| `internal/handler/web/users.go` の `showWithFlash` パターン      | devices 版を `devices.go` 内に複製                                                                                            |
| `internal/handler/web/users.go` の `resolvePinnedDepartmentForUser` | コミット 11 で `resolvePinnedDepartmentForDevice` として複製 → 最終コミットで `resolvePinnedDepartment` に統合                  |
| `internal/handler/web/users.go` の `lookupDepartmentForUser`    | 同型を `lookupDepartmentForDevice` として複製 (lookup は単純なので個別保持)                                                    |
| `internal/handler/web/users.go` の `buildUserListItems`         | `buildDeviceListItems` の雛形 (今回は user + department の 2 種 lookup)                                                       |
| `internal/handler/handlertest/handlertest.go`                  | `NewTestDB` / `NewRequest` / `PostForm` / `AssertStatus` / `AssertContains` / `AssertRedirect`                                |
| `internal/handler/web/vendors_test.go` の `newWebRouter`        | `devices_test.go` でもそのまま呼ぶ                                                                                            |
| `internal/view/layout/base.templ`                              | Base レイアウト + Flash + CSRFInput + Nav                                                                                     |
| `internal/view/users/{list,form,show,helpers}` 4 ファイル      | テンプレ構成と select / pinned option / inline form パターンを写経                                                            |
| `db/queries/departments.sql` の `ListActiveDepartments` (既存) | devices フォームの department select に **そのまま流用**                                                                       |
| `db/queries/users.sql`                                          | 本 PR で `ListActiveUsers :many` を追加 (退職者除外 + `employee_code ASC` + LIMIT 200)                                          |

## ファイル構成

| パス                                                  | 概要                                                                                                                       | 区分        |
| ----------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- | ----------- |
| `docs/plans/proud-baking-wind.md`                     | 本 Plan                                                                                                                    | 新規        |
| `db/queries/devices.sql`                              | List / ListIncludingInactive / Search / SearchIncludingInactive / Get / Create / Update / SoftDelete / Restore (9 クエリ) | 新規        |
| `db/queries/users.sql`                                | `ListActiveUsers :many` を追加 (1 クエリ)                                                                                  | 編集        |
| `internal/repository/devices.sql.go`                  | sqlc 生成                                                                                                                  | 新規 (生成) |
| `internal/repository/users.sql.go`                    | sqlc 再生成 (`ListActiveUsers` 追加分)                                                                                    | 編集 (生成) |
| `internal/handler/web/devices.go`                     | 8 ハンドラ + decode / validate / lookup / showWithFlash / resolvePinnedUser / buildDeviceListItems                          | 新規        |
| `internal/handler/web/devices_test.go`                | 一覧 + 検索 + 新規フォーム + 権限 + last_seen_at 表示 (RED)                                                                  | 新規        |
| `internal/handler/web/devices_crud_test.go`           | Create / Show / Update / Retire / Restore + UNIQUE / FK 違反 + 2 系統 PinnedOption (RED)                                    | 新規        |
| `internal/view/devices/list.templ`                    | 検索 + include_inactive + テーブル + 「新規」ボタン                                                                          | 新規        |
| `internal/view/devices/form.templ`                    | new / edit 兼用 (asset_code / hostname / primary_user select / department select)                                          | 新規        |
| `internal/view/devices/show.templ`                    | 詳細 + 主利用者リンク + 所属部署リンク + last_seen_at + retire/restore ボタン                                                 | 新規        |
| `internal/view/devices/helpers.go`                    | `itoa` / `canEdit` / `formatDateTime` / `userLabel` / `derefString`                                                        | 新規        |
| `internal/view/devices/*_templ.go`                    | templ 生成物                                                                                                                | 新規 (生成) |
| `internal/view/layout/base.templ`                     | Nav に `/devices` リンク追加                                                                                                | 編集        |
| `internal/handler/web/web.go`                         | `RegisterRoutes` に devices ルート群追加 (新規定数追加なし)                                                                  | 編集        |
| **最終 2 コミットで以下を追加・編集**                  |                                                                                                                            |             |
| `internal/view/common/department_label.go`            | **3 度目の抽象化**。`DepartmentLabel(d repository.Department) string` を export                                            | 新規        |
| `internal/view/users/helpers.go`                      | `departmentLabel` を削除                                                                                                  | 編集        |
| `internal/view/users/{list,form,show}.templ`         | `departmentLabel(...)` 呼び出しを `common.DepartmentLabel(...)` に置換                                                      | 編集        |
| `internal/view/devices/helpers.go`                    | `departmentLabel` を削除 (コミット 4 で一時的に複製した分)                                                                  | 編集        |
| `internal/view/devices/{list,form,show}.templ`       | 同様に `common.DepartmentLabel` 呼び出しに置換                                                                              | 編集        |
| `internal/handler/web/helpers.go` (新設)              | `resolvePinnedDepartment(r, q, depts, id)` + `validateAsciiCode(label, max, s)` の共通ヘルパ                                | 新規        |
| `internal/handler/web/users.go`                       | `validateEmployeeCode` / `employeeCodeRe` 削除、`resolvePinnedDepartmentForUser` 削除。呼び出しを共通版に置換                | 編集        |
| `internal/handler/web/departments.go`                 | `validateDepartmentCode` / `deptCodeRe` 削除、呼び出しを `validateAsciiCode("部署コード", 64, s)` に置換                     | 編集        |
| `internal/handler/web/devices.go`                     | `validateAssetCode` (コミット 6 で導入) 削除、`resolvePinnedDepartmentForDevice` (コミット 11 で導入) 削除。呼び出しを共通版に置換 | 編集 |

`sqlc.yaml` / `internal/handler/router.go` / マイグレーションは変更不要。

## SQL クエリ詳細

### `db/queries/devices.sql` (新規 9 本)

```sql
-- name: ListDevices :many
SELECT
  id, asset_code, hostname, primary_user_id, department_id,
  retired_at, last_seen_at, created_at, updated_at
FROM devices
WHERE retired_at IS NULL
ORDER BY asset_code
LIMIT 200;

-- name: ListDevicesIncludingInactive :many
SELECT
  id, asset_code, hostname, primary_user_id, department_id,
  retired_at, last_seen_at, created_at, updated_at
FROM devices
ORDER BY asset_code
LIMIT 200;

-- name: SearchDevices :many
SELECT
  id, asset_code, hostname, primary_user_id, department_id,
  retired_at, last_seen_at, created_at, updated_at
FROM devices
WHERE retired_at IS NULL
  AND (asset_code LIKE ?1 OR hostname LIKE ?1)
ORDER BY asset_code
LIMIT 200;

-- name: SearchDevicesIncludingInactive :many
SELECT
  id, asset_code, hostname, primary_user_id, department_id,
  retired_at, last_seen_at, created_at, updated_at
FROM devices
WHERE asset_code LIKE ?1 OR hostname LIKE ?1
ORDER BY asset_code
LIMIT 200;

-- name: GetDevice :one
SELECT
  id, asset_code, hostname, primary_user_id, department_id,
  retired_at, last_seen_at, created_at, updated_at
FROM devices
WHERE id = ?
LIMIT 1;

-- name: CreateDevice :one
INSERT INTO devices (
  asset_code, hostname, primary_user_id, department_id
) VALUES (
  ?, ?, ?, ?
)
RETURNING
  id, asset_code, hostname, primary_user_id, department_id,
  retired_at, last_seen_at, created_at, updated_at;
-- 注: source カラムは存在しない。last_seen_at は INSERT 時 NULL 初期化

-- name: UpdateDevice :one
UPDATE devices
SET
  asset_code = ?,
  hostname = ?,
  primary_user_id = ?,
  department_id = ?,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING
  id, asset_code, hostname, primary_user_id, department_id,
  retired_at, last_seen_at, created_at, updated_at;
-- 注: retired_at は retire / restore SQL で別管理、last_seen_at は SKYSEA バッチ専用

-- name: SoftDeleteDevice :execrows
UPDATE devices
SET
  retired_at = CURRENT_TIMESTAMP,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND retired_at IS NULL;

-- name: RestoreDevice :execrows
UPDATE devices
SET
  retired_at = NULL,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND retired_at IS NOT NULL;
```

### `db/queries/users.sql` への追加 (1 本)

```sql
-- name: ListActiveUsers :many
SELECT
  id, employee_code, username, name, email, department_id,
  deactivated_at, source, source_dn, ad_modified_at, last_synced_at,
  created_at, updated_at
FROM users
WHERE deactivated_at IS NULL
ORDER BY employee_code
LIMIT 200;
```

department select は既存の `ListActiveDepartments` (`db/queries/departments.sql:114-` 付近) をそのまま使う。

## ハンドラ層の構造

### `internal/handler/web/web.go` への追加 (擬似)

```go
e := &deviceHandlers{db: deps.DB, logger: deps.Logger}

r.With(mw.RequireRole(departmentViewers...)).Group(func(r chi.Router) {
    // ... departments / users 既存
    r.Get("/devices", e.list)
    r.Get("/devices/{id}", e.show)
})
r.With(mw.RequireRole(editors...)).Group(func(r chi.Router) {
    // ... vendors / products / departments / users 既存
    r.Get("/devices/new", e.newForm)
    r.Post("/devices", e.create)
    r.Get("/devices/{id}/edit", e.editForm)
    r.Post("/devices/{id}", e.update)
    r.Post("/devices/{id}/retire", e.retire)
    r.Post("/devices/{id}/restore", e.restore)
})
```

### `devices.go` のシグネチャ

```go
type deviceHandlers struct {
    db     *sql.DB
    logger *slog.Logger
}

func (h *deviceHandlers) list(w, r)       // ?q= × ?include_inactive=1 の 4 通り → buildDeviceListItems で user/dept 解決
func (h *deviceHandlers) newForm(w, r)    // ListActiveUsers + ListActiveDepartments を select に
func (h *deviceHandlers) create(w, r)     // decode → CreateDevice → 303 /devices/:id、UNIQUE / FK (2 種) 違反分岐
func (h *deviceHandlers) show(w, r)       // GetDevice + lookupUser + lookupDepartmentForDevice
func (h *deviceHandlers) editForm(w, r)   // GetDevice + 2 つの ListActive* + 2 つの pinned 解決
func (h *deviceHandlers) update(w, r)     // decode → UpdateDevice、UNIQUE / FK / not_found 判定
func (h *deviceHandlers) retire(w, r)     // SoftDeleteDevice → affected==0 で 409 + showWithFlash
func (h *deviceHandlers) restore(w, r)    // RestoreDevice → affected==0 で 409 + showWithFlash

// ヘルパ群 (devices.go 内)
func decodeDeviceForm(r) (devices.FormInput, deviceParsed, map[string]string)
func formInputFromDevice(d repository.Device) devices.FormInput
func (h *deviceHandlers) showWithFlash(w, r, id, status, flash)
func (h *deviceHandlers) renderForm(w, r, status, props devices.FormProps)
func resolvePinnedUser(r, q, users, id *int64) (*repository.User, error)         // 退職 user を select に残す
func lookupUser(r, q, id *int64) (*repository.User, error)                       // show 用、sql.ErrNoRows は nil
func lookupDepartmentForDevice(r, q, id *int64) (*repository.Department, error)  // 3 度目だが lookup は単純で個別保持
func buildDeviceListItems(r, q, devices) ([]devices.ListItem, error)             // user + dept キャッシュ付き lookup
func validateHostname(s string) string                                           // 任意、1〜255

// コミット 6 で導入し、コミット 14 で削除して共通版に置換するもの
var assetCodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
func validateAssetCode(s string) string                          // コミット 14 で削除
func resolvePinnedDepartmentForDevice(r, q, depts, id) (...)     // コミット 14 で削除
```

### 型定義 (`internal/view/devices/`)

```go
type FormInput struct {
    AssetCode     string
    Hostname      string
    PrimaryUserID string
    DepartmentID  string
}

type FormProps struct {
    Action           string
    Title            string
    Submit           string
    Input            FormInput
    Errors           map[string]string
    Users            []repository.User       // ListActiveUsers
    Departments      []repository.Department // ListActiveDepartments
    PinnedUser       *repository.User        // 退職 user の保持
    PinnedDepartment *repository.Department  // 廃止部署の保持
}

type ShowProps struct {
    Device     repository.Device
    User       *repository.User
    Department *repository.Department
    Flash      string
}

type ListItem struct {
    Device     repository.Device
    User       *repository.User
    Department *repository.Department
}

type deviceParsed struct {
    AssetCode     string
    Hostname      *string  // nilIfEmpty
    PrimaryUserID *int64   // parseInt64Opt
    DepartmentID  *int64   // parseInt64Opt
}
```

### `userLabel` (devices 専用、`internal/view/devices/helpers.go`)

```go
// userLabel は主利用者列 / show リンク / form select で共通利用するラベル。
// 退職済みなら "田川太郎 (退職)"、現役なら "田川太郎" を返す。
// users 側の departmentLabel と書式が違う (廃止日ではなく単に「(退職)」)
// ため、初登場の今は devices 内に閉じる (共通化対象外)。
func userLabel(u repository.User) string {
    if u.DeactivatedAt != nil {
        return u.Name + " (退職)"
    }
    return u.Name
}
```

## テストケース

### `devices_test.go` (RED 1 本目)

```text
TestDevices_List_GeneralUser_403
TestDevices_List_Viewer_200
TestDevices_List_ShowsExistingDevices
TestDevices_List_HidesNewButton_ForViewer
TestDevices_List_ShowsNewButton_ForLicenseManager
TestDevices_NewForm_GeneralUser_403
TestDevices_NewForm_LicenseManager_200
TestDevices_Search_MatchesAssetCode
TestDevices_Search_MatchesHostname
TestDevices_List_ExcludesInactive_ByDefault
TestDevices_List_IncludesInactive_WithQueryParam
TestDevices_List_IncludeInactiveCheckbox_Rendered
TestDevices_Search_ExcludesInactive_ByDefault
TestDevices_Show_RetiredBadgeShown
TestDevices_Show_LastSeenAtRendered
TestDevices_Show_LastSeenAtRendersUnknownWhenNull
```

### `devices_crud_test.go` (RED 2 本目以降)

```text
TestDevices_Create_RedirectsToShow
TestDevices_Create_StoresOptionalFields
TestDevices_Create_RejectsEmptyAssetCode
TestDevices_Create_RejectsInvalidAssetCodeFormat
TestDevices_Create_RejectsTooLongHostname
TestDevices_Create_RejectsDuplicateAssetCode
TestDevices_Create_RejectsNonExistentPrimaryUserID
TestDevices_Create_RejectsNonExistentDepartmentID
TestDevices_Create_GeneralUser_403
TestDevices_Show_RendersDetail
TestDevices_Show_404OnUnknownID
TestDevices_Show_LinksToPrimaryUser
TestDevices_Show_LinksToDepartment
TestDevices_EditForm_LicenseManager_200
TestDevices_EditForm_GeneralUser_403
TestDevices_EditForm_PinsRetiredUser
TestDevices_EditForm_PinsInactiveDepartment
TestDevices_Update_RewritesFields
TestDevices_Update_ClearsOptionalFieldsToNull
TestDevices_Update_RejectsDuplicateAssetCode
TestDevices_Retire_SetsRetiredAt
TestDevices_Retire_HidesFromDefaultList
TestDevices_Retire_NotFound_404
TestDevices_Retire_AlreadyRetired_409
TestDevices_Retire_GeneralUser_403
TestDevices_Restore_ClearsRetiredAt
TestDevices_Restore_AlreadyActive_409
TestDevices_Restore_GeneralUser_403
TestDevices_List_RetiredUserLabel
TestDevices_Show_RetiredUserLabel
TestDevices_List_RetiredDepartmentLabel
TestDevices_Show_RetiredDepartmentLabel
```

## コミット列 (TDD サイクル)

ブランチ: `feat/phase2-pr-e-devices`。CLAUDE.md の「最初のコミットを Plan ファイル」規約に従う。

| #  | コミット件名                                                                                       | サイクル          |
| -- | -------------------------------------------------------------------------------------------------- | ----------------- |
| 1  | `docs(plans): フェーズ 2 PR-E devices CRUD の実装プラン`                                           | —                 |
| 2  | `feat(db/queries): devices の CRUD + 論理削除 SQL を追加 (+ users.ListActiveUsers)`                | —                 |
| 3  | `test(handler/web/devices): 一覧・新規フォーム描画 + 権限 (RED)`                                   | RED               |
| 4  | `feat(handler/web/devices): List / NewForm ハンドラ + templ + 配線`                                | GREEN             |
| 5  | `test(handler/web/devices): Create / Show / Update + UNIQUE / FK 違反 (RED)`                       | RED               |
| 6  | `feat(handler/web/devices): Create / Show / EditForm / Update ハンドラ`                            | GREEN             |
| 7  | `test(handler/web/devices): 退役 / 復活 / 二重退役 409 (RED)`                                       | RED               |
| 8  | `feat(handler/web/devices): Retire / Restore ハンドラ`                                              | GREEN             |
| 9  | `test(handler/web/devices): include_inactive + 退役行スタイル + last_seen_at 表示 (RED)`            | RED               |
| 10 | `feat(handler/web/devices): include_inactive 分岐と退役行スタイル + last_seen_at show 描画`        | GREEN             |
| 11 | `test+feat(handler/web/devices): primary_user / department PinnedOption + 退職user / 廃止部署ラベル` | RED+GREEN         |
| 12 | `feat(view/layout): Nav に /devices リンク追加`                                                    | —                 |
| 13 | `refactor(view/common): departmentLabel を internal/view/common.DepartmentLabel に共通化`           | REFACTOR (3 度目) |
| 14 | `refactor(handler/web): resolvePinnedDepartment / validateAsciiCode を共通化`                       | REFACTOR (3 度目) |

コミット 11 は users PR-D と同様、「廃止 department / 退職 user を pinned に残す」と「label 表記」を 1 コミットにまとめる。FK 違反 (400) のテストは Create / Update に混ぜ込み (#5 / #6)、独立コミット化しない。

### コミット 13: `departmentLabel` 共通化

新規パッケージ `internal/view/common/` に純粋関数を定義:

```go
// internal/view/common/department_label.go
package common

import "github.com/tagawa0525/app_man/internal/repository"

// DepartmentLabel は所属部署列 / show リンク / form select 等で共通利用する
// 部署ラベル。廃止部署は "営業部 (〜2026-04-01)" のように廃止日を併記する。
// users (PR-D)・devices (PR-E) で 2 度複製したため、3 度目登場の本コミットで集約。
func DepartmentLabel(d repository.Department) string {
    if d.ValidTo != nil {
        return d.Name + " (〜" + d.ValidTo.Format("2006-01-02") + ")"
    }
    return d.Name
}
```

置換対象 (現状調査結果):

- `internal/view/users/helpers.go:50-57` の `departmentLabel` を削除
- `internal/view/users/list.templ:89`、`internal/view/users/show.templ:80`、`internal/view/users/form.templ:87,89,93` の呼び出し 5 箇所を `common.DepartmentLabel(...)` に置換
- コミット 4 で複製した `internal/view/devices/helpers.go` の `departmentLabel` と、コミット 4/11 の `internal/view/devices/{list,form,show}.templ` 内呼び出しも同様に置換
- 各テンプレに `import commonview "github.com/tagawa0525/app_man/internal/view/common"` を追加 (templ の import 構文に従う)
- `make generate` で `*_templ.go` を再生成

### コミット 14: `resolvePinnedDepartment` / `validateAsciiCode` 共通化

`internal/handler/web/helpers.go` を新設して 2 関数を定義:

```go
// resolvePinnedDepartment は depts (現役) に含まれていない参照先を 1 件 fetch する。
// 編集中レコードが廃止済み部署を指す場合に、その option を select に残すために使う。
// id が nil または既に depts に含まれる場合は nil。sql.ErrNoRows は nil 扱い。
//
// 単一 ID 版。departments.go の resolvePinnedDepartments (複数 ID 版、可変長引数)
// とは別関数として共存させる。
func resolvePinnedDepartment(r *http.Request, q *repository.Queries, depts []repository.Department, id *int64) (*repository.Department, error) {
    if id == nil {
        return nil, nil
    }
    for _, d := range depts {
        if d.ID == *id {
            return nil, nil
        }
    }
    d, err := q.GetDepartment(r.Context(), *id)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, nil
        }
        return nil, err
    }
    return &d, nil
}

var asciiCodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validateAsciiCode はコード系フィールド (employee_code / 部署 code / asset_code) の
// 共通検証。必須、1〜max 文字、英数 + - + _ のみ。label は日本語のフィールド名。
// users・departments・devices で 3 度登場した時点で集約 (CLAUDE.md「3 回ルール」)。
func validateAsciiCode(label string, max int, s string) string {
    if s == "" {
        return label + "は必須です"
    }
    if utf8.RuneCountInString(s) > max {
        return label + "は " + strconv.Itoa(max) + " 文字以内で入力してください"
    }
    if !asciiCodeRe.MatchString(s) {
        return label + "は英数・ハイフン・アンダースコアで入力してください"
    }
    return ""
}
```

置換対象 (現状調査結果):

- `internal/handler/web/users.go` の `validateEmployeeCode` / `employeeCodeRe` を削除し、呼び出しを `validateAsciiCode("従業員コード", 64, in.EmployeeCode)` に置換。`resolvePinnedDepartmentForUser` も削除し、呼び出しを `resolvePinnedDepartment(r, q, depts, u.DepartmentID)` に置換
- `internal/handler/web/departments.go:530-545` の `validateDepartmentCode` / `deptCodeRe` を削除し、呼び出し (`departments.go:441` 周辺) を `validateAsciiCode("部署コード", 64, in.Code)` に置換
- `internal/handler/web/devices.go` の `validateAssetCode` / `assetCodeRe` (コミット 6 で導入) と `resolvePinnedDepartmentForDevice` (コミット 11 で導入) を削除し、共通版呼び出しに置換

エラー文言の変化があるため、`*_crud_test.go` の `AssertContains` 期待値を同コミットで揃える。

## 受け入れ基準

- [ ] `make generate` 後 `git status` がクリーン (sqlc / templ 生成物含む)
- [ ] `make build` で全バイナリビルド可能
- [ ] `make test` / `go test -race ./...` 緑 (既存 users / departments テストも文言更新後に緑)
- [ ] `make lint` 緑
- [ ] `make run` 起動後、以下が成功:
  - `curl -i -H 'X-User-Role: general_user' http://localhost:8180/devices` → **403**
  - `curl -H 'X-User-Role: viewer' http://localhost:8180/devices` → 200、「新規」ボタンは描画されない
  - `curl -H 'X-User-Role: license_manager' http://localhost:8180/devices/new` → 200、asset_code / hostname / primary_user select / department select 描画
  - 端末作成 (`PC-001` / 事前作成済 user / dept) → show で主利用者リンクと所属部署リンクが描画
  - `POST /devices/1/retire` → 303 → show で「退役 (YYYY-MM-DD HH:MM)」と「復活」ボタン
  - `POST /devices/1/retire` 再投: 409 + flash「この端末は既に退役済みです」
  - `POST /devices/1/restore` → 303 → 「退役」表記が消える
  - `/devices?include_inactive=1` で退役端末が行表示
- [ ] asset_code 重複: 409 + flash + 入力値再表示
- [ ] 存在しない primary_user_id / department_id で POST → 400 + flash
- [ ] 部署を廃止後、その部署所属 device の編集フォーム → select に「営業部 (〜YYYY-MM-DD)」が pinned 表示
- [ ] user を退職後、その user を主利用者に持つ device の編集フォーム → select に「田川太郎 (退職)」が pinned 表示 + selected
- [ ] device show で `last_seen_at` が NULL のとき「(未確認)」、値があれば「YYYY-MM-DD HH:MM」が描画
- [ ] CSRF トークン無し POST が 403 になることを 1 ハンドラ (`/devices`) で確認
- [ ] Nav に `/devices` リンクが出る
- [ ] PR 本文に以下を明記:
  - SKYSEA 取込み / `last_seen_at` 自動更新は別 PR
  - `installations` / `device_assignments` 表示は別 PR
  - `last_seen_at` 閾値判定による自動退役は別 PR
  - 3 度目の抽象化として `internal/view/common.DepartmentLabel` / `resolvePinnedDepartment` / `validateAsciiCode` を新設し、users / departments の該当関数を置換
- [ ] フェーズ 2「マスタ系」(vendors / products / departments / users / devices) が CRUD として全て揃ったことを PR 本文の最後で宣言

## 動作検証手順

1. `nix develop`
2. `make generate` (sqlc + templ)
3. `make build`
4. `make migrate-up` (`./data/app.db` 初期化)
5. `make run`
6. 別端末で:

   ```sh
   # 事前に部署 + ユーザを 1 件ずつ作る
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d 'code=DEPT001&name=営業部&_csrf=dummy-csrf-token' \
        http://localhost:8180/departments

   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d 'employee_code=E001&name=田川太郎&department_id=1&_csrf=dummy-csrf-token' \
        http://localhost:8180/users

   # 一覧 (空)
   curl -i -H 'X-User-Role: viewer' http://localhost:8180/devices

   # 新規作成
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d 'asset_code=PC-001&hostname=tagawa-pc&primary_user_id=1&department_id=1&_csrf=dummy-csrf-token' \
        http://localhost:8180/devices

   # 詳細
   curl -i -H 'X-User-Role: viewer' http://localhost:8180/devices/1

   # 退役処理
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d '_csrf=dummy-csrf-token' \
        http://localhost:8180/devices/1/retire

   # 二重 retire → 409
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d '_csrf=dummy-csrf-token' \
        http://localhost:8180/devices/1/retire

   # 退役端末表示
   curl -i -H 'X-User-Role: viewer' \
        'http://localhost:8180/devices?include_inactive=1'

   # 復活
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d '_csrf=dummy-csrf-token' \
        http://localhost:8180/devices/1/restore

   # 退職 user の pinned 表示確認
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d '_csrf=dummy-csrf-token' \
        http://localhost:8180/users/1/delete
   curl -i -H 'X-User-Role: license_manager' \
        http://localhost:8180/devices/1/edit | grep '(退職)'

   # 廃止部署の pinned 表示確認
   curl -i -H 'X-User-Role: license_manager' -X POST \
        -H 'X-CSRF-Token: dummy-csrf-token' \
        -d '_csrf=dummy-csrf-token' \
        http://localhost:8180/departments/1/delete
   curl -i -H 'X-User-Role: license_manager' \
        http://localhost:8180/devices/1/edit | grep '(〜'

   # general_user は 403
   curl -i -H 'X-User-Role: general_user' http://localhost:8180/devices
   ```

7. ブラウザで `/devices` を開き、Nav リンク・`include_inactive` チェックボックス・退役端末の淡色表示・`last_seen_at` 表示を目視
8. `make test` で in-memory sqlite ベースの統合テストが完走

## フェーズ 3 以降への引き継ぎ

- **フェーズ 2「マスタ系」CRUD が本 PR で完成** (vendors / products / aliases / departments / users / devices)。フェーズ 3「利用許諾」(`department_product_approvals` / `approval_requests`) と「割当」(`user_assignments` / `device_assignments` / `licenses` / `installations`) は本 PR を土台に積む
- 論理削除 + 復活パターン (`deactivated_at` / `retired_at` / `valid_to`) が 3 表で揃った。`licenses.expires_at` や `*_assignments.revoked_at` も同パターンで書ける
- `?include_inactive=1` フィルタ命名は 3 度目。後続でそのまま展開
- `internal/view/common.DepartmentLabel` の抽象化が完了。今後 license owning_department / 棚卸し etc. で再利用
- `resolvePinnedDepartment` (単一 ID 版) + `resolvePinnedDepartments` (複数 ID 版) の 2 形が共存。`PinnedUser` 系は本 PR で `resolvePinnedUser` を 1 度目として devices 内に閉じている。`user_assignments` で 2 度目、3 度目登場時に共通化
- `validateAsciiCode(label, max, s)` が完成。`license_slug` / `vendor_order_no` 等の追加コード系入力でそのまま使える
- show 画面の「関連リソース」セクションは本 PR では `installations` / `device_assignments` 不在のまま (主利用者と所属部署のみ)。フェーズ 3 でこれらが入る都度 show を拡張する別 PR
- SKYSEA 取込み PR (`appmgr-import-skysea`) が `last_seen_at` を更新する。`source` カラムを後付けマイグレーションで追加するかは取込み PR と同時に判断 (本 PR では追加しない)
- AD 同期 PR が来た時点で `source='ad'` のユーザ編集禁止を users の `update` / `delete` / `restore` に追加する。本 PR では仕込まない
