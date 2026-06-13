# CSRF 強化 (session 紐付きトークン化) — Phase 3 第 5 PR

## Context

Phase 3 第 1〜4 PR で session 基盤 / ログイン / 実 AuthMiddleware が揃った。
仕様書 §8.3 では **「サーバ側はセッションごとに発行したトークンと突合」**
することを CSRF 対策として要求しているが、現状の `CSRFMiddleware` は
`DummyCSRFToken = "dummy-csrf-token"` 固定値検証で済ませている。本 PR で
session.CSRFToken (PR #15 で発行済み) を使った検証に切り替える。

## 影響箇所の整理

`DummyCSRFToken` を直接参照しているファイル:

- `internal/handler/middleware/csrf.go` — 検証ロジック
- `internal/handler/middleware/csrf_test.go` — テスト
- `internal/handler/web/auth.go` — login form 埋め込み (5 箇所)
- `internal/handler/web/auth_test.go` — POST テスト helper
- `internal/view/<entity>/*_templ.go` — 自動生成、書き換え不要
- `internal/view/<entity>/*.templ` — テンプレ。`BaseProps.CSRFToken` と `layout.CSRFInput(token)` に固定値を渡す形で 18 ファイル
- `internal/view/errors/errors.templ` — 404 / 403 ページ

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| 検証先 | `SessionFrom(ctx).CSRFToken` | 仕様書 §8.3 通り |
| トークン取得 API | `middleware.CSRFTokenFrom(ctx context.Context) string` を export。SessionFrom が nil なら空文字を返す | 既存 `RoleFrom(ctx)` と同型の context アクセサ |
| templ 側の参照 | `middleware.DummyCSRFToken` → `middleware.CSRFTokenFrom(ctx)`。templ は `{ ctx }` 暗黙引数を参照できる | handler 側を触らず templ 1 行で済ませる |
| handler 側 (auth.go の login form) | `middleware.DummyCSRFToken` → `middleware.CSRFTokenFrom(r.Context())` | login 経路は handler が直接 BaseProps を組み立てている |
| `DummyCSRFToken` 定数 | 完全削除 | 全 caller を実トークン経由に切替後 |
| 安全メソッド | GET / HEAD / OPTIONS は素通り | 既存挙動維持 |
| 検証失敗時の挙動 | 403 + "csrf token mismatch" | 既存挙動維持 |
| 空 token vs session token == "" | constant-time 比較で常に失敗扱い (== "" 時は 403) | ephemeral session で session.CSRFToken == "" のとき、誤って通すと CSRF が無防備になる。明示的に拒否 |
| handlertest.AuthenticatedPostForm | session の CSRFToken を Cookie 経由で取れないため、helper 内で「Cookie 作成 → DB から session 取得 → CSRFToken を form に埋める」に変更 | 既存 caller のシグネチャは維持 |
| 既存の `AuthenticatedRequest` (GET 系) | 変更不要 | GET は CSRF 検証なし |
| `view/layout/base.templ` の `CSRFInput` 引数 | `csrfToken string` のまま据え置き。呼び出し側で `middleware.CSRFTokenFrom(ctx)` を渡す | 互換性維持 |
| ログ | CSRF token mismatch を info ログに残す (path / session_id 末尾) | デバッグ用、token 本体は出さない |

## 対象スコープ

### 範囲内

- `internal/handler/middleware/csrf.go`: `CSRFTokenFrom(ctx)` 追加、`CSRFMiddleware` を session 紐付き検証に書き換え、`DummyCSRFToken` 削除
- `internal/handler/middleware/csrf_test.go`: 全テストを session ベースに書き換え
- `internal/view/**/*.templ` (18 ファイル): `middleware.DummyCSRFToken` → `middleware.CSRFTokenFrom(ctx)` に置換
- `internal/view/**/*_templ.go`: templ generate で再生成
- `internal/handler/web/auth.go`: login form の CSRFToken 渡し方を変更
- `internal/handler/web/auth_test.go`: POST テストの `_csrf` 値を session.CSRFToken から取るように変更
- `internal/handler/handlertest/auth.go`: `AuthenticatedPostForm` の `_csrf` を session.CSRFToken から取る
- `internal/handler/middleware/middleware.go`: パッケージドキュメントを実装に合わせる

### 範囲外 (別 PR / Phase 4 以降)

- Double Submit Cookie 方式への切替 (Cookie + ヘッダ突合)
- per-form ナンス
- CSP / SameSite=Strict
- Nav のログイン中ユーザ名表示

## 内部設計

### `CSRFTokenFrom`

```go
package middleware

import "context"

// CSRFTokenFrom は SessionFrom(ctx) から CSRF token を取り出す。
// session が無い / ephemeral (CSRFToken == "") の場合は空文字を返す。
// templ 側で同じ token を form の hidden / meta tag に埋め、
// CSRFMiddleware が POST 等で検証する。
func CSRFTokenFrom(ctx context.Context) string {
    if s := SessionFrom(ctx); s != nil {
        return s.CSRFToken
    }
    return ""
}
```

### `CSRFMiddleware` (改修後)

```go
func CSRFMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if _, safe := safeMethods[r.Method]; safe {
            next.ServeHTTP(w, r)
            return
        }
        token := r.Header.Get("X-CSRF-Token")
        if token == "" {
            if err := r.ParseForm(); err == nil {
                token = r.PostFormValue("_csrf")
            }
        }
        expected := CSRFTokenFrom(r.Context())
        if expected == "" || subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
            // path / session_id 末尾だけログに出して本体は出さない
            http.Error(w, "csrf token mismatch", http.StatusForbidden)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

### templ 側の置換

```templ
// before
CSRFToken: middleware.DummyCSRFToken

// after
CSRFToken: middleware.CSRFTokenFrom(ctx)
```

`templ` の組み込み引数 `ctx context.Context` が各テンプレ関数内で
参照可能。コンパイル時に検証されるので置換ミスは即落ちる。

### `handlertest.AuthenticatedPostForm` 改修

```go
func AuthenticatedPostForm(t *testing.T, db *sql.DB, store session.Store,
    target string, role middleware.Role, values url.Values,
) *http.Request {
    if values == nil { values = url.Values{} }
    var cookie *http.Cookie
    if role != "" {
        cookie = AuthenticatedAs(t, db, store, role)
        // session.CSRFToken を form に埋める
        sess, err := store.GetByID(context.Background(), cookie.Value)
        if err != nil { t.Fatalf(...) }
        if values.Get("_csrf") == "" {
            values.Set("_csrf", sess.CSRFToken)
        }
    }
    // role == "" の場合は _csrf を埋めない (CSRF 検証で 403 を期待するテスト)
    req := httptest.NewRequest(http.MethodPost, target, ...)
    if cookie != nil { req.AddCookie(cookie) }
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    return req
}
```

### `web/auth.go` の login form

login の前 (GET /login) は未認証 session でも CSRF token を持っているので、
そのトークンを form に埋めれば POST /login の CSRF 検証も通る。

```go
// before
h.renderLogin(w, r, authview.LoginProps{
    CSRFToken: middleware.DummyCSRFToken,
    Next:      next,
}, http.StatusOK)

// after
h.renderLogin(w, r, authview.LoginProps{
    CSRFToken: middleware.CSRFTokenFrom(r.Context()),
    Next:      next,
}, http.StatusOK)
```

## TDD コミット順序

1. `docs(plans): CSRF 強化の Plan ファイル`
2. `feat(middleware): CSRFTokenFrom(ctx) helper を追加`
3. `test(middleware): CSRFMiddleware を session 紐付き検証 (RED)`
4. `feat(middleware): CSRFMiddleware を session 紐付き検証に切替 (GREEN)`
5. `chore(view): 18 templ で DummyCSRFToken → CSRFTokenFrom(ctx) 置換 + templ generate`
6. `chore(handler/web): login の DummyCSRFToken を CSRFTokenFrom(r.Context()) に`
7. `chore(handlertest): AuthenticatedPostForm を session.CSRFToken から取得`
8. `chore: DummyCSRFToken を削除`

## 受け入れ基準

- `make build` / `make test` / `go test -race ./...` / `make lint` 全緑
- `grep -rn "DummyCSRFToken" --include="*.go" internal/` が 0 件
- `appmgr-server` 起動 → /login で発行された session の CSRFToken が form の `_csrf` hidden input に埋まる → POST /login が 200 通る
- 不正な `_csrf` 値で POST → 403
- session が ephemeral (CSRFToken == "") のリクエストで POST → 403
- GET 系は CSRF 検証なしで素通り

## 想定リスク

- **CSRFMiddleware 順序**: 現状 `SessionMiddleware → AuthMiddleware → CSRFMiddleware` の順なので、CSRFMiddleware は session を読める。順序が崩れると panic / 全 POST が 403 になる
- **templ ctx 参照**: templ 関数内で `ctx` を直接参照できるか実証が必要。動かなければ handler 経由で BaseProps に渡す形にフォールバック (作業量が増えるが代替案あり)
- **AuthenticatedPostForm のシグネチャ非互換**: 既存の caller (13 handler テスト) は無修正で通る前提だが、session.CSRFToken 取得が増えるためテストが遅くなる可能性 (微小)
