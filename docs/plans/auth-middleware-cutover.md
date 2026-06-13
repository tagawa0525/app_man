# 本番 router を実 AuthMiddleware に切替 + DummyAuth 廃止 (Phase 3 第 4 PR — 4c)

## Context

PR #18 (4a) で実 `AuthMiddleware` を新規追加し、PR #19 (4b) で 13 個の
handler テストを session 駆動に書き換えた。残るは本番 `router.go` の
DummyAuth → 実 AuthMiddleware 切替と、それに伴う legacy コードの一括削除。

本 PR の完了で「`DummyAuthMiddleware` / `RoleCookieName` / `roleFromCookie` /
`POST /__set_role` ハンドラ / 旧 `handlertest.NewRequest` / `PostForm`」が
コードベースから完全に消える。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| router.go の SessionMiddleware | nil チェックを外し必須化 | テストは 4b で全部 session 駆動になった、本番も session 必須 |
| router.go の AuthMiddleware | SessionMiddleware の直後に挟む。CSRFMiddleware より前 | 公開パス判定が先、CSRF より session/auth が優先 |
| `DummyAuthMiddleware` / `RoleCookieName` / `roleFromCookie` | 完全削除 | 代替は実 AuthMiddleware |
| `AllRoles()` / `IsValidRole()` | 維持 | `session_auth.go` の `pickHighestRole` と `cmd/create-app-user` がまだ使う |
| `POST /__set_role` ハンドラ (`set_role.go`) | 完全削除 | role 切替は dev のみの機能、実 Auth では不要 |
| `set_role_test.go` | 完全削除 | ハンドラ自体が消える |
| `internal/handler/web/Deps.DevMode` | 完全削除 | `/__set_role` 登録の唯一の用途 |
| `cmd/server/main.go` の `APP_MAN_DEV_MODE` 環境変数 | 削除 | 同上 |
| `handlertest.NewRequest` / `PostForm` | 完全削除 | 4b で全 caller が `AuthenticatedRequest` / `AuthenticatedPostForm` に移行済み |
| `handlertest_test.go` | NewRequest / PostForm のテストを削除 | helper 削除に合わせる |
| Nav (`view/layout/base.templ`) | role 切替セレクタ削除 + **Logout button 追加** | dev 用切替は廃止、ログアウト機能は本物が必要 |
| ログイン中ユーザ名の Nav 表示 | 本 PR では入れない | BaseProps への追加 + 全 handler の変更が必要、別 PR |
| `BaseProps` 変更 | 最小限 (role 切替の引数 csrfToken は維持。Logout は同じトークンで投げる) | 既存 handler の引き渡しを変えない |
| `auth.go` のパッケージドキュメント | DummyAuth 言及を削除 | コードと文言を揃える |
| `web/auth_test.go` (login テスト) | newAuthRouter の middleware チェーンを SessionMiddleware + 実 AuthMiddleware + CSRFMiddleware に変更 | 本番 router と同等チェーン |
| `LoginURL` のデフォルト | `/login` | AuthMiddleware のデフォルトと一致 |
| 受け入れ基準テスト | 本番 router を起動し `/healthz` 200、未認証 `/products` → 303 to /login、ログイン後 `/products` 200 のフロー | 統合検証 |

## 対象スコープ

### 範囲内 (削除)

- `internal/handler/middleware/auth.go`: `DummyAuthMiddleware` / `RoleCookieName` / `roleFromCookie` を削除 (`validRoles` は `IsValidRole` と `pickHighestRole` から参照されるため維持)
- `internal/handler/middleware/auth_test.go`: `TestDummyAuthMiddleware_*` 系テストを削除 (5 件)
- `internal/handler/web/set_role.go`: ファイル削除
- `internal/handler/web/set_role_test.go`: ファイル削除
- `internal/handler/handlertest/handlertest.go`: `NewRequest` / `PostForm` を削除
- `internal/handler/handlertest/handlertest_test.go`: 関連テスト削除

### 範囲内 (変更)

- `internal/handler/router.go`: `SessionMiddleware` 必須化、`AuthMiddleware` 追加、`DummyAuthMiddleware` 行削除、`Deps.DevMode` 削除
- `internal/handler/web/web.go`: `Deps.DevMode` 削除、setRole 登録ブロック削除
- `cmd/server/main.go`: `APP_MAN_DEV_MODE` 環境変数読み取り削除、`DevMode` 引数削除
- `internal/handler/web/auth_test.go`: `newAuthRouter` の middleware チェーンを本番同等に変更 (`DummyAuthMiddleware` → 実 `AuthMiddleware`)
- `internal/view/layout/base.templ`: role セレクタ form を削除、Logout button (POST /logout) を追加
- `internal/session/cookie.go`: コメントから `RoleCookieName` 言及を削除
- `internal/handler/middleware/middleware.go`: パッケージドキュメントを「ダミー認可」言及から「実 Auth」言及に更新

