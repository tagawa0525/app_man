# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## プロジェクトの全体像

社内ソフトウェアライセンスと承認状態を集約管理する Web アプリケーション。SKYSEA（端末インベントリ）と AD（人事マスタ）の事実情報と、本システム上の権利情報を突合し、ライセンス過不足・未承認 / 禁止ソフトの利用・退職者の未解除アカウントを可視化する。

仕様は 2 文書構成：

- `docs/specs/01_背景と目的.md`：なぜそう作るか（設計思想・採用しなかった選択肢の根拠）
- `docs/specs/02_要件定義.md`：何を作るか（技術スタック・データモデル・機能要件・受け入れ基準）
- **2 文書が矛盾する場合は 01 を優先**（設計思想が実装指示に勝つ）

実装フェーズごとの計画は `docs/plans/<slug>.md` に置く。

## 開発環境とコマンド

```sh
nix develop                                # go, sqlc, go-migrate, templ, golangci-lint, air, sqlite
make build                                 # bin/appmgr-server を生成（CGO_ENABLED=0 固定。バッチ系バイナリは後続フェーズで追加予定）
make test                                  # go test ./...
make lint                                  # golangci-lint run（v2 設定）
make run                                   # appmgr-server を config.yml で起動
go test -run TestLoad ./internal/config/   # 単一テスト実行
go test -race ./...                        # レースコンディション検出
```

**起動時の自動マイグレーションはしない**設計（誤デプロイで DB が壊れる事故防止）。マイグレーションはデプロイ手順で明示的に実行する。`appmgr-server` は起動時にスキーマ版数チェックのみ行い、不一致なら起動失敗。

## アーキテクチャの大枠

### CLI バイナリの独立性

`cmd/<name>/` で 10 種のバイナリを独立してビルドする構成（`appmgr-server` ＋ 9 個のバッチ系）。各バイナリは `internal/` を共有しつつエントリポイントだけ分離。Windows タスクスケジューラから機能ごとに個別呼び出しできるよう、機能別バイナリで設計してある。サーバが落ちててもバッチが動く。

### 排他制御（lock ファイル基盤）

複数の CLI バイナリが SQLite を奪い合うのを防ぐため、`<base>/locks/<binary-name>.lock` を排他オープンする。`appmgr-backup` のみ他全バッチと相互排他（`ModeGlobal`）— `VACUUM INTO` が他の書込みと衝突するため。多重起動時は **exit code 2** で即終了。`appmgr-server` の lock は別管理（バッチ系のグローバルロック対象外）。

### データソースの役割分担

- **SKYSEA = 事実**（端末にインストールされているもの）。SKYSEA DB を直接参照することは**サポート外操作のため避ける**。エクスポートされた CSV を `imports/skysea/inbox/` 経由で取り込む
- **AD = 人事マスタ**（社員と部署の正本）。本番は LDAP バインド、テスト環境は CSV 代替
- **本システム = 権利と承認**（保有ライセンスと部署別承認情報）

### FS が正本、DB は検索インデックス（ライセンス証書類）

ライセンス証書ファイルは `<base>/licenses/<vendor_slug>/<product_slug>/<license_slug>/` 配下に配置し、各契約フォルダの `meta.yml` を自動生成する。システム廃止時にファイルサーバを覗くだけで全情報が読める状態を維持するため。FS と DB がズレても整合性チェックは警告のみで**ブロックしない**。

### 論理削除の規約

論理削除は boolean フラグではなく **日時カラム**で表現する。「NULL ならアクティブ、値があれば無効」で統一：

- `users.deactivated_at`（退職）
- `departments.valid_to`（部署廃止）
- `devices.retired_at`（端末退役）
- `app_users.disabled_at`（アプリユーザ無効化）
- `*_assignments.revoked_at`（割当解除）
- `licenses.expires_at`（契約満了）

「いつ無効化されたか」が監査情報として必要なため。新規テーブル設計時もこの規約に従う。

### 設定ファイルの `_env` サフィックス

YAML キーが `_env` で終わる場合、値は環境変数名として解決される：

```yaml
server:
  session_secret_env: SESSION_SECRET   # → SESSION_SECRET 環境変数の値が session_secret になる
```

`internal/config` の Load 時に `yaml.Node` を走査して展開。環境変数が未設定なら起動失敗（事故防止）。新しい機微情報フィールドを追加する際もこの規約に従う。

## 禁止事項（要件書 § 2）

- **SPA**（React / Vue 等）。`templ` ＋ HTMX のサーバサイドレンダリングのみ
- **ORM**（GORM 等）。SQL は `sqlc` で直書き
- **外部 CDN 依存**。CSS / JS（Tailwind、HTMX 等）は `web/static/` に同梱・自己ホスト
- **CGO 必須ライブラリ**。`CGO_ENABLED=0` で単一バイナリビルド可能であること（SQLite ドライバは `modernc.org/sqlite` を使用）
- **早すぎる抽象化**。3 回重複してから抽象化を検討。`internal/clock`、`internal/idgen` 等の予防抽象は入れない

## ブランチ運用の特記事項

### Plan ファイルを最初のコミットに含める（ブランチ種別を問わない）

新しいブランチを切るときは、**最初のコミットを `docs/plans/<slug>.md` の追加にする**。`feat/` での実装はもちろん、`docs/`・`fix/`・`refactor/`・`chore/` でも、非自明な意思決定（何を / なぜ / どう作るか）を含むなら Plan ファイルを先に書いて、その後のコミットで本体を積む。

例外は「設計判断を含まない最小変更」のみ：1 行 typo 修正、リンク切れ修正、自明な lint 警告解消等。それ以外、たとえドキュメント追加のみの PR でも Plan を先に置く（CLAUDE.md 追加 PR 自体もこのルールに従う）。

これにより `git log` 上で「設計意図 → 本体変更」が時系列で読める履歴になる。実装中に方針が変わった場合は Plan ファイルを編集するコミットを追加する（揺れも履歴に残す）。

### ブランチを切る順序

1. `git switch -c <type>/<topic>`（`type` は `feat` / `fix` / `docs` / `refactor` / `chore` 等）
2. `docs/plans/<slug>.md` を書く → **最初のコミットで含める**（`docs(plans): ...`）
3. 本体のコミットを積む（実装系なら TDD サイクル：RED テスト → GREEN 実装 → 必要なら REFACTOR）
4. PR 作成

### PR マージ

`gh pr merge --merge`（`--squash` / `--rebase` 禁止 — 個別コミット履歴と TDD サイクルの可視性を保つ）。マージコミットは **Why / What / Impact** 形式で記述する。

## ロギング

各 CLI バイナリは `applog.New(cfg.Logging, binaryName)` で `*slog.Logger` を初期化する。全ログに `binary` 属性（バイナリ名）と `pid` 属性（プロセス ID）が常時付与され、JSON 形式で `logs/<binary-name>.log` に出力される。

## sqlc 生成物の扱い

`internal/repository/*.sql.go` の sqlc 生成コードは **コミットする**（`.gitignore` でも除外していない）。レビューで生成コードの差分が見えること、`go generate` を CI 必須にしない運用の両立のため。スキーマ・クエリ変更時は `make generate` を実行してから commit する。
