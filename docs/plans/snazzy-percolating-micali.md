# フェーズ 2 PR-A — Web 基盤 (templ + HTMX + ダミー認可 + 共通レイアウト)

## Context

フェーズ 1 (PR1〜PR3) で「サーバ骨格 / DB 24 テーブル / lock 基盤 + 8 バッチ CLI + CI」が揃い、11 バイナリが `make build` で生成できる状態 (`Makefile:5-17`、`cmd/server/main.go:99-108`)。現時点で Chi ルータには `/healthz` しか登録されておらず、業務ハンドラ (products / departments / users / devices / product_aliases) は 0。

フェーズ 2「マスタ系」は仕様書 §12 (`docs/specs/02_要件定義.md:1452-1469`) で 5 テーブル群の CRUD + 画面が定義されており、テーブル単位で PR を分割する方針：

- **PR-A**: Web 基盤 (本 PR)
- PR-B: vendors + products + product_aliases
- PR-C: departments
- PR-D: users
- PR-E: devices

本 PR は **業務ハンドラを 1 つも追加せず**、後続 PR が「ファイルを 1 つ足せば templ + HTMX + 認可 + CSRF が乗った画面が出る」状態の素地を作る。認証 (フェーズ 3) 未実装のため `X-User-Role` HTTP ヘッダから役割を取り出すダミー認可ミドルウェアを入れ、CSRF も固定ダミートークン検証のみ。フェーズ 3 でセッション + 本物 CSRF に差し替える際に、handler / template / test を触らずに済むようインタフェースを先に固める。

## 主要決定

| 項目 | 決定 | 根拠 |
|---|---|---|
| templ バージョン | `github.com/a-h/templ` を `go.mod` で固定 (devShell 同梱の `templ` バイナリと同系統 `v0.3.x` 最新) | `flake.nix:24` に既に `templ` あり。再現可能ビルドのため明示固定 |
| templ 生成物の扱い | **コミットする** (sqlc 生成物と同方針) | `CLAUDE.md` 「sqlc 生成物の扱い」セクション。`make generate` で再生成、CI では走らせない |
| HTMX バージョン | `v1.9.12` を `internal/view/static/htmx.min.js` に静置 | 要件書 §2「外部 CDN 依存禁止」(`docs/specs/02_要件定義.md:42`)。v2 系は stable がまだ若いため 1.9 系採用 |
| 静的アセットの置き場 | `internal/view/static/` (**`web/static/` ではない**) | `embed.FS` は Go ソースからの相対パスのみで親ディレクトリを跨げない。`cmd/server/` から `web/static/` を embed すると禁止される。`internal/view/` に置けば同階層からの単純な embed で済む。要件書 §3.1 のレイアウトは論理構成のため README で意図を記す |
| ダミー認可ヘッダ | `X-User-Role: system_admin\|department_security_admin\|license_manager\|viewer\|general_user`。未指定なら `general_user` 扱い、未知の値は 403 | 要件書 §7.1 (`docs/specs/02_要件定義.md:1143-1149`) のロール定義に揃える。フェーズ 3 でセッション値を同じ context key に詰めるだけで差し替わる |
| context key 型 | `type roleKey struct{}` の unexported zero-size 型 | revive `context-keys-type` (`.golangci.yml:24`) が string キーを禁止 |
| CSRF | GET / HEAD / OPTIONS は素通り。それ以外は `X-CSRF-Token` ヘッダ or フォームの `_csrf` フィールドが固定値 `"dummy-csrf-token"` のときのみ通す | 要件書 §8.3 (`docs/specs/02_要件定義.md:1215`) と同形のインタフェース (middleware + template hidden input) を先に作り、検証ロジックだけ仮値 |
| 共通レイアウト | `internal/view/layout/Base(props)` を templ で実装。`<head>` + ナビ (本 PR では空) + フラッシュメッセージ + `<meta name="csrf-token">` + コンテンツスロット | 要件書 §6.2 (`docs/specs/02_要件定義.md:1130-1135`) |
| ルータ切り出し | `cmd/server/main.go` の Chi 組立部分を `internal/handler/router.go` の `NewRouter(Deps) http.Handler` に独立 | 後続 PR で handler 登録を 1 ファイルにまとめて読めるようにする。lock / DB open / Shutdown 構造 (`cmd/server/main.go:64-138`) には触らない |
| エラー画面 | `internal/view/errors/NotFound.templ` / `ServerError.templ` の 2 つ。共通レイアウトを使う。chi の `r.NotFound(...)` / `middleware.Recoverer` のカスタム responder で呼ぶ | フェーズ 2 で 404/500 が綺麗に出ないとデバッグ困難 |
| テストヘルパ | `internal/handler/handlertest/` パッケージ。`NewRequest(t, method, path, role, body)` / `PostForm(t, path, role, values)` / `AssertStatus` 等 | PR-B〜PR-E で 5〜10 個の handler テストファイルが書かれる想定。CLAUDE.md「3 回重複してから抽象化」の例外: テストヘルパは形を固定する価値が大きい (本 Plan に明記して履歴に残す) |
| Makefile `generate` | 既存の `sqlc generate` に `templ generate` を追加 (`Makefile:74-75`) | sqlc 運用と平仄を合わせる |
| 静的配信ルート | `r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))` | 標準ライブラリで充足。MIME は拡張子から推定 |
| applog ロガーの handler への伝搬 | chi middleware で `r.WithContext(applog.WithLogger(ctx, logger))`、handler 側は `applog.LoggerFrom(ctx)` | グローバル `slog.SetDefault` (`cmd/server/main.go:58`) は残しつつ、request_id 等を後付けできるよう context 経由を併設 |

