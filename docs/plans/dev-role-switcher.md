# 開発用ロール切替 UI

## Context

現在の `DummyAuthMiddleware` (`internal/handler/middleware/auth.go` の `DummyAuthMiddleware`) は `X-User-Role` HTTP ヘッダから role を取り出すのみで、ブラウザから画面確認するには ModHeader 等の拡張機能が必須となっており、手数が増えて検証が滞る状態。フェーズ 2 で 6 マスタの CRUD が揃ったため、画面ベースでの全体動作確認を行いたいが、その障壁を取り除く必要がある。

要件書 §7.1 のロール 5 種 (system_admin / department_security_admin / license_manager / viewer / general_user) を Nav 右端のドロップダウンから即時切替できるようにし、Cookie に保存して以降のリクエストに反映する。**ヘッダー方式は既存テストとの互換性のために残し、Cookie はそのフォールバックとして動かす** (ヘッダ > Cookie > general_user)。

フェーズ 3 で本物の認証 (ローカル / LDAP) に置き換える際、本機能は次のいずれかになる:

1. **削除**: 認証が本物になれば「ロール切替」は不要になる
2. **Act as に発展**: 「強い権限ユーザが弱い権限で動作確認する」機能 (要件書未規定だが運用上の有用性あり) として残し、system_admin のみ使える形に絞る

本 PR ではどちらの道も残せるよう、middleware のフックは中立に作り、Nav UI と `/__set_role` エンドポイントは「dev only」のコメントで明示する。**フェーズ 3 で再判断**。

## 主要決定

| 項目                       | 決定                                                                                                                            |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| Cookie 名                  | `app_man_role` (固定、namespace 衝突回避)                                                                                       |
| Cookie 属性                | `HttpOnly` / `SameSite=Lax` / `Path=/`。`Secure` は本番 (TLS) で別途付与する設定だが、本 PR では未対応 (dev 用途のため)            |
| Cookie 寿命                | 30 日 (ブラウザ閉じても保持。dev で何度も切り替えるたびに失われると面倒)                                                          |
| 優先順位                   | `X-User-Role` ヘッダ > `app_man_role` Cookie > `general_user` (既存テスト 100 本超を破壊しないため)                                |
| ヘッダ / Cookie 値の検証   | ヘッダの未知値は従来通り **403 Forbidden** で next 中断。Cookie の未知値は **寛容に `general_user` フォールバック + Set-Cookie で削除** (Cookie 起源の壊れた状態でも画面が動くようにする)              |
| 設定エンドポイント         | `POST /__set_role`、`role=viewer` の form データ。CSRF トークン必須 (`_csrf=dummy-csrf-token`)。成功時 303 で Referer に戻る       |
| Referer がない / 別オリジン | `/` に戻す (オープンリダイレクト回避)                                                                                            |
| Nav UI                     | `internal/view/layout/base.templ` の Nav 右端 `role: ...` 表示を `<form>` + `<select>` + `<button>` に置換。即時 POST           |
| UI 表示の出し分け          | 全 role で表示 (機能の存在を見せるため、本番運用時は middleware で禁止する選択肢を残す)                                          |
| エンドポイントの認可       | 全 role が叩ける (general_user でも切替可能)。CSRF だけが保護                                                                  |
| middleware 内位置          | 既存の `DummyAuthMiddleware` 内で `X-User-Role` 取得直後にフォールバックする 1 ブロックを追加。新規 middleware は作らない         |

## 対象スコープ

### 範囲内

- `DummyAuthMiddleware` に Cookie 読み取りフォールバックを追加
- `POST /__set_role` エンドポイント (`internal/handler/web/set_role.go` 新設)
- Nav の `role: ...` 表示を form + select に置換
- 既存テストは無変更 (ヘッダ優先のため壊れない)
- 新規テスト: middleware の Cookie 読み取り / 設定エンドポイントの正常系・異常系 / Nav UI の描画

### 範囲外

- フェーズ 3 認証実装 (LDAP / ローカル) との統合
- 本番無効化フラグ (`AppEnv=production` で `/__set_role` を 404 にする等)
- ロール表示の i18n
- `general_user` での `/__set_role` 拒否 (フェーズ 3 で act-as として絞る場合に検討)

## ファイル構成

