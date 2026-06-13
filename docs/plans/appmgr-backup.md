# appmgr-backup 本実装 (フェーズ 12 — 運用基盤 第 1 PR)

## Context

フェーズ 1〜3 (基盤 / マスタ系 CRUD / 認証・認可) が完了。ユーザー指示により
認証系 (AD 連携) と SKYSEA 取込みは後回しにし、外部依存ゼロで本番運用の信頼性を
固められる **フェーズ 12 (運用基盤)** を先行する。その第 1 PR として
`appmgr-backup` を placeholder スケルトンから本実装に置き換える。

現状 `cmd/backup/main.go` は 23 行のスケルトンで、`clirun.Run` に
「not implemented」をログ出力する handler を渡すだけ。仕様書 §8.4 の要求：

- `appmgr-backup` バイナリで日次実行 (タスクスケジューラ)
- SQLite を `VACUUM INTO` で別ファイルに書き出し
- 添付ファイル群の同時スナップショット手順を `README.md` に明記
- 世代管理は設定値

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| 起動経路 | 既存 `clirun.Run(binaryName, lockfile.ModeGlobal, handler)` を維持 | ModeGlobal は VACUUM INTO 中の他バッチ書込みを排他する既存設計 (スケルトンで設定済み) |
| DB 接続 | handler 内で `db.Open(deps.Cfg.Database)` | clirun は Cfg/Logger/DryRun のみ渡す。import-bootstrap と同流儀 |
| 本体ロジック分離 | `cmd/backup/runner.go` に `runBackup(ctx, deps, now) error` を切る | main は `clirun.Run` に薄く渡すだけ。runner_test.go で時刻注入してテスト |
| 時刻注入 | `runBackup` に `now time.Time` を引数で渡す | clirun は時刻を渡さない。テスト決定論のため main から `time.Now()` を渡す |
| 出力先 | `config.Backup.OutputDir` (新規) | 仕様書「別ファイルに書き出し」。`./data/backups` をデフォルト例に |
| 出力ファイル名 | `app-<YYYYMMDD-HHMMSS>.db` | タイムスタンプで世代を識別。多重起動は ModeGlobal lock が防ぐ (exit 2) ので同名衝突しない |
| VACUUM INTO | `db.ExecContext(ctx, "VACUUM INTO ?", destPath)` | modernc.org/sqlite が対応するか RED テストで実証。未対応ならフォールバック検討 (リスク項参照) |
| 世代管理 | `config.Backup.Generations` (新規, int)。`output_dir` 内の `app-*.db` を名前順 (= 時刻順) にソートし、Generations を超える古いものを削除 | 仕様書「世代管理は設定値」 |
| Generations の境界 | `0` は無制限保持 (削除しない)、正値は保持世代数、負値は validate エラー | 0 = 「世代管理しない」を素直に表現 |
| OutputDir 未設定 | validate エラー (起動失敗) | バックアップ先不明での実行は事故。事故防止優先 (config の他必須項目と同方針) |
| dry-run | `deps.DryRun` が true なら VACUUM INTO せず「出力予定パス + 削除予定ファイル」をログに出すのみ | clirun 共通フラグ。破壊的操作の事前確認 |
| 添付スナップショット | コードでは行わない。`README.md` に手順を記載 | 仕様書「手順を README に明記」。添付ファイルのコピーはバイナリの責務外 |
| ディレクトリ作成 | `output_dir` が無ければ `os.MkdirAll(0o755)` で作成 | 初回実行で出力先が無いのは正常系 |
| ログ | 成功: `info "backup completed" dest size_bytes pruned_count`、dry-run: `info "backup dry-run" dest would_prune`。失敗は error | 監査・運用可視性 |
| exit code | clirun 既定 (0 OK / 1 handler error / 2 lock 競合 / 3 config 不正) | 既存規約 |

## 対象スコープ

### 範囲内

- `internal/config/config.go`: `BackupConfig{ OutputDir string; Generations int }` を追加 + validate (OutputDir 必須、Generations >= 0)
- `internal/config/config_test.go`: backup 設定の読込 / デフォルト / 負値拒否テスト
- `cmd/backup/main.go`: スケルトンを `runBackup` 配線に置換 (`clirun.Run` に `runBackup(ctx, deps, time.Now())` を渡す)
- `cmd/backup/runner.go`: `runBackup(ctx, deps, now) error` 本体 (DB open → VACUUM INTO → 世代管理)
- `cmd/backup/runner_test.go`: VACUUM INTO で .db 出力 / 世代管理で古い世代削除 / dry-run は出力しない / OutputDir 未作成時の MkdirAll
- `config.example.yml`: `backup:` セクション追記
- `README.md`: 添付ファイルスナップショット手順 + appmgr-backup の運用方法を追記

### 範囲外 (別 PR)

- `appmgr-prune-logs` (運用基盤 第 2 PR)
- `appmgr-generate-meta` / `appmgr-check-integrity` (ライセンス = フェーズ 6 後)
- `/admin/export` 画面
- 添付ファイルの自動コピー (仕様書は手順記載のみ要求)
- バックアップの暗号化 / リモート転送 (要件外)
- リストア機能 (要件外、手順は README で十分)

## 内部設計

### config 拡張

