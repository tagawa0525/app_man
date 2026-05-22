# appmgr-create-app-user 本実装 (Phase 3 第 1 PR)

## Context

要件書 §4.2 / §7.1 で必須と規定された `appmgr-create-app-user` (`cmd/create-app-user/main.go`) は現状 20 行の placeholder で、`flag.String` を 4 本宣言してメッセージを 1 行出すだけ。フェーズ 3 でログイン UI / セッション機構を導入する前段として、初期 `system_admin` を CLI から作れる状態は必須 (画面が無い時点では他に注入経路が無い)。

本 PR では create-app-user の **本実装のみ** をスコープに切り、bcrypt ハッシュ生成・パスワード入力 (env / stdin)・ロール付与 (`user_department_roles` INSERT)・`--reset-password` を一括で揃える。`import-bootstrap` で確立した「placeholder main を独立実装に置換、共通機能 (config / applog / lockfile / db) は流用、本体ロジックは `internal/` パッケージに切り出してテスト可能にする」パターンを踏襲する。

ログイン画面・POST /login・セッション機構・本物の AuthMiddleware 差し替えはすべて別 PR。本 PR の `internal/auth` パッケージはそれら後続 PR から再利用される interface (`Hash` / `Verify`) を最小限提供する。

### 本番運用での認証情報の所在 (仕様書 §7.3 / §4.2)

| ユーザ種別 | `auth_type` | `password_hash` | 認証経路 | レコード作成元 |
| --- | --- | --- | --- | --- |
| 一般社員 (大多数) | `'ad'` | **`NULL`** (app_man 側は認証情報を一切持たない) | AD パススルー認証 (LDAP バインド) | Phase 4 の `appmgr-sync-directory` が自動作成 |
| システム管理用ローカルアカウント (少数) | `'local'` | bcrypt ハッシュ | `LocalAuthenticator` (本 PR の `auth.Verify`) | 本 PR の `appmgr-create-app-user` |

ローカルアカウントを持つ理由は (a) 初期セットアップ時 (AD 同期前) に system_admin が必要、(b) AD 停止時の緊急管理経路の確保、(c) AD アカウントを持たないシステム管理者用、の 3 点。一般社員のログインは恒久的に AD 任せで、`password_hash` を app_man に保存することは無い。

したがって `create-app-user` の責務は「**ローカルアカウントの作成と password リセット**」に閉じる。仕様書 §7.3 でも `appmgr-create-app-user --reset-password` の用途は「ローカル admin のパスワードリセット」と明記。一般社員アカウントの量産は AD 同期 PR (Phase 4) が `auth_type='ad'` + `password_hash=NULL` + `general_user` ロール自動付与の形で担う。

## 主要決定

