# ローカル認証 + /login UI / POST (Phase 3 第 3 PR)

## Context

セッション基盤 (PR #15) でサーバ側 session 保存と `*session.Session` の
context 受け渡しが整った。次は実際にユーザを認証して `session.AppUserID`
を埋める「ログイン経路」を作る。

仕様書の制約:

- §7.3「ローカル認証 (system_admin 用) ＋ AD パススルー認証 (一般社員用)」
- §7.3「`Authenticator` interface (`LocalAuthenticator` / `LDAPAuthenticator`
  / `CompositeAuthenticator` 等を抽象化)」
- §7.3 / §8.3「ログイン成功時に **セッション ID を再発行** する (固定攻撃対策)」
- §6.1「`/login` は認証不要」
- §3.6「ログイン可否判定: `disabled_at IS NULL`」(一般社員ログイン運用)
- §11 受け入れ基準 19「ローカル admin のパスワードリセット後にログインできる」

本 PR で扱うのは **ローカル認証経路のみ** (auth_type='local')。
LDAP / Composite は別 PR (Phase 3 後半 or Phase 4)。

## スコープを切る方針

Phase 3 残作業を 3 PR に分解する:

| PR | スコープ |
|----|---------|
| **本 PR (3a)** | LocalAuthenticator + GET/POST /login + POST /logout + login UI |
| 3b | 実 AuthMiddleware (session.AppUserID → role 解決) + DummyAuthMiddleware 廃止 + handler テスト全移行 |
| 3c | CSRFMiddleware の session-bound 化 (DummyCSRFToken 廃止) |

3a の後の状態: DummyAuthMiddleware が依然として `X-User-Role` ヘッダ/Cookie で
role を駆動するため、**ログインは「session に AppUserID を結びつける」操作止まり**
で、認可は dev cookie 経由のまま。次 PR (3b) で AuthMiddleware が
session.AppUserID から `user_department_roles` を引いて role を context に詰める
形に切り替える。

この分割の理由は、handler テスト群 (vendors / products / departments / users /
devices) が `X-User-Role` ヘッダ前提で書かれており、それを一気に session 認証に
書き換えるのは PR が肥大化する。本 PR では login 機能だけ加えて、handler テスト
は触らない方針。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| Authenticator 配置 | `internal/auth/authenticator.go` に interface + 共通型 + sentinel エラー、`internal/auth/local.go` に LocalAuthenticator | 仕様書通り。`internal/auth` は既存の bcrypt helper と同居 |
| Authenticator 戻り値 | `*AuthenticatedUser{ID int64, Username string}` (DB 行そのものではない) | DB レイヤ表現を上に漏らさない。後続 PR で linked_user_id 等が必要になったら拡張 |
| エラー sentinel | `ErrInvalidCredentials` / `ErrUserDisabled` / `ErrUnsupportedAuthType` | ハンドラ側で表示文言を分けるため。Invalid と NotFound を分けない (= username 存在の有無を漏らさない、列挙攻撃対策) |
| `disabled_at` チェック | LocalAuthenticator 内で実施。`disabled_at IS NOT NULL` → `ErrUserDisabled` | 仕様書 §3.6 通り |
| `auth_type` チェック | LocalAuthenticator 内で実施。`auth_type != 'local'` → `ErrUnsupportedAuthType` | local 経路に AD ユーザを通さない (AD は別 Authenticator が処理) |
| `last_login_at` 更新 | POST /login 成功時のみ。失敗時は更新しない (失敗回数を別途取らない MVP) | 仕様書には記載ないが運用上必須。新クエリ `UpdateAppUserLastLoginAt` 追加 |
| session ID 再発行 | POST /login 成功時。Cookie 再発行 + DB の `sessions.id` を `Rotate(oldID, newID)` で差し替え。`app_user_id` を新クエリ `BindSessionToAppUser` で埋める | 仕様書 §8.3「固定攻撃対策」 |
| 再発行のトランザクション | LocalAuthenticator → Rotate → BindSessionToAppUser → UpdateAppUserLastLoginAt をすべて 1 tx | 途中失敗で session ID だけ変わって AppUserID 埋まらず、を防ぐ |
| ログアウト | POST /logout: `store.Delete(sessionID)` + `ClearCookie` + 303 redirect to /login | 仕様書には記載ないが UI 上ほぼ必須 (Nav の Logout ボタン) |
| /login GET | session 既存・`AppUserID != nil` なら / に redirect、それ以外は login form を 200 で返す | 同 URL を 2 度叩く UX を成立させる |
| /login POST | 認証成功 → 303 redirect to / (or ?next=path)、失敗 → login form を **200** で再表示 (HTMX 親和性、フォーム値復元はしない) | redirect-after-POST。ステータスを 401 にすると HTMX が swap しない |
| /logout | POST のみ (GET だと CSRF 効かない)、`_csrf` 必須 (既存 CSRFMiddleware が DummyCSRFToken で検査) | CSRF 保護 |
| `?next=` 対応 | `/login?next=/path` を受け取り、success 時に `path` に戻す。**同一オリジン (相対パス + 先頭 `/` 必須 + `//` 排除) のみ許可** | open redirect 対策 |
| パスワード入力欄 | `<input type="password" name="password" autocomplete="current-password">` | ブラウザのオートフィル / パスワードマネージャ向け |
| エラー文言 | 「ユーザ名またはパスワードが正しくありません」(Invalid 全部に統一) / 「アカウントが無効化されています」(Disabled) / 「ローカル認証アカウントではありません」(AuthTypeMismatch) | 列挙攻撃対策 + Disabled / AuthTypeMismatch は内部 admin が見て判断 |
| /login のレイアウト | `internal/view/layout.Base` を使わない。専用の薄いレイアウト | Nav (role 切替、業務リンク) を未ログイン状態で見せない |
| CSRF token 値 | `middleware.DummyCSRFToken` をテンプレ props で渡す (既存 handler と同じパターン) | 既存と統一。次 PR (3c) で session.CSRFToken に一括差し替え |
| Login form パッケージ配置 | `internal/view/auth/login.templ` 1 ファイルに `LoginPage` テンプレ + 専用 HTML レイアウトを同居 | 既存 `view/<entity>/` のパターンを踏襲しつつ、共有レイアウトの抽出は重複が出てから |
| handler 配置 | `internal/handler/web/auth.go` (loginGet / loginPost / logoutPost) | 業務 handler と同居 (web パッケージ) |
| ルート登録 | `RegisterRoutes` の中で GET /login / POST /login / POST /logout を **role 不問**で登録 | RequireRole グループの**外側** |
| Authenticator 依存注入 | `web.Deps` に `Authenticator` フィールドと `SessionStore` フィールドを追加 | 既存 Deps の延長線で揃える |
| SessionStore の渡し方 | `handler.Deps.SessionStore` を `web.Deps.SessionStore` に流す | 既存パターン (Logger / DB を流すのと同じ) |
| 構造化ログ | 成功: `info "login success" username app_user_id`、失敗: `info "login failed" username reason`、ログアウト: `info "logout" app_user_id`。**パスワード平文 / ハッシュは絶対に出さない** | 監査要件。失敗を warn ではなく info にするのは、人為的な打ち間違いを警告にするとログがノイズだらけになるため |
| Authenticator の future-proofing | interface に閉じ込めて Composite は別 PR で外側に重ねる。本 PR では LocalAuthenticator を直接 web.Deps に注入する | 早すぎる抽象化を避ける (CLAUDE.md) |

## 対象スコープ

### 範囲内

- `internal/auth/authenticator.go`: `Authenticator` interface + `AuthenticatedUser` 型 + 3 sentinel エラー
- `internal/auth/local.go`: `LocalAuthenticator{db *sql.DB}` 実装
- `internal/auth/local_test.go`: in-memory DB を使った認証成功 / 不正パスワード / disabled / AD アカウント / 未存在
- `db/queries/app_users.sql`: `UpdateAppUserLastLoginAt` 追加
- `db/queries/sessions.sql`: `BindSessionToAppUser` 追加 (UPDATE sessions SET app_user_id = ? WHERE id = ?)
- sqlc 再生成
- `internal/view/auth/login.templ` (Base を使わない薄い HTML を同ファイルに同居)
- `internal/handler/web/auth.go`: loginGet / loginPost / logoutPost
- `internal/handler/web/auth_test.go`: GET 200、POST 成功、POST 失敗、CSRF 無し、`?next=` 同一オリジン、open redirect 拒否、ログアウト
- `internal/handler/web/web.go`: Deps に `Authenticator` / `SessionStore` 追加、ルート登録
- `internal/handler/router.go`: `Deps.SessionStore` を web.Deps に流す
- `cmd/server/main.go`: `auth.NewLocalAuthenticator(sqlDB)` を構築して web.Deps に注入

### 範囲外 (別 PR)

- 実 AuthMiddleware (DummyAuthMiddleware 差し替え) (3b)
- handler テスト群の session 化 (3b)
- CSRF token を session.CSRFToken に切替 (3c)
- LDAPAuthenticator / CompositeAuthenticator (LDAP PR)
- ログイン失敗回数 / アカウントロック / レート制限 (運用判断後)
- パスワード変更画面 (admin のみ `appmgr-create-app-user --reset-password` で代替)

## 内部設計

### Authenticator interface

```go
package auth

type AuthenticatedUser struct {
    ID       int64  // app_users.id
    Username string // app_users.username
}

type Authenticator interface {
    Authenticate(ctx context.Context, username, password string) (*AuthenticatedUser, error)
}

var (
    ErrInvalidCredentials  = errors.New("invalid credentials")
    ErrUserDisabled        = errors.New("user disabled")
    ErrUnsupportedAuthType = errors.New("unsupported auth type")
)
```

`ErrInvalidCredentials` は username 存在しない / password 不一致 / password_hash NULL のすべてに使う (列挙攻撃対策)。

### LocalAuthenticator

```go
type LocalAuthenticator struct {
    db *sql.DB
}

func NewLocalAuthenticator(db *sql.DB) *LocalAuthenticator { ... }

func (a *LocalAuthenticator) Authenticate(ctx context.Context, username, password string) (*AuthenticatedUser, error) {
    1. repository.New(a.db).GetAppUserByUsername(ctx, username)
       - sql.ErrNoRows → ErrInvalidCredentials
       - other err → fmt.Errorf("lookup: %w", err)
    2. row.AuthType != "local" → ErrUnsupportedAuthType
    3. row.DisabledAt != nil → ErrUserDisabled
    4. row.PasswordHash == nil → ErrInvalidCredentials (local だが password_hash 無し)
    5. password.Verify(*row.PasswordHash, password)
       - ErrPasswordMismatch → ErrInvalidCredentials
       - other err → fmt.Errorf("verify: %w", err)
    6. return &AuthenticatedUser{ID: row.ID, Username: row.Username}, nil
}
```

### loginPost フロー

```text
1. ParseForm → username / password / next
2. CSRF middleware は通過済み (ここでは検査しない)
3. authenticator.Authenticate(ctx, username, password)
   - ErrInvalidCredentials → 200 で login form 再表示 (汎用エラー文言)
   - ErrUserDisabled → 200 で login form 再表示 (Disabled 文言)
   - ErrUnsupportedAuthType → 200 で login form 再表示 (AD ユーザ文言)
   - その他 err → 500
4. session := middleware.SessionFrom(ctx) (SessionMiddleware が事前に発行済み)
   - session == nil なら 500 (起動時 setup ミス)
5. tx 開始
   - newID := session.NewID()
   - store.Rotate(ctx tx, oldID=session.ID, newID)
   - q.BindSessionToAppUser(ctx tx, sessionID=newID, appUserID=&user.ID)
   - q.UpdateAppUserLastLoginAt(ctx tx, lastLoginAt=now, id=user.ID)
   - tx.Commit()
6. session.SetCookie(w, newID, maxAge, secure)
7. 303 to validatedNext (default "/")
```

ハンドラは `web.Deps` から `Authenticator`, `SessionStore`, `*sql.DB`, `CookieSecure`, `SessionMaxAge` を取る。

### logoutPost フロー

```text
1. session := middleware.SessionFrom(ctx)
2. session != nil && session.ID != "" なら store.Delete(ctx, session.ID)
   - エラーは warn ログのみ、続行
3. session.ClearCookie(w, secure)
4. 303 to /login
```

### `?next=` の検証

```go
// validateNext は同一オリジン (絶対パス、先頭 "/" 必須、"//" 排除、外部 URL 排除) のみ許可する。
func validateNext(raw string) string {
    if raw == "" { return "/" }
    // "/" で始まり、"//" で始まらず、url.Parse して Host が空のもののみ通す
    if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
        return "/"
    }
    u, err := url.Parse(raw)
    if err != nil || u.Host != "" || u.Scheme != "" {
        return "/"
    }
    return u.RequestURI() // クエリ含む
}
```

### login templ

Layout は専用の `LoginLayout` を切る (Nav なし、CSS は `/static/css/app.css` 共通)。

```text
LoginPage(props LoginProps):
  <form method="post" action="/login?next=...">
    <input type="hidden" name="_csrf" value={csrf}>
    <label>ユーザ名 <input name="username" autocomplete="username" required></label>
    <label>パスワード <input name="password" type="password" autocomplete="current-password" required></label>
    if errorMessage != "" { <div class="error">{errorMessage}</div> }
    <button type="submit">ログイン</button>
  </form>
```

`LoginProps`:

```go
type LoginProps struct {
    Title        string
    CSRFToken    string
    Next         string
    ErrorMessage string  // 表示する場合のみ非空
}
```

## TDD コミット順序

1. `docs(plans): ローカル認証 + /login の Plan ファイル`
2. `feat(db/queries): sessions.BindSessionToAppUser と app_users.UpdateAppUserLastLoginAt 追加 + sqlc`
3. `test(auth): LocalAuthenticator の success / wrong password / disabled / AD / 未存在 (RED)`
4. `feat(auth): Authenticator interface + LocalAuthenticator (GREEN)`
5. `test(view/auth): LoginPage template snapshot 的 (RED は templ ファイル無いので skip 可能、直接 GREEN でも可)`
6. `feat(view/auth): login.templ + login_layout.templ`
7. `test(web): GET /login が 200 でフォーム + CSRF token を返す (RED)`
8. `feat(web): loginGet ハンドラ (GREEN)`
9. `test(web): POST /login success → 303 to /、session.AppUserID 埋まる、last_login_at 更新、session ID 再発行 (RED)`
10. `feat(web): loginPost ハンドラ (GREEN)`
11. `test(web): POST /login wrong password / disabled / AD → 200 で各エラー文言 (RED)`
12. `feat(web): エラー分岐実装 (GREEN)`
13. `test(web): POST /login の ?next= 同一オリジン許可 / 外部 URL 拒否 (RED)`
14. `feat(web): validateNext 実装 (GREEN)`
15. `test(web): POST /logout が session を消す + Cookie を消す + /login へ 303 (RED)`
16. `feat(web): logoutPost (GREEN)`
17. `feat(web): web.Deps に Authenticator / SessionStore / CookieSecure / SessionMaxAge 追加、router / main から流す`

## 受け入れ基準

- `make build` / `make test` / `go test -race ./...` / `make lint` 全緑
- LocalAuthenticator 単体: 正しい credentials で `*AuthenticatedUser` 返却、wrong password で `ErrInvalidCredentials`、disabled で `ErrUserDisabled`、auth_type='ad' で `ErrUnsupportedAuthType`、未存在 username で `ErrInvalidCredentials` (列挙されない)
- GET /login → 200、form の hidden に `_csrf=dummy-csrf-token`
- POST /login (正しい credentials) → 303 to /、Cookie に新 session ID (rotation 完了)、DB の `sessions.app_user_id` 埋まる、`app_users.last_login_at` 更新
- POST /login (失敗系) → 200 で form 再表示、エラー文言は 3 種類で分岐
- POST /login ?next=/products → 303 to /products、?next=//evil.com → 303 to /、?next=https://evil.com → 303 to /
- POST /logout → `sessions` から消える、Cookie に MaxAge<0 が返る、303 to /login
- 構造化ログにパスワード平文 / hash が出ない (logger は username / role / app_user_id のみ)
- `appmgr-create-app-user` で作った admin が `/login` から実際に入れる (build verify)

## 想定リスク

- **DummyAuth と併用**: 本 PR では DummyAuthMiddleware が role を駆動するので、ログインしても role が変わらない (= /admin/audit-logs に dev cookie で入れたまま)。これは設計上の中間状態で、次 PR 3b で解消される
- **CSRF token が DummyCSRFToken のまま**: 別 PR (3c) で session.CSRFToken に統一する
- **session.Rotate のレース**: session middleware が直前にリクエストを処理中に Rotate されると、その並列リクエストの SessionMiddleware が GetByID(oldID) で失敗し新セッションを発行してしまう。実害は「同時に 2 つのタブで操作中にログインすると古いタブの session が壊れる」程度。MVP では許容
- **`disabled_at` 動的変更**: ログイン中に admin が disable した場合、session.AppUserID が残るので継続できる。実 AuthMiddleware (PR 3b) で都度 `disabled_at` チェックを入れるかは別途判断