```go
type Config struct {
    Server   ServerConfig
    Database DatabaseConfig
    Locks    LocksConfig
    Logging  LoggingConfig
    Auth     AuthConfig
    Backup   BackupConfig   `yaml:"backup"`
}

// BackupConfig は appmgr-backup の設定。
type BackupConfig struct {
    OutputDir   string `yaml:"output_dir"`   // VACUUM INTO の出力先。必須
    Generations int    `yaml:"generations"`  // 保持世代数。0 = 無制限、負値はエラー
}
```

validate:

- `Backup.OutputDir == ""` かつ `appmgr-backup` 起動時 → エラー。ただし config は全バイナリ共有なので、validate を「常に必須」にすると他バイナリ (server 等) が backup 設定なしで落ちる。**回避策**: config.validate では Generations の負値のみ弾き、OutputDir の必須チェックは `runBackup` の冒頭で行う (backup バイナリ固有の前提条件として扱う)

### runBackup フロー

```text
1. cfg.Backup.OutputDir == "" なら error ("backup.output_dir is required")
2. cfg.Backup.Generations < 0 は config.validate で既に弾かれている
3. db.Open(cfg.Database) → defer close
4. os.MkdirAll(OutputDir, 0o755)
5. dest := filepath.Join(OutputDir, "app-" + now.Format("20060102-150405") + ".db")
6. dry-run なら:
     - 削除予定 (pruneTargets) を算出してログ → return
7. db.ExecContext(ctx, "VACUUM INTO ?", dest)
8. 世代管理: OutputDir 内 app-*.db を昇順ソート、len - Generations 個の古いものを os.Remove
   (Generations == 0 ならスキップ)
9. ログ: dest / size / pruned_count
```

### 世代管理ヘルパ

```go
// pruneOldBackups は dir 内の app-*.db を時刻昇順に並べ、generations を
// 超える古いファイルを削除して削除数を返す。generations == 0 は no-op。
func pruneOldBackups(dir string, generations int) (int, error)
```

ファイル名 `app-YYYYMMDD-HHMMSS.db` は辞書順 = 時刻順なので `sort.Strings` で十分。

### main.go

```go
func main() {
    clirun.Run(binaryName, lockfile.ModeGlobal, func(ctx context.Context, deps clirun.Deps) error {
        return runBackup(ctx, deps, time.Now())
    })
}
```

## TDD コミット順序

1. `docs(plans): appmgr-backup 本実装の Plan ファイル`
2. `feat(config): BackupConfig を追加 (OutputDir / Generations + 負値 validate)`
3. `test(backup): VACUUM INTO で .db が出力される (RED)`
4. `feat(backup): runBackup で VACUUM INTO 実装 (GREEN)`
5. `test(backup): 世代管理で古い世代を削除 (RED)`
6. `feat(backup): pruneOldBackups 実装 (GREEN)`
7. `test(backup): dry-run は出力しない / OutputDir 未指定はエラー (RED)`
8. `feat(backup): dry-run と OutputDir 前提チェック (GREEN)`
9. `feat(cmd/backup): main をスケルトンから runBackup 配線に置換`
10. `docs: README に backup 運用 + 添付スナップショット手順を追記`
11. `chore(config): config.example.yml に backup セクション追記`

各コミットで `make test` / `make lint` 緑を確認する。

## 受け入れ基準

- `make build` / `make test` / `go test -race ./...` / `make lint` 全緑
- `runBackup`:
  - 出力先に `app-<timestamp>.db` が作られ、それが有効な SQLite (開いて `SELECT` できる)
  - Generations=2 で 3 回バックアップ → 古い 1 個が削除され 2 個残る
  - Generations=0 → 削除されない
  - dry-run → ファイルが作られず、削除も起きず、ログのみ
  - OutputDir 未指定 → エラー (handler error, exit 1)
  - OutputDir のディレクトリが無い → MkdirAll で作られる
- config: `backup.generations: -1` で Load がエラー
- 多重起動 (ModeGlobal lock 競合) → exit 2 (clirun 既存挙動、本 PR では追加テスト不要)
- README に「DB バックアップ後に `<base>/licenses/` 等の添付を同タイミングでスナップショットする手順」が記載されている
- 構造化ログに DB の中身 (レコード値) が出ない (出力は dest パス / サイズ / 件数のみ)

## 想定リスク

- **modernc.org/sqlite の VACUUM INTO 対応**: pure-Go ドライバが `VACUUM INTO` をサポートしない可能性。RED テスト (手順 3) で最初に実証する。未対応の場合は (a) `sqlite3` の online backup API 相当、(b) WAL チェックポイント + ファイルコピー、のフォールバックを検討。Plan の前提が崩れたら別案で再設計
- **config 必須チェックの配置**: OutputDir を config.validate で必須にすると backup 設定を持たない server 等が起動不能になる。→ runBackup 冒頭でのバイナリ固有チェックに留める (上記設計通り)
- **VACUUM INTO の所要時間**: 大規模 DB (10000 インストール想定) で日次実行が長引く可能性。ModeGlobal lock 保持時間が延びるが、日次の計画停止許容 (§8.2) なので MVP では問題視しない
- **タイムスタンプ衝突**: 同一秒内の 2 回実行で同名ファイル。ModeGlobal lock で多重起動が exit 2 になるため事実上発生しない。手動連続実行のレアケースは VACUUM INTO が既存ファイルを上書きする (実害なし)
