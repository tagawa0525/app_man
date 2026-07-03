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

## 事前検証 (2026-07-03 実施)

Plan 初版で最大リスクとしていた「modernc.org/sqlite の `VACUUM INTO` 対応」を、
使い捨て検証プログラム (WAL 有効 + `_pragma=foreign_keys(1)`、本番と同 DSN 構成)
で実証済み。フォールバック検討は不要になった。

| 検証項目 | 結果 |
|---------|------|
| `db.Exec("VACUUM INTO ?", dest)` (パラメータバインド) | **成功**。出力ファイルは開いて SELECT 可能 |
| 既存の非空ファイルへの `VACUUM INTO` | **エラー** `SQL logic error: output file already exists (1)`。上書きはされない |
| 既存の空 (0 byte) ファイルへの `VACUUM INTO` | 成功 (SQLite 仕様どおり) |

この結果から 2 つの設計上の帰結：

1. 出力先ファイルが既に存在すると VACUUM INTO 自体が失敗する。中断された前回実行の
   残骸 (部分ファイル) が次回実行を恒久的にブロックしうる → **tmp 名に書いて rename**
   で解決する (下記「原子性」)
2. Plan 初版の「同名衝突時は上書きされるので実害なし」は誤りだったため削除

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| 起動経路 | 既存 `clirun.Run(binaryName, lockfile.ModeGlobal, handler)` を維持 | ModeGlobal は VACUUM INTO 中の他バッチ書込みを排他する既存設計 (スケルトンで設定済み) |
| DB 接続 | handler 内で `db.Open(deps.Cfg.Database)` | clirun は Cfg/Logger/DryRun のみ渡す。import-bootstrap と同流儀 |
| ソース DB の存在チェック | `db.Open` の前に `os.Stat(cfg.Database.Path)` で存在確認、無ければ error | SQLite は open 時に空 DB を勝手に作る。DB パス設定ミスのまま「空 DB のバックアップに成功」する事故を防ぐ |
| 本体ロジック分離 | `cmd/backup/runner.go` に `runBackup(ctx, deps, now) error` を切る | main は `clirun.Run` に薄く渡すだけ。runner_test.go で時刻注入してテスト |
| 時刻注入 | `runBackup` に `now time.Time` を引数で渡す | clirun は時刻を渡さない。テスト決定論のため main から `time.Now()` を渡す |
| 出力先 | `config.Backup.OutputDir` (新規) | 仕様書「別ファイルに書き出し」。`./data/backups` をデフォルト例に |
| 出力ファイル名 | `app-<YYYYMMDD-HHMMSS>.db`。タイムスタンプは **ローカル時刻 (JST)** | 運用者が直接目にするファイル名であり、meta.yml の日時も JST (仕様書 §8.6)。日本に DST は無いので辞書順 = 時刻順は崩れない |
| 原子性 | `VACUUM INTO` は `<dest>.tmp` に書き、`f.Sync()` (fsync) 後に `os.Rename(tmp, dest)` | (a) SQLite の VACUUM INTO は出力を sync しない (電源断でバックアップ破損のおそれ、SQLite 公式ドキュメント)。(b) SIGINT / エラー中断の部分ファイルが `dest` に残ると上記検証のとおり次回実行が恒久ブロックされる。tmp + rename で「`app-*.db` は常に完成品」を保証する。dir の fsync は Windows で不可のため行わない |
| 残骸 tmp の掃除 | 実行冒頭で `output_dir` 内の `app-*.db.tmp` (厳格パターン一致) を削除 | 前回中断の残骸。ModeGlobal lock 下なので並走プロセスの tmp を消す危険は無い |
| VACUUM INTO | `db.ExecContext(ctx, "VACUUM INTO ?", tmpPath)` | パラメータバインドは実証済み (上記)。パス文字列の SQL 連結を避ける |
| 世代管理 | `config.Backup.Generations` (新規, int)。`output_dir` 内の正規表現 `^app-\d{8}-\d{6}\.db$` に一致するファイルを名前昇順 (= 時刻順) にソートし、新しい方から Generations 個を残して古いものを削除 | 仕様書「世代管理は設定値」。glob `app-*.db` ではなく厳格一致にするのは、利用者が置いた無関係ファイル (例 `app-old.db`) を誤削除しないため |
| Generations の境界 | `0` は無制限保持 (削除しない)、正値は保持世代数、負値は validate エラー | 0 = 「世代管理しない」を素直に表現。config.example.yml では `14` を例示し無制限をデフォルト運用にしない |
| OutputDir 未設定 | `runBackup` 冒頭で error (exit 1) | config.validate で必須化すると backup 設定を持たない server 等が起動不能になるため、バイナリ固有の前提条件として handler 内で検査。「config 不正なのに exit 3 でなく 1」の非対称は clirun の handler が exit code を選べない制約による割り切り (clirun 改修はこの PR ではしない) |
| dry-run | `deps.DryRun` が true なら tmp 掃除・VACUUM INTO・削除を一切せず「出力予定パス + 削除予定ファイル (新ファイルが出来たと仮定して算出)」をログに出すのみ | clirun 共通フラグ。破壊的操作の事前確認。削除予定は実行後の姿を予告する方が有用 |
| 添付スナップショット | コードでは行わない。`README.md` に手順を記載 | 仕様書「手順を README に明記」。添付ファイルのコピーはバイナリの責務外 |
| ディレクトリ作成 | `output_dir` が無ければ `os.MkdirAll(0o755)` で作成 | 初回実行で出力先が無いのは正常系 |
| ログ | 成功: `info "backup completed" dest size_bytes pruned_count`、dry-run: `info "backup dry-run" dest would_prune`。失敗は error | 監査・運用可視性。DB の中身 (レコード値) は出さない |
| exit code | clirun 既定 (0 OK / 1 handler error / 2 lock 競合 / 3 config 不正) | 既存規約 |

