# app-manager

社内アプリケーション管理システム。ライセンス・承認状態・SKYSEA インストール実態・AD ユーザを集約管理する Web アプリケーション。

仕様の詳細は `docs/specs/01_背景と目的.md` および `docs/specs/02_要件定義.md` を参照。実装フェーズ計画は `docs/plans/` を参照。

## 開発環境

Nix flake で必要なツール一式が提供される。

```sh
nix develop
```

提供されるツール：`go`、`gopls`、`sqlc`、`go-migrate`（CLI 名 `migrate`）、`templ`、`golangci-lint`、`air`、`sqlite`。

## ビルド・実行

```sh
make build          # bin/ にバイナリを生成
make test           # 全テスト実行
make lint           # golangci-lint 実行

cp config.example.yml config.yml
make run            # appmgr-server を起動

curl http://localhost:8180/healthz   # "ok" が返れば起動成功
```

## ディレクトリ構造（フェーズ 1 時点）

```text
app_man/
├── cmd/
│   └── server/                 # appmgr-server: Web サーバ本体
├── internal/
│   ├── config/                 # YAML 設定ファイル読込（*_env 環境変数展開対応）
│   └── applog/                 # slog ロガー初期化
├── db/                         # PR2 以降：マイグレーション・sqlc クエリ
├── docs/
│   ├── specs/                  # 背景・要件定義
│   └── plans/                  # 実装フェーズ計画
├── config.example.yml          # 設定ファイル雛形
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

JSON 構造化ログを `logs/<binary-name>.log` に出力する。各エントリには `binary`（バイナリ名）と `pid`（プロセス ID）属性が常時付与される。

## 開発ルール

- main ブランチへの直接コミットは禁止。必ず feature ブランチを切って PR を出す
- TDD：RED テスト → GREEN 実装 → REFACTOR の各サイクルをコミット履歴に残す
- 1 コミット 1 論理変更
- 詳細は `~/.claude/CLAUDE.md` および `docs/specs/01_背景と目的.md` 参照