| 項目 | 決定 | 判断 |
| --- | --- | --- |
| bcrypt 配置 | `internal/auth/password.go` を新設し `Hash` / `Verify` を export | 後続のログイン handler で `Verify` を必ず使う。cmd に閉じ込めると cmd → internal 逆参照になり再利用できない |
| bcrypt cost | `const DefaultCost = bcrypt.DefaultCost` (= 10) | 将来チューニング時 1 箇所差し替え。env で変える需要は今は無い |
| パスワード入力源 | env `APPMGR_INITIAL_PASSWORD` 優先、無ければ `golang.org/x/term.ReadPassword` で stdin から 2 回読んで一致確認 | env は CI / docker で再現可能、対話は人間オペレータ用 |
| TTY 非対応時 | `term.IsTerminal(int(os.Stdin.Fd()))` が false かつ env 未設定 → exit 3 で「`APPMGR_INITIAL_PASSWORD` を設定するか TTY から実行してください」 | パイプ越しに空入力でアカウント作成、を発生させない |
| パスワード最低長 | 8 文字以上 (`const MinPasswordLength = 8`、設定で変更不可) | bcrypt 上限 72 byte のみで弱い PW 防止が無いので自前で最低長検証 |
| flag 排他 | `--reset-password` 指定時に `--role` / `--department-code` / `--notify-email` が指定されていたら警告ログを出して**無視** (エラーにはしない) | `history` から再呼出ししたとき微差で死ぬのは UX 悪い |
| 必須/排他検証 | `flag.Parse` 後の自前 `validateFlags(opts) error` を runner.go に切り出してテスト可能化 | main から分離 |
| department-code 解決 | 既存 `GetDepartmentByCode` (確認済み) を使う。`valid_to IS NOT NULL` (廃止済み) は **拒否** (exit 3) | 廃止部署への付与は運用事故。override flag は需要発生時に追加 |
| トランザクション境界 | create: `app_users` INSERT と `user_department_roles` INSERT を 1 tx | 部分挿入 (app_users だけ作成されてロール無し、username UNIQUE のせいで再実行不能) を防ぐ |
| reset の tx | 不要 (UPDATE 1 件) | シンプルさ優先 |
| 重複検出 | create: `GetAppUserByUsername` が nil → 既存ありで exit 1、`sql.ErrNoRows` → OK、それ以外 → lookup error で exit 1 | bootstrap 直近 fix (`c8d2d85` 等) と同じ「DB エラー握りつぶさない」方針 |
| reset の存在確認 | reset: `sql.ErrNoRows` → 存在しないで exit 1、nil → OK、それ以外 → lookup error | create と対称 |
| lockfile mode | `lockfile.ModeShared` (※ `ModeBatch` は存在しない。`import-bootstrap` も `ModeShared`) | backup 専用の `ModeGlobal` 以外を取れば import-bootstrap と同流儀 |
| `batchBinaries` への追記 | `internal/lockfile/lockfile.go` の `batchBinaries` に `"appmgr-create-app-user"` を末尾追加 | 追加しないと `appmgr-backup` (ModeGlobal) が create-app-user を排他対象から外し、create-app-user 実行中に backup が走る穴が空く |
| clirun 経由か独立か | **独立実装** (import-bootstrap と同じ) | `clirun.Run` は flag 受け取り口を持たない。`clirun.RunWithFlags` 追加は本 PR スコープ外 |
| 本体ロジック分離 | `cmd/create-app-user/runner.go` に `run(args, stdin, stdout, stderr, getenv) int` を切る | main は `os.Exit(run(...))` だけ。runner_test.go で in-memory sqlite を渡してテスト |
| exit code | 0 OK / 1 handler error / 2 lock conflict / 3 config invalid | import-bootstrap と同一規約 |
| 新規 SQL | `app_users.sql` に `UpdateAppUserPasswordHash`、`user_department_roles.sql` (新規) に `CreateUserDepartmentRole` のみ | List / Revoke は Grant/Revoke UI PR と同 PR |
| audit_logs 書込 | しない (別 PR) | runner の tx Commit 後に hook 挿入点 (コメント) だけ残す |
| 構造化ログ | `applog.New(cfg.Logging, "appmgr-create-app-user")`。`username` / `role` / `department_code` / `mode=create\|reset` を attribute に。**パスワード平文 / ハッシュは絶対にログに出さない** | 監査要件 |
| `notify_email` の扱い | 空欄は警告ログのみでエラーにしない | 仕様書 §4.2 「実質必須」だが、reset で後付けする運用余地を残す |
| `IsValidRole` 呼び出し | `middleware.IsValidRole(middleware.Role(opts.role))` で型変換してから呼ぶ | `IsValidRole` は `Role` (string ベース type) を受ける |

## 対象スコープ

### 範囲内

- `internal/auth/` パッケージ新設 (`password.go` + `password_test.go`)
- `cmd/create-app-user/main.go` 本実装置換
- `cmd/create-app-user/runner.go` (本体ロジック切出し) + `runner_test.go`
- `db/queries/app_users.sql` に `UpdateAppUserPasswordHash` 追加
- `db/queries/user_department_roles.sql` 新規 (`CreateUserDepartmentRole`)
- sqlc 再生成 (`internal/repository/app_users.sql.go`, `user_department_roles.sql.go`)
- `internal/lockfile/lockfile.go` の `batchBinaries` に `appmgr-create-app-user` 追記
- `go.mod` / `go.sum` 更新 (`golang.org/x/crypto/bcrypt`, `golang.org/x/term`)

### 範囲外

- ログイン画面 / POST /login (別 PR)
- セッション機構・cookie・server-side store (別 PR)
- `DummyAuthMiddleware` → 本物 `AuthMiddleware` 差し替え (別 PR)
- `user_department_roles` の Grant / Revoke 画面・CLI (別 PR)
- AD 認証 / LDAP bind (Phase 4 別 PR)
- `audit_logs` への記録 (audit_logs PR と同時)
- ユーザ無効化 (`disabled_at`) CLI (別 PR)
- `linked_user_id` 紐付け (= 既存社員を local admin に昇格) CLI (AD 同期後)