## 対象スコープ

### 範囲内

- `internal/config/config.go`: `BackupConfig{ OutputDir string; Generations int }` を追加 + validate (Generations >= 0 のみ。OutputDir 必須チェックは runBackup 側)
- `internal/config/config_test.go`: backup 設定の読込 / 未指定時ゼロ値 / 負値拒否テスト
- `cmd/backup/main.go`: スケルトンを `runBackup` 配線に置換 (`clirun.Run` に `runBackup(ctx, deps, time.Now())` を渡す)
- `cmd/backup/runner.go`: `runBackup(ctx, deps, now) error` 本体 (前提チェック → tmp 掃除 → VACUUM INTO tmp → fsync → rename → 世代管理)
- `cmd/backup/runner_test.go`: 後述の受け入れ基準を網羅
- `config.example.yml`: `backup:` セクション追記 (`output_dir: ./data/backups` / `generations: 14`)
- `README.md`: 添付ファイルスナップショット手順 + appmgr-backup の運用方法 (タスクスケジューラ登録・リストア手順の概要) を追記

### 範囲外 (別 PR)

- `appmgr-prune-logs` (運用基盤 第 2 PR)
- `appmgr-generate-meta` / `appmgr-check-integrity` (ライセンス = フェーズ 6 後)
- `/admin/export` 画面
- 添付ファイルの自動コピー (仕様書は手順記載のみ要求)
- バックアップの暗号化 / リモート転送 (要件外)
- リストア機能・バックアップ後の `PRAGMA integrity_check` (要件外。VACUUM INTO 成功 + fsync + rename で完成品保証は足りる。復元検証は README の手動手順)
- clirun への「handler が exit 3 を返せる」拡張 (必要になったら別 PR)

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
    OutputDir   string `yaml:"output_dir"`   // VACUUM INTO の出力先。appmgr-backup 実行時に必須
    Generations int    `yaml:"generations"`  // 保持世代数。0 = 無制限、負値はエラー
}
```

validate は `Generations < 0` のみ弾く。OutputDir の必須チェックを config.validate に
置くと、backup 設定を持たない server 等の全バイナリが起動不能になるため、
`runBackup` の冒頭でバイナリ固有の前提条件として検査する。

### runBackup フロー

```text
1. cfg.Backup.OutputDir == "" なら error ("backup.output_dir is required")
   (Generations < 0 は config.validate で既に弾かれている)
2. os.Stat(cfg.Database.Path) → 無ければ error (空 DB の自動生成を防ぐ)
3. os.MkdirAll(OutputDir, 0o755)
4. dest := filepath.Join(OutputDir, "app-" + now.Format("20060102-150405") + ".db")
   tmp  := dest + ".tmp"
5. dry-run なら:
     - 削除予定 (dest が出来たと仮定した pruneTargets) を算出してログ → return
6. 残骸掃除: OutputDir 内の `^app-\d{8}-\d{6}\.db\.tmp$` 一致ファイルを削除
7. db.Open(cfg.Database) → defer close
8. db.ExecContext(ctx, "VACUUM INTO ?", tmp)
   失敗時は tmp を削除して error return
