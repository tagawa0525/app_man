# フェーズ 1 PR3 — lock ファイル基盤 + 全 CLI スケルトン + CI

## Context

PR1 で骨組みサーバ（`internal/config`・`internal/applog` + `/healthz`）、PR2 で DB 基盤（24 テーブル + マイグレーション + sqlc + Chi 置換）が揃った。フェーズ 1「基盤整備」の最後のピースとして、**バッチ CLI の排他制御と CI** を整備し、フェーズ 2「マスタ系」へ進める土台を完成させる。

本 PR で投入するもの：

- `internal/lockfile`：`<base>/locks/<binary-name>.lock` の排他取得・解放（要件書 § 8.8）。`ModeShared`（通常）と `ModeGlobal`（`appmgr-backup` 用、他全バッチ lock を相互排他取得）の 2 モード
- `internal/clirun`：8 バッチに共通の起動ヘルパー（フラグ → config → logger → lock 取得 → 実処理 → release、exit code 規約）
- 8 バッチバイナリの骨格（`sync-directory` / `import-skysea` / `check-integrity` / `notify` / `backup` / `prune-logs` / `generate-meta` / `import-bootstrap`）
- `cmd/server` への多重起動防止 lock（バッチ系のグローバルロック対象外、別名で管理）
- `.github/workflows/ci.yml`（build / test / race / lint）
- `Makefile` の `BINARIES` 拡張

PR3 完了時点で **11 バイナリ**が `make build` で生成され、要件書受け入れ基準 11.2-5（cmd 配下の機能別ディレクトリ分割）と 18（lock の排他検証）が部分充足される。

## 主要決定

| 項目 | 決定 | 根拠 |
|---|---|---|
| Windows lock | unix を本実装（`syscall.Flock`）、windows はビルドが通るだけの error 返却スタブ | 本番は Windows Server だが、開発・CI は Linux。Windows 本実装は本番投入直前の別 PR に分離。CLAUDE.md「現在の要件に対する最小限の実装」 |
| lock 排他方式 | OS のファイルロック（`syscall.LOCK_EX\|LOCK_NB`）+ メタデータ用 PID 書込 | flock(2) はプロセス終了（正常・異常問わず）で fd と共に自動解放される。lock ファイル残骸の上から再 Acquire できる（flock が free なら成功）ため、PID 生存確認による stale 判定ロジックは入れない。PID 書込は運用時に「誰が hold 中か」を grep するための情報。CLAUDE.md「早すぎる抽象化禁止 / 現在の要件に対する最小限の実装」 |
| `ModeGlobal` の実現 | unexport な `batchBinaries` 定数（8 バッチ名）を `internal/lockfile` に定義し、`backup` 起動時は自身 + 他 7 つの lock を順次 `LOCK_EX\|LOCK_NB` で取得。1 つでも失敗したら取得済みを逆順 release して `ErrAlreadyHeld`。export しないのは、外部からスライス内容を書き換えられて ModeGlobal の対象が意図せず変わる事故を防ぐため | 共有ロック (LOCK_SH) を使う案より単純で、PID 書込・stale 検出の整合が取れる |
| `appmgr-server` の lock | `<base>/locks/appmgr-server.lock` を `lockfile.Acquire(ModeServer)` で個別取得。バッチ系のグローバルロック対象外 | 要件書 § 8.8 明記。常駐前提なので shutdown までホールドしっぱなし |
| `appmgr-migrate` / `appmgr-create-app-user` の lock | **lock 不要**（既存実装に変更を加えない） | 要件書 § 8.8 の対象列挙にも、§ 9 の運用バッチにも含まれない（前者は手動運用、後者は初期セットアップ）。`appmgr-backup` との衝突は運用ドキュメントで案内 |
| exit code 規約 | 0=正常 / 1=実行エラー / 2=lock 取得失敗 / 3=設定エラー（config 読込・logger 初期化・フラグ不正） | rustling-discovering-beaver.md PR3 確定値 |
| `clirun` の対象 | 新規 8 バッチのみ。既存の `server` / `migrate` / `create-app-user` には適用しない | サーバは shutdown context、migrate は CLI 引数仕様、create-app-user は対話入力と各々形が異なる。3 回重複してから抽象化（CLAUDE.md） |
| `--dry-run` フラグ | `clirun` 共通フラグとして定義し、`Deps.DryRun` で handler に渡す（骨格段階では handler が値を無視して問題なし） | 仕様書 § 9 で複数バッチが `--dry-run` を要求しているため共通化が妥当 |
| 骨格 handler の挙動 | 「`<binaryName>: not implemented` を logger.Info で出して nil 返却」 | 標準出力ではなくログに出すことで、タスクスケジューラ運用時の挙動を本実装と統一 |
| CI | `build` / `test` / `test -race` / `golangci-lint` の 4 ジョブ。Go バージョンは `go.mod` の 1.25 に合わせる | sqlc 生成物の差分検証は CLAUDE.md「CI 必須にしない運用」と矛盾するため含めない |
| Makefile の build ルール | 既存 3 バイナリの個別ルールはそのまま残し、新規 8 バイナリは 1 つのパターンルール `$(BIN_DIR)/appmgr-%` で記述 | サーバ・migrate は DB 系の追加依存（`internal db` 等）が必要、骨格 8 つは `cmd/<name>/*.go internal/**/*.go` で十分。重複も 3 回未満 |