### 範囲外 (別 PR)

- Nav にログイン中ユーザ名を表示 (`BaseProps.Username` 追加 + 全 handler 変更)
- CSRFMiddleware の session-bound 化 (4d / Phase 3 第 5 PR)
- 部署別認可 (Phase 4 以降)

## 内部設計

### `router.go` の middleware チェーン

```go
r := chi.NewRouter()
r.Use(chimw.RequestID)
r.Use(recoverer(deps.Logger))
r.Use(middleware.SessionMiddleware(middleware.SessionConfig{
    Store:        deps.SessionStore,
    SecureCookie: deps.CookieSecure,
    MaxAge:       deps.SessionMaxAge,
    Logger:       deps.Logger,
}))
r.Use(middleware.AuthMiddleware(middleware.AuthConfig{
    DB:     deps.DB,
    Logger: deps.Logger,
}))
r.Use(middleware.CSRFMiddleware)
```

`Deps.SessionStore` の nil チェック分岐は削除。tests は 4b で必ず store を作るようになっている。

### Nav の Logout button

```text
[製品 | ベンダー | 部署 | ユーザ | 端末]                          [Logout]
                                                                  ^
                                                                  POST /logout
                                                                  + _csrf hidden
```

`base.templ` の Nav templ から `<form class="role-switcher">` を削除し、
代わりに `<form class="logout-form" method="post" action="/logout">` を追加。
中に `<button type="submit">ログアウト</button>` と CSRF hidden input。

`Nav` の引数は `(role middleware.Role, csrfToken string)` 据え置き
(csrfToken は logout form でも使う)。

### `cmd/server/main.go`

```go
r := handler.NewRouter(handler.Deps{
    Logger:        logger,
    DB:            sqlDB,
    StaticFS:      static.FS(),
    SessionStore:  sessionStore,
    CookieSecure:  cfg.Server.CookieSecure,
    SessionMaxAge: time.Duration(cfg.Auth.SessionMaxAgeHours) * time.Hour,
    Authenticator: auth.NewLocalAuthenticator(sqlDB),
})
```

`DevMode` 引数行と `os.Getenv("APP_MAN_DEV_MODE")` 行を削除。

## TDD / コミット順序

1. `docs(plans): DummyAuth 廃止と本番 router 切替の Plan ファイル`
2. `feat(handler): router で SessionMiddleware を必須化、AuthMiddleware を挿入`
3. `chore(handler): /__set_role と DevMode を廃止` (set_role.go / set_role_test.go 削除 + web.go から DevMode 削除 + router.Deps.DevMode 削除 + main.go の APP_MAN_DEV_MODE 削除)
4. `chore(view): Nav の role セレクタを Logout button に置換`
5. `chore(middleware): DummyAuthMiddleware と関連コードを削除`
6. `chore(handlertest): 旧 NewRequest / PostForm helper を削除`
7. `chore(web): /login テストの newAuthRouter を本番同等チェーンに揃える`
8. `chore(session): cookie.go のコメントから RoleCookieName 言及を削除`

各コミット後に `make test` / `make lint` 緑を確認する。

## 受け入れ基準

- `make build` / `make test` / `go test -race ./...` / `make lint` 全緑
- `grep -rn "DummyAuthMiddleware\|RoleCookieName\|roleFromCookie\|/__set_role\|setRole\|DevMode\|APP_MAN_DEV_MODE" --include="*.go" internal/ cmd/` が 0 件
- `appmgr-server` を起動し:
  - `GET /healthz` → 200
  - `GET /products` (未認証) → 303 to `/login?next=...`
  - `/login` フォームで admin / passw0rd を入力 → 303 to `/`
  - `GET /products` (認証済) → 200、Nav に Logout button、role セレクタは無い
  - Logout button → POST /logout → /login に redirect

## 想定リスク

- **Nav の CSS が role セレクタ前提**: role セレクタ削除に合わせて CSS の class 名 (.role-switcher) も整理する必要があるかも (本 PR では CSS には触れず、見た目が崩れる場合は別 PR で調整)
- **既存 handler テストへの破壊**: 4b 完了時点で全テストが session ベースなので、本 PR は本番 router 切替が主。テストの破壊が出るのは `web/auth_test.go` の newAuthRouter のみ
- **削除一括による diff の肥大**: テスト削除も含めると 1000+ 行削除。レビュアーが追いやすいよう「削除」「追加」「変更」を別コミットに分ける