9. tmp を open → f.Sync() → close (VACUUM INTO は出力を sync しないため)
10. os.Rename(tmp, dest)  // Windows でも既存 dest を置換できる (os.Rename の保証)
11. 世代管理: pruneOldBackups(OutputDir, Generations)
12. ログ: dest / size_bytes / pruned_count
```

### 世代管理ヘルパ

```go
// pruneOldBackups は dir 内の ^app-\d{8}-\d{6}\.db$ に一致するファイルを
// 名前昇順 (= 時刻昇順) に並べ、新しい方から generations 個を残して
// 古いものを削除し、削除数を返す。generations == 0 は no-op。
func pruneOldBackups(dir string, generations int) (int, error)
```

ファイル名 `app-YYYYMMDD-HHMMSS.db` は辞書順 = 時刻順なので `slices.Sort` で十分。
正規表現一致により `.tmp` や利用者配置の無関係ファイルは削除対象にならない。

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
3. `test(backup): VACUUM INTO で有効な SQLite が出力される (RED)`
4. `feat(backup): runBackup で VACUUM INTO + tmp/rename/fsync 実装 (GREEN)`
5. `test(backup): 世代管理で古い世代のみ削除・無関係ファイル温存 (RED)`
6. `feat(backup): pruneOldBackups 実装 (GREEN)`
7. `test(backup): dry-run / OutputDir 未指定 / ソース DB 不在 / 残骸 tmp 掃除 (RED)`
8. `feat(backup): 前提チェックと dry-run・tmp 掃除 (GREEN)`
9. `feat(cmd/backup): main をスケルトンから runBackup 配線に置換`
10. `docs: README に backup 運用 + 添付スナップショット手順を追記`
11. `chore(config): config.example.yml に backup セクション追記`

各コミットで `make test` / `make lint` 緑を確認する。

## 受け入れ基準

- `make build` / `make test` / `go test -race ./...` / `make lint` 全緑
- `runBackup`:
  - 出力先に `app-<timestamp>.db` が作られ、それが有効な SQLite (開いて `SELECT` できる)
  - 出力先に `.tmp` ファイルが残らない (成功時・VACUUM 失敗時とも)
  - 実行冒頭で前回残骸の `app-*.db.tmp` が削除される
  - Generations=2 で 3 回バックアップ → 古い 1 個が削除され 2 個残る
  - Generations=0 → 削除されない
  - パターン不一致ファイル (例 `app-old.db`, `notes.txt`) は世代管理で削除されない
  - dry-run → ファイル作成・削除が一切起きず、ログのみ
  - OutputDir 未指定 → エラー (handler error, exit 1)
  - ソース DB (database.path) が存在しない → エラー。空 DB が作られない
  - OutputDir のディレクトリが無い → MkdirAll で作られる
- config: `backup.generations: -1` で Load がエラー
- 多重起動 (ModeGlobal lock 競合) → exit 2 (clirun 既存挙動、本 PR では追加テスト不要)
- README に「DB バックアップ後に `<base>/licenses/` 等の添付を同タイミングでスナップショットする手順」が記載されている
- 構造化ログに DB の中身 (レコード値) が出ない (出力は dest パス / サイズ / 件数のみ)

## 想定リスク

- **VACUUM INTO の所要時間**: 大規模 DB (10000 インストール想定) で日次実行が長引く可能性。ModeGlobal lock 保持時間が延びるが、日次の計画停止許容 (§8.2) なので MVP では問題視しない
- **タイムスタンプ衝突**: 同一秒内の 2 回実行は ModeGlobal lock で exit 2 になるため事実上発生しない。lock 解放後の手動連続実行のレアケースでは `os.Rename` が既存 dest を置換する (直前の完成バックアップが同内容で置き換わるだけで実害なし)
- **ディスク逼迫**: 世代管理前に新バックアップを書くため、瞬間的に Generations+1 世代分の容量が要る。DB サイズが小さい (SQLite 単一ファイル) 想定なので許容。config.example.yml の generations 例示値 (14) で無制限膨張をデフォルトにしない

## 解消済みリスク (記録)

- ~~modernc.org/sqlite の VACUUM INTO 対応~~ → 2026-07-03 の事前検証で解消 (冒頭の表)。パラメータバインド `VACUUM INTO ?` も動作確認済み
- ~~既存ファイルへの VACUUM INTO は上書きされる~~ → 誤り。実際はエラーになる。tmp + rename 方式でこの制約自体を回避
