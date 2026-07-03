# app-manager

社内アプリケーション管理システム。ライセンス・承認状態・SKYSEA インストール実態・AD ユーザを集約管理する Web アプリケーション。

仕様の詳細は `docs/specs/01_背景と目的.md` および `docs/specs/02_要件定義.md` を参照。実装フェーズ計画は `docs/plans/` を参照。

## 開発環境

Nix flake で必要なツール一式が提供される。

```sh
nix develop
```

提供されるツール：`go`、`gopls`、`sqlc`、`templ`、`golangci-lint`、`air`、`sqlite`。

## ビルド・実行

```sh
make build          # bin/ に appmgr-server / appmgr-create-app-user / appmgr-migrate を生成
make test           # 全テスト実行
make lint           # golangci-lint 実行

cp config.example.yml config.yml
mkdir -p ./data     # DB_PATH 既定 (./data/app.db) の親ディレクトリ
make migrate-up     # マイグレーションを適用 (必須スキーマ版 = 6)
make run            # appmgr-server を起動

curl http://localhost:8180/healthz   # "ok" が返れば起動成功
```

**起動時の自動マイグレーションはしない**。`make migrate-up` を実行せずに `appmgr-server` を起動するとスキーマ版数不一致で exit 1 する。誤デプロイ防止のため、マイグレーションは明示的に行う運用とする。

スキーマ・クエリを変更したときは `make generate` で sqlc 生成物 (`internal/repository/*.sql.go`) を再生成してから commit する。

## ディレクトリ構造（フェーズ 1 時点）

```text
app_man/
├── cmd/
│   ├── server/                 # appmgr-server: Web サーバ本体
│   ├── create-app-user/        # appmgr-create-app-user: ローカル admin 作成 CLI (骨格)
│   └── migrate/                # appmgr-migrate: マイグレーション実行 CLI
├── internal/
│   ├── config/                 # YAML 設定ファイル読込（*_env 環境変数展開対応）
│   ├── applog/                 # slog ロガー初期化
│   ├── db/                     # modernc/sqlite 接続、go-migrate ランナ、版数チェック
│   └── repository/             # sqlc 生成物（コミット対象、手書きしない）
├── db/
│   ├── migrations/             # up/down SQL + embed.FS (現在 6 マイグレーション)
│   └── queries/                # sqlc 入力クエリ
├── docs/
│   ├── specs/                  # 背景・要件定義
│   └── plans/                  # 実装フェーズ計画
├── config.example.yml          # 設定ファイル雛形
├── sqlc.yaml                   # sqlc 設定
├── flake.nix                   # 開発環境定義
└── Makefile
```

## 設定ファイル

`config.example.yml` を `config.yml` にコピーして編集する。キーが `_env` で終わる場合、値は環境変数名として解決される。

```yaml
server:
  session_secret_env: SESSION_SECRET   # 環境変数 SESSION_SECRET から値を取得
```

## ログ

JSON 構造化ログを `<logging.base_dir>/<binary-name>.log` に出力する（`base_dir` は `config.yml` の `logging.base_dir` で指定。デフォルト `./logs`）。各エントリには `binary`（バイナリ名）と `pid`（プロセス ID）属性が常時付与される。

## バックアップ

`appmgr-backup` が SQLite DB を `VACUUM INTO` で別ファイルに書き出す。日次でタスクスケジューラから実行する想定。

```sh
appmgr-backup -config config.yml            # バックアップ実行
appmgr-backup -config config.yml -dry-run   # 出力予定パスと削除予定ファイルをログに出すのみ（バックアップ作成・削除はしない。出力ディレクトリだけは作成される）
```

出力先には `app-<YYYYMMDD-HHMMSS>.db`（タイムスタンプはローカル時刻 = JST）が作られる。書出しは一時ファイル + fsync + rename で行うため、`app-*.db` は常に完成品である（部分ファイルが残らない）。

exit code：

| code | 意味 |
| ---- | ---- |
| 0 | 成功 |
| 1 | バックアップ処理の失敗（DB 不在、出力先未設定、VACUUM 失敗等） |
| 2 | lock 競合（他の `appmgr-*` バッチが実行中）。多重起動防止のため即終了 |
| 3 | config 不正（読込失敗、`backup.generations` 負値等） |