## 内部設計

### パッケージ構成

```text
internal/auth/
    password.go         # Hash / Verify / DefaultCost / MinPasswordLength
    password_test.go    # round-trip / wrong password / too short

cmd/create-app-user/
    main.go             # os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv)) のみ
    runner.go           # 本体: flag parse → validate → lock → DB open → create or reset
    runner_test.go      # in-memory sqlite で migrate → 各シナリオを runner 直叩き
```

### 関数シグネチャ

```go
// internal/auth/password.go
package auth

const (
    DefaultCost       = bcrypt.DefaultCost // = 10
    MinPasswordLength = 8
)

var (
    ErrPasswordTooShort = errors.New("password too short")
    ErrPasswordMismatch = errors.New("password mismatch")
)

// Hash は plaintext を bcrypt cost=DefaultCost でハッシュ化する。
// MinPasswordLength 未満なら ErrPasswordTooShort を返す。
func Hash(plaintext string) (string, error)

// Verify は hash と plaintext を bcrypt.CompareHashAndPassword で照合する。
// 不一致は ErrPasswordMismatch (bcrypt sentinel をラップ)。
func Verify(hash, plaintext string) error
```

```go
// cmd/create-app-user/runner.go
package main

type runOptions struct {
    configPath     string
    username       string
    role           string // middleware.IsValidRole(middleware.Role(s)) で検証
    departmentCode string
    notifyEmail    string
    resetPassword  bool
}

// run はテスト可能本体。
func run(
    args []string,
    stdin io.Reader,
    stdout, stderr io.Writer,
    getenv func(string) string,
) int

// readPassword は env 優先 → stdin プロンプト → 2 回入力一致確認の順で取得。
// stdin が *os.File なら term.ReadPassword、それ以外 (テストの bytes.Buffer)
// なら bufio.Scanner で 1 行読む (テスト容易性のため)。
func readPassword(stdin io.Reader, stdout io.Writer, getenv func(string) string) (string, error)

// createUser は app_users INSERT + user_department_roles INSERT を 1 tx で実行。
func createUser(ctx context.Context, db *sql.DB, opts runOptions, passwordHash string) error

// resetPassword は password_hash UPDATE を実行。
func resetPassword(ctx context.Context, db *sql.DB, username, passwordHash string) error

// resolveDepartmentID は code → id 解決。system_admin は (nil, nil) を返す。
// valid_to IS NOT NULL (廃止済み) はエラー。
func resolveDepartmentID(ctx context.Context, q *repository.Queries, role, code string) (*int64, error)
```

### フロー (create モード)

1. `run(args, ...)` 開始、`flag.NewFlagSet` で `--config` / `--username` / `--role` / `--department-code` / `--notify-email` / `--reset-password` を登録
2. `validateFlags(opts)`:
   - `--username` 必須 (両モード共通)
   - create: `middleware.IsValidRole(middleware.Role(opts.role))` を通る
   - create: role が `system_admin` 以外 → `--department-code` 必須
3. `config.Load`、`applog.New`、`lockfile.Acquire(..., ModeShared)`、`db.Open`、`signal.NotifyContext`
4. `q := repository.New(sqlDB)` で既存ユーザチェック (create なら ErrNoRows 期待、reset なら逆)
5. `resolveDepartmentID` (create のみ)
6. `readPassword` でパスワード取得 → `auth.Hash`
7. create: `sqlDB.BeginTx` → `q.WithTx(tx).CreateAppUser` → `q.WithTx(tx).CreateUserDepartmentRole` → `tx.Commit` (失敗時は Rollback、stderr に「rolled back」)
8. reset: `q.UpdateAppUserPasswordHash` 1 発
9. 成功時 stdout に `created app_user id=N username=admin role=system_admin` 等を 1 行

### SQL 追加

```sql
-- db/queries/app_users.sql に追加
-- name: UpdateAppUserPasswordHash :execrows
UPDATE app_users
SET password_hash = ?
WHERE username = ?;
```

```sql
-- db/queries/user_department_roles.sql (新規ファイル)
-- name: CreateUserDepartmentRole :one
INSERT INTO user_department_roles (
  app_user_id,
  department_id,
  role
) VALUES (
  ?, ?, ?
)
RETURNING id, app_user_id, department_id, role, granted_at, revoked_at;
```

