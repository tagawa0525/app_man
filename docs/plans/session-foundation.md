# セッション基盤 (Phase 3 第 2 PR)

## Context

Phase 3 第 1 PR (`appmgr-create-app-user`) でローカル管理アカウントの作成手段が揃った。次のステップはログイン経路の整備だが、その前段として **サーバ側セッション保存** を確立する必要がある。

仕様書の制約：

- §7.3「セッションは Cookie (HttpOnly, SameSite=Lax)」「`session_max_age_hours: 8`」
- §8.3「セッション ID は暗号論的乱数 32 バイト」「ログイン成功時にセッション ID を再発行 (固定攻撃対策)」
- §8.3「CSRF はセッションごとに発行したトークンと突合」→ CSRF token は session に bind される必要があり、session 基盤が CSRF 強化の前提

現状の `CSRFMiddleware` は `DummyCSRFToken = "dummy-csrf-token"` を固定値で検証している。session 基盤を入れた後 (別 PR で) この固定値検証を session.csrf_token 比較に置き換える。

仕様書はサーバ側保存方式 (in-memory / DB) を明示していないが、

- app_man は単一プロセス・単一インスタンス前提 (§8.2)
- リスタート跨ぎでセッションを残せると運用が楽 (オペレータが server を再起動してもログイン状態が維持される)
- 監査要件で「最終ログイン時刻」を `app_users.last_login_at` に書く必要があり、どのみち DB は触る

ので **SQLite 永続** を選ぶ。in-memory との切り替え抽象 (`Store` interface) は将来の需要が明確になるまで入れない (CLAUDE.md「早すぎる抽象化」)。

## 主要決定

| 項目 | 決定 | 判断 |
| --- | --- | --- |
| 保存方式 | SQLite の `sessions` テーブル | 上記理由 |
| セッション ID 表現 | crypto/rand 32 byte → URL-safe base64 (= 43 文字) | 仕様書 §8.3「32 バイト」を満たし、Cookie 安全な文字種に収まる |
| Cookie 名 | `app_man_session` | `RoleCookieName = "app_man_role"` と同 prefix で揃える |
| Cookie 属性 | HttpOnly、SameSite=Lax、Secure は **設定 (`server.cookie_secure: bool`) で切替** | 仕様書 §7.3 通り。開発は HTTP なので Secure=false が要る、本番は HTTPS で Secure=true |
| Cookie Path | `/` | 全エンドポイントで共有 |
| Cookie Max-Age | `auth.session_max_age_hours * 3600` (デフォルト 8h) | 仕様書通り |
| `expires_at` 列 | DB 側にも持つ | クライアントの clock skew や cookie 改竄を信用しない。サーバ側で必ず確認 |
| `last_seen_at` | リクエストごとに更新 | アイドルタイムアウト判定 (将来の slide expiry) と監査の両方に使える |
| 期限切れ掃除 | `DeleteExpiredSessions` クエリを用意するが、本 PR では起動時に 1 回だけ呼ぶ | 定期 GC は cron バイナリを足すまで不要。session 数は社員数 ×（同時セッション 数）で過大にならない |
| 匿名セッション | OK (app_user_id NULL を許可) | ログイン前 (/login GET) でも CSRF token が必要なので、未認証でも session を発行できる必要がある |
| `csrf_token` 列 | session 作成時に 32 byte 乱数を生成して保存 | 本 PR では発行のみ。CSRFMiddleware の検証ロジック差し替えは別 PR |
| ID 再発行 | `RotateSessionID(oldID, newID)` クエリを用意 | ログイン成功時に呼ぶ。本 PR ではテストでのみ呼び出しを検証 |
| ミドルウェア配置 | `SessionMiddleware` を `recoverer` の直後・`DummyAuthMiddleware` の前に挿入 | 後続の本物 AuthMiddleware は session.app_user_id を参照する形になる |
| Context への注入 | `SessionFrom(ctx) *Session` で取り出す。`Session` は ID / AppUserID (`*int64`) / CSRFToken / ExpiresAt のみ持つ struct | role と同じ pattern。型衝突を避けるため `sessionKey struct{}` |
| `internal/session` パッケージ | DB 操作・ID 生成・Cookie helper を集約。`SessionMiddleware` は `middleware` 側に置き、`session` パッケージを呼ぶ | `middleware` は HTTP 層、`session` は永続化層、で責務分離 |
| Cookie helper | `session.SetCookie(w, id, maxAge, secure)`、`session.ClearCookie(w, secure)` | テストで Cookie 属性を直接組み立てない |
| Cookie 不正値の扱い | DB に存在しない / `expires_at < now` ならば Cookie を削除して新規発行 | DummyAuthMiddleware が role cookie に対して既にやっているのと同じ寛容な扱い |
| `auth` セクション | `internal/config` に `AuthConfig { SessionMaxAgeHours int }` を追加 | フィールド単位で増やす (Mode / LDAP は次 PR 以降) |
| `server.cookie_secure` | bool フィールド追加 | 開発 (HTTP) ↔ 本番 (HTTPS) の切替 |
| 設定デフォルト | `auth.session_max_age_hours: 8`、`server.cookie_secure: false` | 仕様書通り / 開発優先 |
| sqlc コミット | 生成物 (`internal/repository/sessions.sql.go`) をコミット | 既存運用通り |
| 新規 SQL | `db/queries/sessions.sql` 新規 | `Create / GetByID / Touch (last_seen_at) / Rotate / Delete / DeleteExpired` |
| マイグレーション | `000007_sessions.up.sql` / `down.sql` | `app_users(id)` 参照 |
| ローカル GC | `cmd/server/main.go` 起動時に `DeleteExpiredSessions` を 1 回 | 別 cron バイナリは需要発生まで |
| `disabled_at` 連動 | 本 PR では考慮しない (Login 実装時に LocalAuthenticator が `disabled_at` を見て弾く) | スコープ分離 |
| `app_users.last_login_at` 更新 | 本 PR では実施しない | Login 実装 (LocalAuthenticator) と同 PR にする |

