# CLAUDE.md 追加プラン

## Context

将来の Claude Code セッションが本リポジトリで効率的に作業できるよう、リポジトリ直下に `CLAUDE.md` を追加する。`/init` 起点だが、機械的に生成するのではなく、フェーズ 1 PR1 実装中に得た知見（Chi 採用、CGO=0、`_env` サフィックス展開、論理削除日時カラム規約等）も反映した「実際の運用と整合した」内容にする。

グローバル CLAUDE.md（`~/.claude/CLAUDE.md`、main 直接コミット禁止／TDD 義務／`--merge` 厳守等を規定）と重複させない。プロジェクト固有事項に絞る。

## Approach

参照する既存資産：

- `docs/specs/01_背景と目的.md`：設計思想・採用しなかった案の根拠
- `docs/specs/02_要件定義.md`：技術スタック・データモデル・機能要件
- `docs/plans/rustling-discovering-beaver.md`：フェーズ 1 の判断（Web FW 選定・マイグレ運用・CGO 固定 等）
- フェーズ 1 PR1 のレビュー指摘から反映済みの実装上の知見（`_env` 展開、`applog.New` cleanup 等）

CLAUDE.md に**含める節**：

- 2 文書スペック（01 / 02）の優先順位
- 開発環境（`nix develop`）と主要コマンド（make build / test / lint / 単一テスト / migrate）
- アーキテクチャ大枠（CLI バイナリ独立性、lock ファイル基盤、データソース役割分担、FS 正本、論理削除日時カラム規約）
- 設定ファイルの `_env` サフィックス展開規約
- 禁止事項（SPA / ORM / 外部 CDN / CGO 必須 / 早すぎる抽象化）
- ブランチ運用の特記事項（Plan ファイル最初コミット、`gh pr merge --merge` 厳守）
- ロギング規約、sqlc 生成物の扱い

CLAUDE.md に**含めない節**：

- ファイル構造の網羅列挙（discover 容易）
- グローバル CLAUDE.md と重複するルール
- `/init` テンプレが警告した「Common Development Tasks」「Tips for Development」「Support and Documentation」等の創作セクション

## Critical Files

- `/home/tagawa/github/app_man/CLAUDE.md`（新規）

## Branch / PR 運用

- ブランチ：`docs/claude-md`
- 最初のコミット：本プランファイル（`docs(plans): CLAUDE.md 追加プラン`）
- 2 番目のコミット：CLAUDE.md 本体（`docs: CLAUDE.md を追加`）
- main にマージ前に rebase で線形化（PR2 が前進したため）

## Verification

- README が参照している `docs/specs/01_背景と目的.md` と内容矛盾がないこと
- フェーズ 1 PR1 実装プラン（`rustling-discovering-beaver.md`）で確定した事項（Web FW=Chi、CGO=0、論理削除 `*_at` カラム、`_env` サフィックス）が CLAUDE.md にも反映されていること
- Markdown lint（リポジトリ pre-commit hook の `fix-markdown-lint.py`）通過

## 初稿レビュー反映：roadmap 言及の削除

初稿には「PR2 以降で追加される DB 関連：」「リクエスト ID 等のスコープ属性は PR3 以降で Chi ミドルウェアと一緒に追加予定。」といった **PR 番号付きの時系列ロードマップ** と、現状 Makefile に存在しない具体コマンド（`make migrate-up` / `make migrate-down`）への参照が紛れ込んでいた。

CLAUDE.md は「現状コードベースとの付き合い方」を伝える文書であり、roadmap は性質が異なる（マージ済になると即陳腐化し、未実装機能を「ある」かのように示唆して誤誘導する）。次の方針で整理する：

- **削除**：PR 番号への言及、Makefile に存在しない具体コマンド（`make migrate-up` / `make migrate-down`）への参照
- **残す**：「自動マイグレーションしない」「lock ファイル基盤」「論理削除日時カラム」「FS 正本」等は実装前から有効な*規約・設計指針*として記述
- **残す**：`make generate`（sqlc 導入と同時に Makefile に追加される想定の規約）、「10 種のバイナリ」（要件書で確定済みの設計上の数）

判断軸：「現状コードベースの状態を述べる文」か「規約・設計指針を述べる文」かで分け、前者は実装と一致させ、後者は実装に先行して規約として書いてよい。