## コミット列 (13 個想定)

ブランチ：`feat/phase2-pr-a-web-foundation`

1. `docs(plans): フェーズ 2 PR-A Web 基盤の実装プラン` — Plan ファイル先行 (`CLAUDE.md:85-91`)
2. `chore(go.mod): templ ランタイムを追加 (バージョン固定)` — `go get github.com/a-h/templ@vX.Y.Z` と `go mod tidy`
3. `chore(make): make generate に templ generate を追加` — `Makefile:74` の `generate` 拡張。`appmgr-server` ビルド依存に `*_templ.go` を含める
4. `feat(view/static): HTMX v1.9.12 と最小 app.css を internal/view/static/ に同梱 + embed.FS で公開`
5. `test(handler): /static/htmx.min.js が 200 とバイト一致で返る` — RED
6. `feat(handler): NewRouter を internal/handler/router.go に切り出し、/static/* と /healthz を登録` — GREEN
7. `test(handler/middleware): ダミー認可ミドルウェアの role 取り出しテスト` (ヘッダなし / 既知 / 未知) — RED
8. `feat(handler/middleware): ダミー認可ミドルウェア + RequireRole + roleKey 実装` — GREEN
9. `test(handler/middleware): CSRF ミドルウェアの GET 素通り / POST 拒否 / 正トークンで通過` — RED
10. `feat(handler/middleware): CSRF ミドルウェア (固定ダミートークン検証) 実装` — GREEN
11. `feat(view/layout): Base レイアウト + ナビ + フラッシュ + CSRF meta タグの templ` — `_templ.go` も同コミット
12. `feat(view/errors): 404 / 500 用テンプレートと chi の NotFound / Recoverer 連携`
13. `feat(handler/handlertest): 共通テストヘルパ (role 付きリクエスト / CSRF 込み POST / assertion)`

`test → feat` の RED/GREEN サイクルが static / dummy-auth / CSRF の 3 箇所で履歴に残り、`cmd/server/main.go` への変更は router 切り出し (コミット 6) の 1 回だけで読める構成。

## API 設計 (要点)

### `internal/handler/router.go`

```go
package handler

type Deps struct {
    Logger   *slog.Logger
    DB       *sql.DB
    StaticFS fs.FS
    // フェーズ 3 でセッションストア・CSRF ジェネレータ・Authenticator を追加
}

func NewRouter(deps Deps) http.Handler {
    r := chi.NewRouter()
    r.Use(middleware.RequestID)
    r.Use(middleware.Recoverer)
    r.Use(LoggerMiddleware(deps.Logger))
    r.Use(DummyAuthMiddleware)
    r.Use(CSRFMiddleware)

    r.Get("/healthz", healthHandler)
    r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(deps.StaticFS))))
    r.NotFound(notFoundHandler)
    // 後続 PR で r.Route("/products", ...) 等が並ぶ
    return r
}
```

### `internal/handler/middleware/auth.go`

```go
type Role string

const (
    RoleSystemAdmin           Role = "system_admin"
    RoleDepartmentSecurityAdm Role = "department_security_admin"
    RoleLicenseManager        Role = "license_manager"
    RoleViewer                Role = "viewer"
    RoleGeneralUser           Role = "general_user"
)

type roleKey struct{}

var validRoles = map[Role]struct{}{ /* 上記 5 種 */ }

func DummyAuthMiddleware(next http.Handler) http.Handler {
    // X-User-Role 空 → general_user / 既知 → そのまま / 未知 → 403
    // context.WithValue(r.Context(), roleKey{}, role)
}

func RoleFrom(ctx context.Context) Role { /* default general_user */ }

// RequireRole は許可リストに含まれる role でのみ next を呼ぶハンドララッパ。
// 後続 PR で r.With(RequireRole(RoleSystemAdmin, RoleLicenseManager)).Get(...) で使う。
func RequireRole(allowed ...Role) func(http.Handler) http.Handler
```