`appmgr-backup` は他の全バッチと相互排他（グローバルロック）で動く。他バッチと重なった場合は exit 2 になるので、タスクスケジューラでは 0 以外をエラー扱いにし、時刻をずらして再実行する。

### 設定

`config.yml` の `backup` セクションで出力先と保持世代数を指定する。

```yaml
backup:
  output_dir: ./data/backups   # VACUUM INTO の出力先（appmgr-backup 実行時に必須）
  generations: 14              # 保持世代数。0 = 無制限保持、負値はエラー
```

世代管理は `output_dir` 内の `app-<YYYYMMDD-HHMMSS>.db` パターンに厳密一致するファイルのみが対象で、新しい方から `generations` 個を残して古いものを削除する。パターン不一致のファイル（利用者が手動で置いたもの等）は削除されない。

### Windows タスクスケジューラでの日次実行

「タスクの作成」で以下を登録する：

- 操作：`appmgr-backup.exe` を引数 `-config C:\appmgr\config.yml` で実行（開始フォルダはバイナリ設置先）
- トリガー：日次、業務時間外（例：深夜 2:00）
- 「タスクを停止するまでの時間」等の失敗検知を有効にし、exit code 0 以外を失敗として扱う

### 添付ファイル群の同時スナップショット

ライセンス証書ファイルと `meta.yml` は DB ではなくファイルシステム（`<base>/licenses/` 配下。`<base>` はライセンス証書の保管ベースディレクトリ、要件書 §3.2）が正本である。`appmgr-backup` は DB のみを対象とするため、**DB バックアップの直後に `licenses/` 配下を同タイミングでコピーする**。DB と添付のスナップショット時刻を揃えることで、リストア時に「DB は参照しているが実体がない証書」の発生を最小化する。

Windows（本番）では、タスクスケジューラに登録するバッチファイルで DB バックアップに続けて robocopy を実行する：

```bat
@echo off
for /f %%T in ('powershell -NoProfile -Command "Get-Date -Format yyyyMMdd-HHmmss"') do set TS=%%T

C:\appmgr\appmgr-backup.exe -config C:\appmgr\config.yml
if errorlevel 1 exit /b %errorlevel%

robocopy "D:\appmgr\files\licenses" "D:\appmgr\backups\licenses-%TS%" /E /R:1 /W:1
if %errorlevel% geq 8 exit /b %errorlevel%
exit /b 0
```

robocopy の exit code は 0〜7 が成功（コピー実施・差分なし等）、8 以上が失敗なので、`geq 8` で判定する。

開発環境（Linux/macOS）では `cp -r` で同等の手順になる：

```sh
./bin/appmgr-backup -config config.yml
cp -r ./data/files/licenses "./data/backups/licenses-$(date +%Y%m%d-%H%M%S)"
```

`licenses-<timestamp>/` の世代管理は `appmgr-backup` の対象外（削除対象は `app-*.db` のみ）なので、古いスナップショットの削除は運用で行う。

### リストア手順の概要

1. `appmgr-server` を停止し、タスクスケジューラのバッチを一時無効化する
2. 戻したい世代の `app-<timestamp>.db` を `database.path`（例：`./data/app.db`）にコピーする。コピー先に残っている `-wal` / `-shm` ファイル（例：`app.db-wal`）は古い DB のものなので削除する
3. 同タイムスタンプの `licenses-<timestamp>/` を `<base>/licenses/` に戻す
4. `appmgr-server` を起動し、ログイン・ライセンス一覧表示・証書ダウンロードを確認する

FS が正本・DB は検索インデックスという設計のため、DB と `licenses/` のスナップショット時刻が完全に一致していなくてもデータは失われない。ズレがある場合は FS 側の実体を優先し、整合性チェックの警告（ブロックはしない）に従って DB 側を修正する。

## 開発ルール

- main ブランチへの直接コミットは禁止。必ず feature ブランチを切って PR を出す
- TDD：RED テスト → GREEN 実装 → REFACTOR の各サイクルをコミット履歴に残す
- 1 コミット 1 論理変更
- 詳細は `docs/specs/01_背景と目的.md` 参照