## コミット列（13 個想定）

ブランチ：`feat/phase1-pr3-lock-and-cli-skeletons`

1. `docs(plans): フェーズ 1 PR3 lock 基盤 + CLI スケルトン + CI の実装プラン` — Plan ファイル先行
2. `test(lockfile): 同一 binary 名の Acquire 2 回目は ErrAlreadyHeld` — RED（`t.TempDir()` で `baseDir` を作り、同名 Acquire を 2 回呼ぶ）
3. `feat(lockfile): unix flock ベースの Acquire/Release を実装` — GREEN（`//go:build unix`、`syscall.Flock` + PID 書込）
4. `docs(plans): stale 検出を flock 自動解放で代替し別ロジックは入れない方針に変更` — Plan の揺れを履歴化
5. `test(lockfile): lock ファイル残存からの再取得（flock 自動解放）を回帰テスト化` — RED ではなく、flock 方式の挙動を documenting & 固定化
6. `test(lockfile): ModeGlobal は他全バッチ lock も同時排他取得、いずれかが取得済みなら ErrAlreadyHeld` — RED
7. `feat(lockfile): ModeGlobal + batchBinaries を実装、取得失敗時は逆順 release` — GREEN
8. `feat(lockfile): windows 側の build-tag スタブ（呼ぶと error 返却）を追加` — クロスコンパイル維持
9. `feat(clirun): 共通起動ヘルパー Run と exit code 規約を実装` — `Run(name, mode, handler)`、内部で `os.Exit` を呼ぶ
10. `feat(cmd): 8 バッチバイナリの骨格を追加` — `appmgr-backup` のみ `ModeGlobal`、他 7 つは `ModeShared`
11. `feat(server): 多重起動防止用の lock を起動時に取得` — `ModeServer`（バッチ系とは別管理）
12. `chore(make): BINARIES に 8 バッチを追加、新規分はパターンルールで一括ビルド`
13. `feat(ci): GitHub Actions で build / test / race / lint を実行`

`test` → `feat` の RED/GREEN サイクルが lockfile で 3 回履歴に残ること、cmd/server への lock 追加が骨格バイナリ追加とは別コミットで読めることがレビュー時のチェックポイント。

## API 設計（要点）

### `internal/lockfile`

```go
package lockfile

type Mode int

const (
    ModeShared Mode = iota // 自身の lock のみ取得（通常バッチ）
    ModeGlobal             // 自身 + batchBinaries 全 lock を排他取得（appmgr-backup 用）
    ModeServer             // 自身の lock のみ取得（バッチ系の Global 対象外）
)

// 要件書 § 8.8 の lock 対象。順序固定（取得 / 解放の決定性のため）。
// unexport にして外部からの書き換えを防ぐ。
var batchBinaries = []string{
    "appmgr-sync-directory",
    "appmgr-import-skysea",
    "appmgr-check-integrity",
    "appmgr-notify",
    "appmgr-backup",
    "appmgr-prune-logs",
    "appmgr-generate-meta",
    "appmgr-import-bootstrap",
}

var ErrAlreadyHeld = errors.New("lock already held by another process")

type Lock struct { /* baseDir, name, *os.File, holds []*os.File */ }

// Acquire は baseDir/<name>.lock を排他取得する。
//   - ModeShared / ModeServer: 自身の lock のみ
//   - ModeGlobal: 自身 + 他 batchBinaries の lock を順次取得
// 取得済み（他プロセス保持中）なら ErrAlreadyHeld を返す。
// 過去プロセスの lock ファイル残骸の上からも、flock が free なら成功する
// （flock(2) はプロセス終了で自動解放されるため）。
func Acquire(baseDir, name string, mode Mode) (*Lock, error)

// Release は保持中の全 fd をクローズし、lock ファイルを削除する。
func (l *Lock) Release() error
```

lock ファイルの内容は JSON 1 行：`{"pid":12345,"started_at":"2026-05-21T10:00:00+09:00","binary":"appmgr-backup"}`。PID は運用時の「誰が hold 中か」確認用で、Acquire ロジックは参照しない。

### `internal/clirun`

```go
package clirun

type Deps struct {
    Cfg    *config.Config
    Logger *slog.Logger
    DryRun bool
}

type Handler func(ctx context.Context, deps Deps) error

// Run は 8 バッチ共通の main 実装。
// 1. フラグ (--config, --dry-run) パース
// 2. config.Load → applog.New → lockfile.Acquire
// 3. signal.NotifyContext で ctx を作成し handler 呼出
// 4. handler エラー → exit 1、lock 失敗 → exit 2、設定/初期化失敗 → exit 3
// shutdown 時は handler 終了 → defer で Release → defer で closeLog の順
func Run(binaryName string, mode lockfile.Mode, handler Handler)
```

