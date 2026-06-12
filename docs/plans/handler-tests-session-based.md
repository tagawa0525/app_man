# Handler テスト群を session 駆動に書き換え (Phase 3 第 4 PR — 4b)

## Context

PR #18 (4a) で実 `AuthMiddleware` と `handlertest.AuthenticatedAs` を追加した。
既存 13 個の handler テストファイルは依然 `DummyAuthMiddleware` + `X-User-Role`
ヘッダで認可を駆動している。本 PR (4b) で全テストを session Cookie ベースに
切り替える。

本 PR の終了時点で:

- 13 個の test ファイルの `newWebRouter()` は **SessionMiddleware + AuthMiddleware** チェーンを使う
- 全 `handlertest.NewRequest / PostForm` 呼び出しが session ベースの新 helper に置換される
- 本番 router (`internal/handler/router.go`) は依然 `DummyAuthMiddleware` を使う (= 4c で切り替え)
- 本番側の `DummyAuthMiddleware` / `RoleCookieName` / `POST /__set_role` は残す (= 4c で削除)

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| 新 helper のシグネチャ | `AuthenticatedRequest(t, db, store, method, target, role, body)` / `AuthenticatedPostForm(t, db, store, target, role, values)` | 既存 helper のシグネチャ + db, store を頭に追加 |
| role="" の意味 | session Cookie を付けない (= 未認証) | 既存「ヘッダなし → general_user フォールバック」は実 AuthMiddleware では `/login` redirect なので意味が変わる。テスト側で role を明示する移行をする |
| newWebRouter の戻り値 | テストファイル毎に異なる現状を尊重しつつ、必要なら `*sql.DB` / `session.Store` も返す | 機械的書き換えに留め、共通 helper への抽出は別 PR (4c か Phase 4 で) |
| middleware チェーン | `SessionMiddleware` → `AuthMiddleware` → `CSRFMiddleware` の順 | 仕様書 §8.3 / §7.2 に沿う。実 AuthMiddleware が session を読むため SessionMiddleware が先 |
| AuthConfig.LoginURL | デフォルト `/login` のまま | テストはすべて web パッケージ内で同じ前提を使う |
| 既存 `handlertest.NewRequest` / `PostForm` | 本 PR では削除しない | 4c で本番 DummyAuth と一緒に削除予定。互換維持で 13 ファイルが順に移行する間も build が通る |
| set_role テスト | 廃止対象だが本 PR では keep | /__set_role のハンドラ自体は 4c で削除。本 PR では旧チェーン (DummyAuth) を使う set_role 専用の test 環境を維持 (= 本 PR では set_role_test.go だけ既存 DummyAuth 経由テストを残す) |
| url_validation_test | url 検証は middleware 経由しない汎用 utility テスト | newWebRouter 不要かもしれない → 確認して維持 / 削除を判断 |
| TDD | RED→GREEN は不要 (機械的移行)。ファイル毎の独立コミットで履歴を残す | 各コミットで `make test` が緑であることを担保 |

## 対象スコープ

### 範囲内 (移行する 13 ファイル)

- `internal/handler/handlertest/`: `AuthenticatedRequest` / `AuthenticatedPostForm` helper を追加
- `internal/handler/web/vendors_test.go` / `vendors_crud_test.go`
- `internal/handler/web/products_test.go` / `products_crud_test.go` / `aliases_test.go`
- `internal/handler/web/departments_test.go` / `departments_crud_test.go`
- `internal/handler/web/users_test.go` / `users_crud_test.go`
- `internal/handler/web/devices_test.go` / `devices_crud_test.go`
- `internal/handler/web/url_validation_test.go`

### 範囲外 (4c で扱う)

- `set_role_test.go` の book full 移行 (4c で /__set_role 自体と一緒に削除)
- 本番 `router.go` の DummyAuthMiddleware → AuthMiddleware 切替
- `DummyAuthMiddleware` / `RoleCookieName` / `AllRoles` (使用箇所削除後) の削除
- `internal/handler/middleware/auth_test.go` (DummyAuth のテスト) の削除
- `handlertest.NewRequest` / `PostForm` の削除
- 4c では Nav (`internal/view/layout/base.templ`) から /__set_role の role 切替セレクタも削除