### `internal/handler/middleware/csrf.go`

```go
// DummyCSRFToken はフェーズ 3 で session-bound 値に置き換える前提の固定値。
// middleware / template / handlertest の 3 箇所から参照する。
const DummyCSRFToken = "dummy-csrf-token"

// CSRFMiddleware は GET/HEAD/OPTIONS を素通りし、それ以外で
// X-CSRF-Token ヘッダ or form の _csrf 値が DummyCSRFToken と一致しなければ 403。
func CSRFMiddleware(next http.Handler) http.Handler
```

### `internal/view/layout/base.templ`

```templ
package layout

type BaseProps struct {
    Title     string
    Role      middleware.Role
    Flash     string
    CSRFToken string
}

templ Base(p BaseProps) {
    <!DOCTYPE html>
    <html lang="ja">
        <head>
            <meta charset="utf-8"/>
            <meta name="csrf-token" content={ p.CSRFToken }/>
            <title>{ p.Title } - 社内アプリ管理</title>
            <link rel="stylesheet" href="/static/css/app.css"/>
            <script src="/static/htmx.min.js"></script>
        </head>
        <body hx-headers={ `{"X-CSRF-Token":"` + p.CSRFToken + `"}` }>
            @Nav(p.Role)
            if p.Flash != "" { <div class="flash">{ p.Flash }</div> }
            { children... }
        </body>
    </html>
}

templ CSRFInput(token string) {
    <input type="hidden" name="_csrf" value={ token }/>
}
```

## ファイル構成

### 新規

- `docs/plans/snazzy-percolating-micali.md` (本 Plan)
- `internal/view/static/static.go` — `//go:embed static/*` で `fs.FS` 公開
- `internal/view/static/static/htmx.min.js` — HTMX v1.9.12 upstream から取得して同梱
- `internal/view/static/static/css/app.css` — リセット + ナビ + フラッシュの最小スタイル
- `internal/view/layout/base.templ` / `base_templ.go` — 共通レイアウト + Nav + CSRFInput
- `internal/view/errors/notfound.templ` / `notfound_templ.go`
- `internal/view/errors/server_error.templ` / `server_error_templ.go`
- `internal/handler/router.go` — `NewRouter` + `Deps`
- `internal/handler/router_test.go` — `/static/htmx.min.js` 配信 / 404 動作
- `internal/handler/middleware/auth.go` — ダミー認可 + RequireRole + roleKey
- `internal/handler/middleware/auth_test.go`
- `internal/handler/middleware/csrf.go`
- `internal/handler/middleware/csrf_test.go`
- `internal/handler/middleware/logger.go` — request 経由で logger を context に詰める
- `internal/handler/middleware/doc.go` — パッケージコメント
- `internal/handler/handlertest/handlertest.go` — test helper 群
- `internal/applog/context.go` — `WithLogger(ctx, *slog.Logger) ctx` と `LoggerFrom(ctx) *slog.Logger` の 2 関数のみ追加 (既存 `logger.go` には触らない)

### 編集

- `cmd/server/main.go` — `r := chi.NewRouter() ... r.Get("/healthz", ...)` の塊 (`cmd/server/main.go:99-103`) を `handler.NewRouter(handler.Deps{Logger: logger, DB: sqlDB, StaticFS: static.FS})` 呼び出しに差し替え。lock 取得 / DB open / signal 受け / Shutdown の構造 (`cmd/server/main.go:64-138`) には**一切触らない**
- `Makefile` — `generate:` に `templ generate` を追加 (`Makefile:74-75`)。`$(BIN_DIR)/appmgr-server` の依存条件 (`Makefile:32`) に `*_templ.go` を含める
- `go.mod` / `go.sum` — `github.com/a-h/templ` 追加

### 触らない

- `internal/lockfile/` / `internal/clirun/` / `internal/config/` / `internal/applog/logger.go` / `internal/db/` / `internal/repository/`
- `cmd/migrate/` 他バッチ系 — Web 基盤と無関係

## Verification

### 手元での動作確認

```sh
nix develop
make generate                  # sqlc + templ generate が両方走り、生成物が更新される
git status                     # *_templ.go の差分が出ていればコミット対象
make build                     # 11 バイナリ全て生成
ls bin/ | wc -l                # 11

make test                      # 新規 handler / middleware / handlertest が緑
make lint                      # context-keys-type 含めて golangci-lint クリーン

cp config.example.yml config.yml
make migrate-up
make run &
sleep 1

# /healthz が引き続き 200
curl -sS -o /dev/null -w "%{http_code}\n" http://localhost:8080/healthz   # 200

# 静的アセット同梱の配信
curl -sS -o /tmp/htmx.js http://localhost:8080/static/htmx.min.js
diff /tmp/htmx.js internal/view/static/static/htmx.min.js                # 差分なし

# 404 のレイアウト
curl -sS -o /dev/null -w "%{http_code}\n" http://localhost:8080/no-such-path  # 404、HTML

kill %1
```