`UPDATE ... WHERE username = ?` を選ぶ理由: reset 時に id を引いてから UPDATE する 2 ステップを 1 ステップに削減 (上流の `GetAppUserByUsername` チェックは「不存在ならその場で exit 1」のメッセージング目的で別に残す)。

### lockfile への追記

`internal/lockfile/lockfile.go` の `batchBinaries` (現在 8 件) の末尾に `"appmgr-create-app-user"` を追加。同パッケージのテストで `batchBinaries` の件数・内容を assert しているテストがあれば期待値更新。

## ファイル構成

| パス | 概要 | 区分 |
| --- | --- | --- |
| `docs/plans/create-app-user-core.md` (rename 後) | 本 Plan | 新規 |
| `internal/auth/password.go` | bcrypt Hash / Verify + constants | 新規 |
| `internal/auth/password_test.go` | round-trip / wrong password / too short | 新規 |
| `db/queries/app_users.sql` | `UpdateAppUserPasswordHash` 追加 | 編集 |
| `db/queries/user_department_roles.sql` | `CreateUserDepartmentRole` のみ | 新規 |
| `internal/repository/app_users.sql.go` | sqlc 再生成 | 編集 (生成) |
| `internal/repository/user_department_roles.sql.go` | sqlc 再生成 | 新規 (生成) |
| `internal/lockfile/lockfile.go` | `batchBinaries` に追記 | 編集 |
| `internal/lockfile/lockfile_test.go` | batchBinaries 期待値更新 (該当テストがあれば) | 編集 (条件付き) |
| `cmd/create-app-user/main.go` | placeholder を本実装に置換 (薄いエントリポイント) | 編集 |
| `cmd/create-app-user/runner.go` | 本体ロジック (flag / validate / create / reset) | 新規 |
| `cmd/create-app-user/runner_test.go` | in-memory sqlite で create / reset / 検証エラー | 新規 |
| `go.mod` / `go.sum` | `golang.org/x/crypto`, `golang.org/x/term` 追加 | 編集 |

## コミット列 (TDD サイクル)

ブランチ: `feat/create-app-user`。CLAUDE.md「最初のコミットを Plan ファイル」規約に従う。

| # | コミット件名 |
| --- | --- |
| 1 | `docs(plans): create-app-user 本実装の Plan ファイル` (= Plan ファイルを rename して追加) |
| 2 | `test(auth): bcrypt Hash + Verify の round-trip と too-short / mismatch (RED)` |
| 3 | `feat(auth): bcrypt ベースの password helper を新設 (GREEN)` |
| 4 | `feat(db/queries): UpdateAppUserPasswordHash + CreateUserDepartmentRole` (sqlc 再生成含む) |
| 5 | `feat(lockfile): batchBinaries に appmgr-create-app-user を追加` |
| 6 | `test(create-app-user): runner — system_admin の create が app_users + user_department_roles に INSERT (RED)` |
| 7 | `feat(create-app-user): runner の create 系本体実装 (GREEN)` |
| 8 | `test+feat(create-app-user): --department-code 解決と廃止済み拒否` |
| 9 | `test+feat(create-app-user): --reset-password モード` |
| 10 | `test+feat(create-app-user): username 重複 / 存在しない / 役割不正 / 部署無しの検証エラー` |
| 11 | `test+feat(create-app-user): readPassword の env 優先 + TTY 必須 + 8 文字未満拒否` |
| 12 | `feat(cmd/create-app-user): main を本実装に置換 (runner エントリポイント化)` |

`import-bootstrap` 同様、kind 別に「テスト追加と最小実装を 1 コミットにまとめる」を許容して 12 コミット程度に収める。コミット 4 (sqlc) はクエリ追加と生成物を 1 コミットにする (CLAUDE.md「sqlc 生成物はコミットする」)。

## 受け入れ基準