## 対象スコープ

### 範囲内

- `db/migrations/000007_sessions.up.sql` / `down.sql` (sessions テーブル)
- `db/queries/sessions.sql` (6 クエリ)
- sqlc 再生成 (`internal/repository/sessions.sql.go`)
- `internal/session/session.go`: `Session` struct、`Store` interface、`SQLiteStore` 実装、`NewID()` (32 byte → base64url)、`NewCSRFToken()` 同じパターン
- `internal/session/cookie.go`: `SetCookie` / `ClearCookie` / `ReadCookie` helper、Cookie 名定数 `CookieName`
- `internal/handler/middleware/session.go`: `SessionMiddleware(store, cookieSecure, sessionMaxAge) func(http.Handler) http.Handler`、`SessionFrom(ctx) *Session`
- `internal/config`: `AuthConfig`、`ServerConfig.CookieSecure` 追加 + validate / load tests
- `internal/handler/router.go`: `SessionMiddleware` を挿入 (`DummyAuthMiddleware` の前)
- `cmd/server/main.go`: 起動時に `store.DeleteExpired(ctx, time.Now())` を 1 回呼ぶ
- `config.yml` (devsample): `auth.session_max_age_hours: 8` / `server.cookie_secure: false` 追記

### 範囲外 (別 PR)

- LocalAuthenticator / LDAPAuthenticator / CompositeAuthenticator (第 3 PR)
- `/login` GET / POST handler (第 3 PR)
- 本物 AuthMiddleware (DummyAuthMiddleware の差し替え、第 3 PR)
- CSRFMiddleware の session-bound 化 (第 4 PR)
- `app_users.last_login_at` 更新 (第 3 PR)
- セッション一覧 / 強制ログアウト UI
- 定期 GC バイナリ (cron 等)

## 内部設計

### sessions テーブル

```sql
CREATE TABLE sessions (
  id TEXT PRIMARY KEY,                 -- 32 byte 乱数 base64url (43 文字)
  app_user_id INTEGER REFERENCES app_users(id), -- NULL = 匿名
  csrf_token TEXT NOT NULL,            -- 32 byte 乱数 base64url
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at DATETIME NOT NULL
);

CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
```

### `Session` struct

```go
type Session struct {
    ID         string
    AppUserID  *int64    // NULL なら未認証
    CSRFToken  string
    CreatedAt  time.Time
    LastSeenAt time.Time
    ExpiresAt  time.Time
}
```

### `Store` interface

```go
type Store interface {
    Create(ctx context.Context, s Session) error
    GetByID(ctx context.Context, id string) (*Session, error) // sql.ErrNoRows 透過
    Touch(ctx context.Context, id string, now time.Time) error
    Rotate(ctx context.Context, oldID, newID string) error    // 第 3 PR で使う
    Delete(ctx context.Context, id string) error
    DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}
```

抽象化理由は「テストで in-memory fake を使いたい」ではなく「将来の Authenticator テストで Store のモックを刺すことが見えている」点。`Rotate` を含めて 6 メソッド固定の小さい interface に絞れば負債にならない。

### `SessionMiddleware` の挙動

