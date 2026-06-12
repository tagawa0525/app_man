# 実 AuthMiddleware の追加とテスト helper (Phase 3 第 4 PR — 4a)

## Context

PR #16 で `/login` GET/POST と LocalAuthenticator が入り、ログイン成功時に
`session.AppUserID` が DB に書かれるようになった。しかし認可経路はまだ
`DummyAuthMiddleware` が `X-User-Role` ヘッダ / dev cookie で role を駆動
している (= ログインしても認可は dev cookie のまま)。

Phase 3 第 4 PR で「実 AuthMiddleware (session.AppUserID → user_department_roles
→ role を context に詰める)」に切り替える。これは既存 14 個の handler テスト
ファイル (X-User-Role 駆動) の全面書き換えを伴うため、リスク分散のために
3 段階に分ける:

| PR | スコープ |
|----|---------|
| **本 PR (4a)** | 実 `AuthMiddleware` を新規追加 + 認証済 session を作るテスト helper `AuthenticatedAs` を追加。既存 router / 既存 test には触れない |
| 4b | 14 個の handler テストを `AuthenticatedAs` ベースに書き換え。各 test の `newWebRouter` を「SessionMiddleware + AuthMiddleware」構成に切り替える。本番 router は依然 DummyAuth |
| 4c | 本番 router を実 AuthMiddleware に切り替え + `DummyAuthMiddleware` / `RoleCookieName` / `POST /__set_role` 削除 |

本 PR では `internal/handler/middleware/` に新関数を足すだけで、`internal/handler/router.go`
の DummyAuthMiddleware 使用も含めて触らない。「既存挙動を壊さない追加 PR」に閉じる。

## 仕様書からの制約

- §7.2「ハンドラの前段ミドルウェアで: (1) セッションからログインユーザ特定、(2) `user_department_roles` を引き保有ロール・部署を取得、(3) リクエストパスとデータの所属部署を照らし許可 / 拒否を判定」
- §7.1 5 ロール (`system_admin` / `department_security_admin` / `license_manager` / `viewer` / `general_user`)
- §6.1「`/login` は不要、それ以外は認証済」
- §11 受け入れ基準 11「一般社員アカウントでログインすると `/my/licenses` で自分に割り当てられたライセンスが見える」

仕様書の (3) (部署別認可) は本 PR 範囲外。Phase 3 では「最も権限の高い `role` を 1 つ context に詰める」までで、部署別判定は別 PR (Phase 4 以降) で扱う。`RequireRole(...)` の既存 API が単一 Role 受けなので、まずは既存 API を生かす形で乗せる。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| 配置 | `internal/handler/middleware/session_auth.go` を新設し `AuthMiddleware` / `AuthConfig` を定義 | 既存 `auth.go` (DummyAuth) と分離して 4c での削除を簡単にする |
| 依存 | `SessionFrom(ctx)` に依存 (= SessionMiddleware が前段で必須)、DB は `*sql.DB` で注入 | DB を sqlc Queries で wrap する |
| 未認証時 | リクエストパスが公開パス (`/login` / `/static/*` / `/healthz`) でなければ `/login?next=<original>` に 303 redirect | 仕様書 §6.1 / §6.2 通り、Cookie に next を埋めるパターンは MVP では不採用 |
| 公開パスの指定 | `AuthConfig.PublicPathPrefixes []string` で受ける (デフォルト `["/login", "/static/", "/healthz"]`) | regex を入れない (CLAUDE.md 早すぎる抽象化) |
| 認証済かつ role 0 件 | 403 Forbidden | 「アカウントは存在するが権限が無い」を redirect ループにしない |
| 複数 role の優先順 | `middleware.AllRoles()` の順序 (system_admin → general_user) で最も高いものを採用 | 既存 RoleFrom() / RequireRole() の単一 Role 受けに合わせる |
| 既存 `RoleFrom(ctx)` | 既存実装を生かす。AuthMiddleware は同じ `roleKey{}` で詰める | handler 側は一切変更不要 |
| 新 SQL | `db/queries/user_department_roles.sql` に `ListActiveRolesForAppUser` 追加 (revoked_at IS NULL) | 認可ロジックの中核クエリ |
| エラーハンドリング | DB lookup 失敗は 500 + ログ。SessionFrom が nil なら 500 (= SessionMiddleware 未配置の bug) | fail-fast |
| AuthenticatedAs helper | `handlertest.AuthenticatedAs(t, db, store, role) *http.Cookie` を追加。app_users 1 行 + user_department_roles 1 行 + session 1 行を INSERT して Cookie を返す | 4b で 14 test ファイルを一括書き換えるための土台 |
| AuthenticatedAs の app_user 命名 | `test_<role>_<counter>` でユニーク化 | t.Parallel() でも衝突しないように atomic.Counter で連番 |
| AuthenticatedAs の department | NULL (= 全社スコープ) で固定 | 部署別認可は本 PR 範囲外。tests 全部 system_admin 相当のスコープでも動く |
| ログ | 認証 redirect 時 `info` (path), 403 時 `warn` (app_user_id), DB 失敗時 `error`。session ID / app_user_id 全文はログに出さない | 仕様 §8.3 |

## 対象スコープ

### 範囲内

- `db/queries/user_department_roles.sql`: `ListActiveRolesForAppUser :many` を追加
- sqlc 再生成
- `internal/handler/middleware/session_auth.go`: `AuthMiddleware` + `AuthConfig` + 内部ヘルパ
- `internal/handler/middleware/session_auth_test.go`: 公開パス素通し、未認証 redirect、role 解決、複数 role の最高権限選択、403 (role 0 件)、SessionFrom nil パニック
- `internal/handler/handlertest/handlertest.go` (もしくは新ファイル): `AuthenticatedAs(t, db, store, role) *http.Cookie`
- `internal/handler/handlertest/auth_test.go` (新規): `AuthenticatedAs` の動作テスト