| パス                                                  | 概要                                                                  | 区分     |
| ----------------------------------------------------- | --------------------------------------------------------------------- | -------- |
| `docs/plans/dev-role-switcher.md`                     | 本 Plan                                                               | 新規     |
| `internal/handler/middleware/auth.go`                 | Cookie 読み取りフォールバックを追加、Cookie 名定数 `RoleCookieName` を export | 編集     |
| `internal/handler/middleware/auth_test.go`            | Cookie 経由 role の確認 (RED → GREEN)                                  | 編集     |
| `internal/handler/web/set_role.go`                    | `POST /__set_role` ハンドラ + handler 構造体                          | 新規     |
| `internal/handler/web/set_role_test.go`               | 正常系 (303 + Cookie) / 不正 role (400) / CSRF 欠落 (403)               | 新規     |
| `internal/handler/web/web.go`                         | `POST /__set_role` をルート登録 (CSRF middleware 経由、認可なし)        | 編集     |
| `internal/view/layout/base.templ`                     | Nav の `role: ...` 表示を form + select に置換                        | 編集     |
| `internal/view/layout/base_templ.go`                  | templ 再生成                                                          | 編集     |

## 実装詳細

### `DummyAuthMiddleware` の改修 (`internal/handler/middleware/auth.go`)

```go
// RoleCookieName は dev 向けロール切替で利用する Cookie 名。
// 本物の認証が入るフェーズ 3 で扱いを再判断する。
const RoleCookieName = "app_man_role"

func DummyAuthMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        raw := r.Header.Get("X-User-Role")
        // X-User-Role がなければ Cookie をフォールバックとして見る。
        // 不正値の Cookie は削除して general_user 扱い。
        if raw == "" {
            if c, err := r.Cookie(RoleCookieName); err == nil {
                if _, ok := validRoles[Role(c.Value)]; ok {
                    raw = c.Value
                } else {
                    http.SetCookie(w, &http.Cookie{
                        Name: RoleCookieName, Value: "", Path: "/", MaxAge: -1,
                    })
                }
            }
        }

        role := Role(raw)
        switch raw {
        case "":
            role = RoleGeneralUser
        default:
            if _, ok := validRoles[role]; !ok {
                http.Error(w, "unknown role", http.StatusForbidden)
                return
            }
        }
        ctx := context.WithValue(r.Context(), roleKey{}, role)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

### `POST /__set_role` (`internal/handler/web/set_role.go`)

- Form `role` を取得、`middleware.validRoles` ベース (もしくは新規 `IsValidRole` 関数) で検証
- 不正なら 400
- 正なら `Set-Cookie: app_man_role=...; HttpOnly; SameSite=Lax; Path=/; Max-Age=2592000` を付与
- 303 で `Referer` ヘッダにリダイレクト (空 or 別オリジンなら `/`)

```go
const roleCookieMaxAge = 30 * 24 * 60 * 60 // 30 days

func (h *devHandlers) setRole(w http.ResponseWriter, r *http.Request) {
    if err := r.ParseForm(); err != nil {
        http.Error(w, "bad request", http.StatusBadRequest)
        return
    }
    role := r.PostFormValue("role")
    if !middleware.IsValidRole(middleware.Role(role)) {
        http.Error(w, "unknown role", http.StatusBadRequest)
        return
    }
    http.SetCookie(w, &http.Cookie{
        Name: middleware.RoleCookieName, Value: role, Path: "/",
        HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: roleCookieMaxAge,
    })
    http.Redirect(w, r, safeRedirect(r), http.StatusSeeOther)
}