```text
1. Cookie 読み取り (app_man_session)
2. Cookie 値が存在し store.GetByID で hit、かつ expires_at > now なら:
     - store.Touch(id, now) (last_seen_at 更新)
     - context に session を詰めて next
3. Cookie 値が存在するが store にない or 期限切れなら:
     - 古い Cookie を削除指示 (Set-Cookie MaxAge<0)
     - 新規 session を発行 (匿名) → Cookie 設定 → context 詰めて next
4. Cookie が無い → 新規 session を発行 (匿名) → Cookie 設定 → context 詰めて next
```

**注意**: 毎リクエスト session 発行は heavy に見えるが、

- `/healthz` と `/static/*` は SessionMiddleware を通さないルーティングにする (router.go で chi.Group 分離)
- 業務ハンドラ側は GET でも session を必要とする (CSRF token のため)

の二点で必要十分。さらに毎リクエスト Touch は SQLite で WAL モードなら問題にならない (社員 300 名想定)。

### `cmd/server/main.go` 起動時 GC

```go
store := session.NewSQLiteStore(db)
deleted, err := store.DeleteExpired(ctx, time.Now())
if err != nil {
    logger.Warn("session GC failed", "err", err)
} else if deleted > 0 {
    logger.Info("expired sessions deleted", "count", deleted)
}
```

エラーは fatal にしない (GC は best-effort、server 起動を止めない)。

## TDD コミット順序

最初のコミットは Plan (`docs(plans): セッション基盤の Plan ファイル`)。以降:

1. `feat(db): sessions table migration 追加` — `000007_sessions.up.sql` / `down.sql` (`make build` で migrate スモークが通ること)
2. `test(session): ID 生成のユニットテスト` (RED) — `NewID` / `NewCSRFToken` がまだ無いので失敗
3. `feat(session): NewID / NewCSRFToken 実装` (GREEN) — `internal/session/id.go` (crypto/rand + base64.RawURLEncoding)
4. `feat(db/queries): sessions の 6 クエリ追加 + sqlc 再生成`
5. `test(session): SQLiteStore round-trip (RED)` — 実装無し
6. `feat(session): SQLiteStore 実装 (GREEN)` — `Create / GetByID / Touch / Rotate / Delete / DeleteExpired`
7. `test(session): Cookie helper の属性検証 (RED→GREEN 同 commit でも可)`
8. `feat(session): cookie.go (SetCookie / ClearCookie)`
9. `test(middleware): SessionMiddleware 挙動 (RED)` — 新規 / 既存 / 期限切れ / 不正 Cookie
10. `feat(middleware): SessionMiddleware (GREEN)`
11. `feat(config): AuthConfig + ServerConfig.CookieSecure 追加 + validate`
12. `feat(server): router に SessionMiddleware 挿入 + 起動時 GC`
13. `chore(config): config.yml に auth / cookie_secure を追記`

各ステップでテストを通し、最後に `make lint` まで全緑にする。

## 受け入れ基準

- `make build` / `make test` / `go test -race ./...` / `make lint` 全緑
- 任意のエンドポイントへ GET → Set-Cookie `app_man_session=...; HttpOnly; SameSite=Lax; Path=/` が返り、DB の `sessions` に対応行が INSERT される
- 同じ Cookie を付けて再リクエスト → 同 session ID、`sessions.last_seen_at` が更新される
- 不正な session_id Cookie を付けて GET → 新 session が発行され、古い Cookie 削除指示 (MaxAge<0) が Set-Cookie に出る
- 期限切れの session を持つ Cookie で GET → 同上
- `cmd/server/main.go` 起動 → 期限切れ session が掃除される (ログに件数が出る)
- DB スキーマ: `sessions(id TEXT PK, app_user_id INTEGER NULL, csrf_token TEXT, created_at, last_seen_at, expires_at)` が migrate で作られる
- 構造化ログに session ID / CSRF token が出ない (logger は session 発行/期限切れの件数のみ)

## 想定リスク

- **Cookie Secure=false** の本番起用: `cookie_secure: false` でデプロイされると盗聴可能。`validate()` で本番判定はできないので、ドキュメント (README) に注意書きを残す
- **session 毎リクエスト INSERT/UPDATE**: 性能要件 (300 名 / ダッシュボード < 2 秒) を脅かさないか。SQLite WAL + idx_sessions_expires_at で問題なし、と判断
- **CSRF middleware が壊れる**: 本 PR は CSRF を触らない (固定値検証のまま) ので影響なし。第 4 PR で session.csrf_token 比較に差し替える際は handlertest の dummy token 注入 helper も差し替える
