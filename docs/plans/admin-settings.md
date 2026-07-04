# /admin/settings — app_settings 編集画面 (第 4 グループ 第 2 PR)

## Context

仕様 §5.11「app_settings テーブルを編集する画面 (system_admin のみ)。
変更は audit_logs に記録」。prune-logs (PR #23) が読む保持期間 4 キーを
UI から変更可能にする。prune-logs 検証で見つけた「前後空白つき値の
厳格拒否」は入口 (本画面) で trim して解決する。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| 編集対象 | 仕様 §5.11 の既知 5 キー (notification_max_retry + retention_days_* 4 種) の固定リスト。任意キーの新規作成は不可 | 自由記入はタイポで「効かない設定」を作る事故源。消費者が増えたらリストに足す |
| 表示 | キーごとに 現在値 (未設定なら「既定値 N を使用中」) + 説明 + 用途 (どのバイナリが読むか) | 運用者が既定値フォールバックの仕組みを画面で理解できる |
| 更新 | POST /admin/settings/{key} (value)。**TrimSpace 後に**正整数検証 (5 キーとも正整数)。UPSERT + updated_by_app_user_id | prune-logs の resolveRetentionDays と同じ検証基準。空白起因の exit 1 を入口で防ぐ |
| 既定値へ戻す | POST /admin/settings/{key}/reset で行 DELETE (キー不在 = 既定値の設計に合わせる) | 「既定値と同じ値の行」を残すより状態が明確 |
| audit | app_setting.change {key, old, new} / app_setting.reset {key, old} を同一 tx で記録 | §5.11「変更は audit_logs に記録」。承認 PR の方式踏襲 |
| 認可 | system_admin のみ (systemAdmins 束) | §6.1 |
| prune-logs 側 | 変更しない (キー不在 = 既定値・不正値 exit 1 の既存挙動維持)。本画面が正整数のみ書き込むため実運用で exit 1 経路は塞がる | 多層防御として CLI 側検証は残す |

## 対象スコープ

- repository: `ListAppSettings :many` / `UpsertAppSetting :one` /
  `DeleteAppSetting :execrows`
- web: settings.go (GET /admin/settings、POST {key}、POST {key}/reset) +
  view/settings/ + ルート + audit
- 既知キーのレジストリ (キー / 説明 / 既定値 / 消費バイナリ) は web 層の
  定数テーブル (prune-logs の既定値定数と重複するが、値の正本は仕様
  §5.11。コメントで相互参照)

### 範囲外

- 任意キー編集・notification_max_retry の消費実装 (通知フェーズ)
- prune-logs 側の trim 対応 (入口で解決)

## TDD コミット順序

1. `docs(plans): 本 Plan`
2. `feat(repository): app_settings の一覧・UPSERT・削除クエリ (sqlc)`
3. `test(web): 設定一覧/更新/リセット/検証/認可 (RED)`
4. `feat(web): /admin/settings 画面と audit 記録 (GREEN)`

## 受け入れ基準

- 全ゲート緑
- 実サーバ: system_admin が retention_days_audit_logs を変更 →
  prune-logs が新値で動く (dry-run で確認)。` 30 ` (前後空白) 入力が
  30 として保存される。`abc`・`0`・`-1` は 400。リセットで行が消え
  「既定値を使用中」表示に戻る。audit_logs に change / reset。
  dept_security_admin は 403

## 想定リスク

- **既定値の二重管理**: prune-logs の定数と画面レジストリの値ズレ。
  仕様 §5.11 を正本とするコメントで抑止 (テストで両者の一致を固定
  できるなら固定する — 実装時に判断)