// safeRedirect は Referer が同一オリジンなら採用、そうでなければ "/"。
func safeRedirect(r *http.Request) string {
    ref := r.Header.Get("Referer")
    if ref == "" {
        return "/"
    }
    u, err := url.Parse(ref)
    if err != nil || u.Host != r.Host {
        return "/"
    }
    return u.RequestURI()
}
```

`IsValidRole` は `auth.go` 側に export する新関数 (既存の `validRoles` は package private)。

### `RegisterRoutes` への追加 (`internal/handler/web/web.go`)

```go
d := &devHandlers{logger: deps.Logger}
// 認可は一切なし。CSRF middleware は上位で適用済。
r.Post("/__set_role", d.setRole)
```

`/__set_role` だけは認可グループの外に置き、general_user 含む全リクエストから叩けるようにする。

### Nav の改修 (`internal/view/layout/base.templ`)

既存の `<span class="role">role: { string(role) }</span>` を以下の form に置換:

```templ
<form class="role-switcher" method="post" action="/__set_role">
    @CSRFInput(csrfToken)
    <label for="role-select">role:</label>
    <select id="role-select" name="role" onchange="this.form.submit()">
        @roleOption(role, middleware.RoleSystemAdmin)
        @roleOption(role, middleware.RoleDepartmentSecurityAdmin)
        @roleOption(role, middleware.RoleLicenseManager)
        @roleOption(role, middleware.RoleViewer)
        @roleOption(role, middleware.RoleGeneralUser)
    </select>
</form>

templ roleOption(current, target middleware.Role) {
    if current == target {
        <option value={ string(target) } selected>{ string(target) }</option>
    } else {
        <option value={ string(target) }>{ string(target) }</option>
    }
}
```

`onchange="this.form.submit()"` で即時切替。JS 1 行のみで HTMX に依存しない (要件書 §2 の CDN 禁止に抵触しない)。

## テストケース

### `middleware/auth_test.go` への追加

```text
TestDummyAuthMiddleware_FallsBackToCookie
TestDummyAuthMiddleware_HeaderTakesPrecedenceOverCookie
TestDummyAuthMiddleware_InvalidCookieClearsCookieAndFallsBackToGeneral
```

### `web/set_role_test.go` 新規

```text
TestSetRole_SetsCookie_RedirectsToReferer
TestSetRole_RedirectsToRoot_WhenNoReferer
TestSetRole_RedirectsToRoot_WhenRefererIsExternal
TestSetRole_RejectsInvalidRole_400
TestSetRole_RejectsWithoutCSRF_403
TestSetRole_AcceptedForAllRoles  // general_user でも叩ける
```

Nav の描画は既存の handler テスト (`vendors_test.go` 等) を介して間接的に確認できる (Nav は全画面に挟まる)。専用のテストは作らない。

## コミット列 (TDD サイクル)

ブランチ: `feat/dev-role-switcher`。CLAUDE.md「最初のコミットを Plan ファイル」規約に従う。

| #  | コミット件名                                                                              | サイクル |
| -- | ----------------------------------------------------------------------------------------- | -------- |
| 1  | `docs(plans): 開発用ロール切替 UI の実装プラン`                                           | —        |
| 2  | `test(middleware): DummyAuthMiddleware の Cookie フォールバック (RED)`                      | RED      |
| 3  | `feat(middleware): DummyAuthMiddleware に Cookie フォールバックを追加 + IsValidRole export` | GREEN    |
| 4  | `test(handler/web): POST /__set_role 正常系 + 不正 role + CSRF 欠落 (RED)`                 | RED      |
| 5  | `feat(handler/web): POST /__set_role ハンドラと配線`                                       | GREEN    |
| 6  | `feat(view/layout): Nav にロール切替ドロップダウンを追加`                                  | —        |

## 受け入れ基準

- [ ] `make generate` / `make build` / `make test` / `go test -race ./...` / `make lint` 全緑
- [ ] サーバ起動後、ブラウザで `http://localhost:8180/vendors` を開くと Nav 右端に role select が見える
- [ ] select 変更で即時 Cookie 設定 → 同じ画面 (Referer) にリダイレクト → role 表示と権限が変わる
- [ ] `viewer` 選択時に `/vendors/new` のボタンが消える、`license_manager` 選択時に表示される
- [ ] `general_user` 選択時に `/users` が 403
- [ ] X-User-Role ヘッダ付きの curl は既存通り動く (ヘッダ優先)
- [ ] PR 本文に「dev 用、フェーズ 3 認証で再判断」を明記

## フェーズ 3 への引き継ぎ

- 本物の認証 (ローカル / LDAP) が入った時点で、以下のどちらかを選択:
  1. `/__set_role` と Cookie フォールバックを **削除** し、認証セッション一本に絞る
  2. `system_admin` 限定の **act-as** に絞り、画面上は「一時的に弱い権限で動作確認」と明示
- どちらにせよ middleware の優先順位 (header > cookie > session) は維持できる構造になっているため、置き換えが局所的に済む