### 範囲外 (別 PR)

- 既存 handler テスト (vendors / products / departments / users / devices / aliases) の書き換え (4b)
- 本番 `router.go` の `DummyAuthMiddleware` → `AuthMiddleware` 切替 (4c)
- `DummyAuthMiddleware` / `RoleCookieName` / `POST /__set_role` 削除 (4c)
- 部署別認可 (Phase 4 以降)
- LDAPAuthenticator (LDAP PR)

## 内部設計

### `AuthConfig` と `AuthMiddleware`

```go
type AuthConfig struct {
    DB                 *sql.DB
    Logger             *slog.Logger
    LoginURL           string   // default "/login"
    PublicPathPrefixes []string // default ["/static/", "/healthz"] + LoginURL.Path
}

// AuthMiddleware は SessionMiddleware の後段で動く。SessionFrom(ctx) が
// nil ならリクエスト単位で 500 + error ログ (router 組立順のミス検出)。
// 起動時 panic にしないのは、ハンドラ毎に SessionMiddleware を貼らない
// ルート (例: /healthz) を許容するため。
func AuthMiddleware(cfg AuthConfig) func(http.Handler) http.Handler
```

挙動:

```text
1. 公開パスは素通り
2. session := SessionFrom(ctx)
   - nil なら 500 + error ログ (router 組立ミス)
3. session.AppUserID == nil なら 303 to LoginURL + ?next=<escaped(path+rawQuery)>
4. q.ListActiveRolesForAppUser(ctx, appUserID)
   - エラーなら 500
   - 0 件なら 403 Forbidden
5. highest := pickHighestRole(rows) // AllRoles() 順
6. ctx = context.WithValue(ctx, roleKey{}, highest)
7. next.ServeHTTP(w, r.WithContext(ctx))
```

### 新クエリ `ListActiveRolesForAppUser`

```sql
-- name: ListActiveRolesForAppUser :many
SELECT
  role,
  department_id
FROM user_department_roles
WHERE app_user_id = ?
  AND revoked_at IS NULL;
```

部署別認可は今回扱わないが department_id も併せて返すのは「ハンドラ層で department_id を見て『自部署のみ閲覧可』分岐」する将来拡張のため。本 PR の middleware はあえて `role` 列のみ使う。

### `pickHighestRole`

```go
// AllRoles() の先頭 (= system_admin) から順に rows を走査して最初に
// マッチした Role を返す。1 件も該当しなければ "" を返し、呼び出し側で
// 403 を返す。
func pickHighestRole(rows []repository.ListActiveRolesForAppUserRow) middleware.Role
```

`middleware.IsValidRole` でフィルタする。DB に未知の文字列が入っていたら無視 (= 防御的)。

### `AuthenticatedAs` helper

```go
// AuthenticatedAs はテスト用に「role を持つ app_user」と「その session」を
// DB に作成し、session Cookie を返す。テストは httptest.NewRequest に
// AddCookie(returned) するだけで認証済リクエストを組み立てられる。
func AuthenticatedAs(t *testing.T, db *sql.DB, store session.Store, role middleware.Role) *http.Cookie
```

具体実装:

1. `atomic` カウンタで `username = fmt.Sprintf("test_%s_%d", role, counter)` を組み立てる
2. `INSERT INTO app_users (username, password_hash, auth_type) VALUES (?, '', 'local')` — password_hash は空文字 (ログイン経由しないので使わない)
3. `INSERT INTO user_department_roles (app_user_id, department_id, role) VALUES (?, NULL, ?)`
4. session.NewID() / NewCSRFToken() で生成
5. `store.Create(ctx, Session{ID, AppUserID: &appUserID, CSRFToken, ExpiresAt: now+1h})`
6. `return &http.Cookie{Name: session.CookieName, Value: id}`

## TDD コミット順序

1. `docs(plans): 実 AuthMiddleware の Plan ファイル`
2. `feat(db/queries): ListActiveRolesForAppUser 追加 + sqlc 再生成`
3. `test(middleware): AuthMiddleware の各種挙動 (RED)`
4. `feat(middleware): AuthMiddleware 実装 (GREEN)`
5. `test(handlertest): AuthenticatedAs の動作 (RED)`
6. `feat(handlertest): AuthenticatedAs 実装 (GREEN)`

## 受け入れ基準

- `make build` / `make test` / `go test -race ./...` / `make lint` 全緑
- `AuthMiddleware`:
  - 公開パス (`/login`, `/static/foo.css`, `/healthz`) は素通り
  - SessionFrom nil で 500
  - session.AppUserID nil で `/login?next=...` に 303
  - role 0 件で 403
  - 1 role でその role が context に詰まる
  - 複数 role で `AllRoles()` 順最高が選ばれる
- `AuthenticatedAs`:
  - 戻り値 Cookie で実際に `SessionMiddleware + AuthMiddleware` を通せる
  - 異なる role 指定で異なる app_user が作られる (parallel safe)
- 既存 `make test` が全 pass (= 既存 router / 既存 test に副作用なし)

## 想定リスク

- **複数 role の最高権限選択がハンドラ側ロジックと合わない**: 既存 handler テストは「system_admin で全部見える」前提で書かれているため、4b 移行時に「viewer 持ちの user で /vendors を見て 200」期待などが続けば現状と一致する。本 PR では問題にならない
- **DB クエリ追加によるスキーマバージョン**: クエリ追加だけで migration 追加はないので version 据え置き
- **AuthMiddleware が SessionMiddleware 必須前提**: panic で fail-fast。本番 router は 4c で順序保証する