## 内部設計

### 新 helper の実装

```go
// AuthenticatedRequest は AuthenticatedAs で作った session Cookie を
// 付けたリクエストを返す。role="" の場合は session を作らず未認証リクエストを返す
// (実 AuthMiddleware が /login に redirect する経路の検証用)。
func AuthenticatedRequest(t *testing.T, db *sql.DB, store session.Store,
    method, target string, role middleware.Role, body io.Reader) *http.Request

// AuthenticatedPostForm は CSRF token (DummyCSRFToken) を自動付与した
// application/x-www-form-urlencoded POST リクエストを返す。
// values に _csrf が含まれていればそちらを尊重する (既存 PostForm と同じ作法)。
func AuthenticatedPostForm(t *testing.T, db *sql.DB, store session.Store,
    target string, role middleware.Role, values url.Values) *http.Request
```

### newWebRouter の書き換えパターン

旧:

```go
func newWebRouter(t *testing.T) (http.Handler, *repository.Queries) {
    sqlDB := handlertest.NewTestDB(t)
    r := chi.NewRouter()
    r.Use(middleware.DummyAuthMiddleware)
    r.Use(middleware.CSRFMiddleware)
    web.RegisterRoutes(r, web.Deps{Logger: ..., DB: sqlDB, DevMode: true})
    return r, repository.New(sqlDB)
}
```

新:

```go
func newWebRouter(t *testing.T) (http.Handler, *sql.DB, session.Store, *repository.Queries) {
    sqlDB := handlertest.NewTestDB(t)
    store := session.NewSQLiteStore(sqlDB)
    r := chi.NewRouter()
    r.Use(middleware.SessionMiddleware(middleware.SessionConfig{
        Store:  store,
        MaxAge: time.Hour,
        Logger: slog.New(slog.DiscardHandler),
    }))
    r.Use(middleware.AuthMiddleware(middleware.AuthConfig{
        DB:     sqlDB,
        Logger: slog.New(slog.DiscardHandler),
    }))
    r.Use(middleware.CSRFMiddleware)
    web.RegisterRoutes(r, web.Deps{Logger: ..., DB: sqlDB, DevMode: true})
    return r, sqlDB, store, repository.New(sqlDB)
}
```

呼び出し側:

旧:

```go
r, _ := newWebRouter(t)
req := handlertest.NewRequest(t, http.MethodGet, "/vendors", middleware.RoleGeneralUser, nil)
```

新:

```go
r, db, store, _ := newWebRouter(t)
req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/vendors", middleware.RoleGeneralUser, nil)
```

## コミット順序

1. `docs(plans): handler テスト session 化の Plan ファイル`
2. `feat(handlertest): AuthenticatedRequest / AuthenticatedPostForm helper を追加`
3. `test(web): vendors_test / vendors_crud_test を session 化`
4. `test(web): products_test / products_crud_test / aliases_test を session 化`
5. `test(web): departments_test / departments_crud_test を session 化`
6. `test(web): users_test / users_crud_test を session 化`
7. `test(web): devices_test / devices_crud_test を session 化`
8. `test(web): url_validation_test を session 化 (or 不要なら no-op)`

各コミットで `make test` 緑を確認する。

## 受け入れ基準

- `make build` / `make test` / `go test -race ./...` / `make lint` 全緑
- 移行済テスト 13 個すべてが session Cookie 駆動 (X-User-Role ヘッダを **直接** 使わない、handlertest.NewRequest を **直接** 使わない)
- set_role_test.go は引き続き旧 DummyAuth テスト helper を使う (4c で削除予定)
- 本番 router.go は触らず、`appmgr-server` の挙動に変更なし

## 想定リスク

- **role="" のテスト振る舞い変化**: 既存テストで `role=""` を渡して general_user フォールバックを期待していた場合、新 helper では session Cookie 無し → 303 to /login になる。各ファイル移行時に挙動を読み替えて role 明示 or 303 期待に書き換える。`set_role_test.go` 以外で `role=""` を渡すパターンは事前 grep の結果 0 件
- **handler テスト件数が多い (devices_crud 31, users_crud 29, departments_crud 26 等)**: 機械的置換が大半だが見落としがあると CI 落ちる → ファイル毎のコミットで段階的に通す