### 単体テスト

```sh
go test -run TestDummyAuthMiddleware ./internal/handler/middleware/   # 3 サブテスト全緑
go test -run TestCSRFMiddleware      ./internal/handler/middleware/   # 3 サブテスト全緑
go test -race ./...                                                    # race 無し
```

### クロスコンパイル確認

```sh
GOOS=windows GOARCH=amd64 go build ./cmd/...   # windows 側ビルドも通る (embed.FS は OS 非依存)
```

### CI

`.github/workflows/ci.yml` は無修正で 4 ジョブ全緑 (build / test / race / lint)。CI で `templ generate` は走らせない (生成物コミット運用 = sqlc と同じ)。

## 留意点

- **業務ハンドラを 1 つも追加しない**。products / departments / users / devices / product_aliases の handler は PR-B 以降に分離。レビュー時のチェックポイント
- **`internal/view/static/` への配置**は要件書 §3.1 の `web/static/` から逸脱するが、embed.FS の制約のため。README または本 Plan で意図を明記
- **context key 衝突回避**：`type roleKey struct{}` の 0 サイズ unexported 型で revive `context-keys-type` 警告も同時クリア
- **CSRF 固定トークン**は middleware / handlertest / templ テンプレートの 3 箇所から参照される。`middleware.DummyCSRFToken` の 1 定数を export し全箇所が同じ値を使う。フェーズ 3 で「session-bound のジェネレータ + 検証ロジック差し替え」だけで済むよう、テンプレートは props 経由で受け取る (定数を直接埋め込まない)
- **フェーズ 1 の lock 排他制御を壊さない**：`cmd/server/main.go:64-77` の `lockfile.Acquire(...) // ModeServer` と `cmd/server/main.go:79-95` の DB open / defer 登録順には触らない。router 組立部分だけを切り出す
- **ロガー伝搬の二段構え**：`slog.SetDefault` (`cmd/server/main.go:58`) を残しつつ、handler コンテキスト経由でも参照可能にする。`internal/applog/context.go` に `WithLogger` / `LoggerFrom` の 2 関数のみ追加、既存 `applog/logger.go:21-46` の `New` には触らない
- **HTMX バージョンの再現性**：upstream 正規 URL から取得し、ファイル冒頭の version コメント行を保ったまま同梱。レビューで version が読める状態にする
- **マージコミット例** (Linus 方式)：
  - **Why**: フェーズ 2 のテーブル単位 PR (B〜E) が共通で使う Web 基盤を先出し
  - **What**: templ + HTMX 同梱、ダミー認可ミドルウェア (X-User-Role → context)、CSRF (固定トークン版)、共通レイアウト、router 切り出し、handler テストヘルパ
  - **Impact**: PR-B 以降は handler ファイル追加だけで templ + HTMX + 認可 + CSRF が乗った画面が出る。フェーズ 3 でセッション + 本物 CSRF に差し替え予定

## フェーズ 2 全体のロードマップ

PR-A 完了後、以下 4 PR で「マスタ系」全 5 テーブル群の MVP を完成させる：

- **PR-B (vendors + products + product_aliases)** — 関連が深く一緒に出す方が自然。vendors は新規 / 編集、products は license_manager で自部署編集 + admin で全社承認設定 (`docs/specs/02_要件定義.md:1107, 1115`)、product_aliases は名寄せキューの承認 UI (`docs/specs/02_要件定義.md:1108`)
- **PR-C (departments)** — 一覧 + 廃止対応 (`valid_to` 設定 / 後継部署選択) のみ。新規追加は AD 同期管轄なので無し。`/admin/departments/migrate` も含む (`docs/specs/02_要件定義.md:1119`)
- **PR-D (users)** — 一覧 + 詳細のみ (CRUD なし、AD 同期管轄)。`/users` 表示 (`docs/specs/02_要件定義.md:1117`)
- **PR-E (devices)** — 一覧 + 退役対応 (`retired_at` 設定) のみ。新規追加は SKYSEA 取込管轄

各 PR は同じ TDD パターン (plan → sqlc クエリ → handler RED → handler GREEN → templ → 統合確認) で 6〜12 コミット程度に収まる想定。PR-A で作る `handlertest` / `RequireRole` / `Base` レイアウト / CSRF middleware が全 PR で再利用される。