各バッチの `main.go` は 1 行：

```go
func main() {
    clirun.Run("appmgr-backup", lockfile.ModeGlobal, func(ctx context.Context, deps clirun.Deps) error {
        deps.Logger.Info("not implemented", slog.Bool("dry_run", deps.DryRun))
        return nil
    })
}
```

## Critical Files

**新規**：

- `docs/plans/glistening-plotting-garden.md`（本 Plan）
- `internal/lockfile/lockfile.go`（型・定数・エラー・共通ロジック）
- `internal/lockfile/lockfile_unix.go`（`//go:build unix`）
- `internal/lockfile/lockfile_windows.go`（`//go:build windows`、スタブ）
- `internal/lockfile/lockfile_test.go`（`//go:build unix`、integration）
- `internal/clirun/run.go`
- `internal/clirun/run_test.go`（exit code 規約のテスト、`os.Exit` をフックするため `runMain` のような内部関数を分けて検証）
- `cmd/sync-directory/main.go`
- `cmd/import-skysea/main.go`
- `cmd/check-integrity/main.go`
- `cmd/notify/main.go`
- `cmd/backup/main.go`
- `cmd/prune-logs/main.go`
- `cmd/generate-meta/main.go`
- `cmd/import-bootstrap/main.go`
- `.github/workflows/ci.yml`

**編集**：

- `cmd/server/main.go` — `db.Open` の直後（`db.CheckVersion` の前）に `lockfile.Acquire(cfg.Locks.BaseDir, "appmgr-server", lockfile.ModeServer)` を追加。`defer` 登録順は LIFO で `release → closeDB → closeLog`
- `Makefile` — `BINARIES` に 8 つ追加、`$(BIN_DIR)/appmgr-%: $(shell find cmd/% internal -type f -name '*.go') go.mod go.sum` 形式のパターンルール
- `go.mod` — 追加依存なし（`syscall` は標準）

**触らない**：

- `cmd/migrate/main.go` / `cmd/create-app-user/main.go` — lock 対象外、現状維持

## Verification

### 手元での動作確認

```sh
nix develop
make build
ls bin/ | wc -l        # 11

make test              # 全テスト緑
go test -race ./...    # race なし
make lint              # golangci-lint 緑

# lock 衝突（受け入れ基準 18 の lock 部分）
cp config.example.yml config.yml
make migrate-up

./bin/appmgr-import-skysea --config config.yml &
sleep 1
./bin/appmgr-import-skysea --config config.yml ; echo "exit=$?"   # exit=2
./bin/appmgr-backup        --config config.yml ; echo "exit=$?"   # exit=2
wait

# 逆方向：backup 実行中の他バッチ
./bin/appmgr-backup --config config.yml &
sleep 1
./bin/appmgr-import-skysea --config config.yml ; echo "exit=$?"   # exit=2
wait

# サーバ多重起動
./bin/appmgr-server --config config.yml &
sleep 1
./bin/appmgr-server --config config.yml ; echo "exit=$?"          # exit=2
kill %1

# 設定エラー (exit 3)
./bin/appmgr-notify --config /nonexistent.yml ; echo "exit=$?"    # exit=3

# lock ファイル残骸からの再取得（flock 自動解放確認）
echo '{"pid":999999,"started_at":"2026-01-01T00:00:00+09:00","binary":"appmgr-notify"}' > data/locks/appmgr-notify.lock
./bin/appmgr-notify --config config.yml ; echo "exit=$?"          # exit=0（flock が free なので成功、メタデータは上書き）
```

### CI 確認

- `.github/workflows/ci.yml` を含む PR を push → Actions タブで 4 ジョブ全て緑
- ジョブ：`build`（`go build ./cmd/...`）、`test`（`go test ./...`）、`race`（`go test -race ./...`）、`lint`（`golangci-lint run`）
- Go バージョン：`actions/setup-go@v5` で `go-version-file: go.mod` 指定（`go.mod` の 1.25 を追従）

### クロスコンパイル確認

```sh
GOOS=windows GOARCH=amd64 go build ./cmd/...   # windows 側ビルドが通る
```

windows スタブが import エラー / 未使用 import を起こさないことの確認。

## 留意点

- lockfile の test は `t.TempDir()` で完全に独立した baseDir を使う。本番 `./data/locks/` を汚さない
- `syscall.Flock` は Linux/macOS で動くが Windows には存在しない。build tag で `//go:build unix` を付与
- `clirun.Run` は内部で `os.Exit` を呼ぶため、テストでは exit code を返す内部関数（`runMain(...) int`）に分離し、`Run` はそのラッパとする
- マージコミット例：「Why: フェーズ 1 残課題の lock 排他制御と CI を投入し、フェーズ 2 へ進める土台を完成。What: lockfile（unix flock + windows スタブ）、clirun ヘルパー、8 バッチ骨格、server の多重起動 lock、GitHub Actions CI。Impact: `appmgr-import-skysea` 等の二重起動が exit 2 で防止される。Windows 本実装は本番投入前の別 PR」