- [ ] `make generate` 後 `git status` クリーン
- [ ] `make build` / `make test` / `go test -race ./...` / `make lint` 全緑
- [ ] `APPMGR_INITIAL_PASSWORD='secret' ./bin/appmgr-create-app-user --config config.yml --username admin --role system_admin --notify-email admin@example.com` で `app_users` 1 行 + `user_department_roles` 1 行 (`department_id IS NULL`, `role='system_admin'`) が INSERT される
- [ ] `./bin/appmgr-create-app-user --config config.yml --username sato_lm --role license_manager --department-code DEPT010 --notify-email sato@example.com` (env 未設定) は `Password:` プロンプトを 2 回出して echo 抑制で読む
- [ ] `APPMGR_INITIAL_PASSWORD='newpw' ./bin/appmgr-create-app-user --config config.yml --reset-password --username admin` で `password_hash` が更新される (`auth.Verify(newHash, "newpw") == nil`)
- [ ] パイプ越し (env 未設定) は exit 3 で「`APPMGR_INITIAL_PASSWORD` を設定するか TTY から実行してください」出力
- [ ] `--role unknown_role` は exit 3 で role 一覧エラー
- [ ] `--role license_manager` で `--department-code` 未指定は exit 3
- [ ] `--role license_manager --department-code NOSUCH` は exit 3 (lookup エラー)
- [ ] 廃止済み (`valid_to IS NOT NULL`) 部署を指定したら exit 3
- [ ] 既存 username で create 再実行は exit 1 (`既に存在します`)
- [ ] 存在しない username で reset は exit 1 (`存在しません`)
- [ ] 4 文字パスワード (env or stdin) は exit 3 (`8 文字以上`)
- [ ] create 中に rollback すべき経路 (テストで人為的に `user_department_roles` INSERT を失敗させ、`app_users` にも残らないことを検証)
- [ ] 構造化ログに password 平文 / hash が出ていない (テストで logger 出力を assert)
- [ ] PR 本文に「ログイン画面 / セッション / Grant・Revoke UI / AD 同期 / audit_logs は別 PR」と明記

## 動作検証手順

```sh
nix develop
make generate              # sqlc 再生成
make build
make dev-seed              # = migrate-up → 6 kind を順次 --commit (departments も投入される)

# 1) 初期 system_admin (env)
APPMGR_INITIAL_PASSWORD='AdminPass1' ./bin/appmgr-create-app-user \
    --config config.yml \
    --username admin \
    --role system_admin \
    --notify-email admin@example.com
# 期待: stdout に "created app_user id=1 username=admin role=system_admin"

sqlite3 data/app.db "SELECT id, username, auth_type, notify_email FROM app_users;"
sqlite3 data/app.db "SELECT app_user_id, department_id, role FROM user_department_roles;"
# 期待: department_id IS NULL, role='system_admin'

# 2) license_manager (stdin プロンプト)
./bin/appmgr-create-app-user --config config.yml \
    --username sato_lm \
    --role license_manager \
    --department-code DEPT010 \
    --notify-email sato@example.com
# 期待: "Password: " と "Password (again): " を出して echo 抑制で受付

# 3) reset-password
APPMGR_INITIAL_PASSWORD='NewAdminPass2' ./bin/appmgr-create-app-user \
    --config config.yml \
    --reset-password --username admin
sqlite3 data/app.db "SELECT password_hash FROM app_users WHERE username='admin';"

# 4) エラーケース
./bin/appmgr-create-app-user --config config.yml --username admin --role system_admin
# exit=1 (重複)

./bin/appmgr-create-app-user --config config.yml --username x --role license_manager
# exit=3 (--department-code 必須)

./bin/appmgr-create-app-user --config config.yml --username x --role license_manager --department-code NOSUCH
# exit=3 (部署無し)

APPMGR_INITIAL_PASSWORD='shrt' ./bin/appmgr-create-app-user --config config.yml --username x --role system_admin
# exit=3 (8 文字未満)
```

## 後続 PR の準備 (本 PR で固める interface)

| 後続 PR | 再利用される本 PR の成果物 |
| --- | --- |
| ログイン handler (POST /login) | `auth.Verify(hash, plaintext)` をそのまま呼ぶ。`ErrPasswordMismatch` を 401 にマップ |
| セッション機構 | `repository.GetAppUserByUsername` (既存) + `auth.Verify` で認証。本 PR では変更しない |
| 本物 AuthMiddleware | `middleware.RoleFrom(ctx)` の interface は維持。本 PR では middleware 側を触らない (DummyAuth のまま) |
| Grant / Revoke UI | `repository.CreateUserDepartmentRole` (本 PR 追加) を再利用。`RevokeUserDepartmentRole` は UI PR と同時に追加 |
| audit_logs PR | create / reset 成功時に `app_user.create` / `app_user.password_reset` action を書き込むよう本 PR の runner に audit hook 引数を後付け。本 PR では tx Commit 直後にフック挿入点 (コメント) を残す |
| ユーザ無効化 CLI | 本 PR の runner.go パターン (flag → validate → lock → tx → 1 件更新) を sibling バイナリにコピー |
