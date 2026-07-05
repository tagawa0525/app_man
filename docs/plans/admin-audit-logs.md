# /admin/audit-logs — 監査ログ閲覧 (第 4 グループ 第 3 PR)

## Context

仕様 §6.1 の監査ログ画面 (system_admin)。license_keys.view / approval.*/
product.default_approval_change / app_setting.* / bootstrap_import と
書込み経路が揃い、閲覧手段が SQL 直叩きしかない状態を解消する。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| 一覧 | occurred_at 降順、100 件 + 「さらに表示」(id カーソル `?before_id=`) | OFFSET はログ肥大で劣化する。id は AUTOINCREMENT で時系列に単調 |
| フィルタ | action (前方一致) / entity_type (完全一致) / app_user (username 完全一致) | 運用の主用途は「誰が / 何を」。日時範囲は必要になったら |
| 表示列 | 日時 (JST 表示) / 操作者 (app_users JOIN、NULL = CLI 実行) / action / entity (type/id) / diff_json | diff_json は `<details>` で折り畳み生 JSON 表示 (整形は将来) |
| 書込み UI | なし (閲覧専用)。削除は prune-logs の責務 | 監査ログの完全性 |
| 認可 | system_admin のみ | §6.1 |
| audit の audit | 閲覧自体は記録しない | 閲覧記録はノイズ (仕様も要求しない) |

## 対象スコープ

- repository: `ListAuditLogs :many` (フィルタ 3 種は NULL 許容引数 +
  `?1 IS NULL OR ...` 方式、before_id、LIMIT 101 で has_more 判定)
- web: audit_logs.go (GET /admin/audit-logs) + view/auditlogs/
- handler テスト (フィルタ・カーソル・403)

### 範囲外

- 日時範囲フィルタ・CSV エクスポート (/admin/export の責務)・diff 整形表示

## TDD コミット順序

1. `docs(plans): 本 Plan`
2. `feat(repository): 監査ログ一覧クエリ (sqlc)`
3. `test(web): 監査ログ閲覧のフィルタ/カーソル/認可 (RED)`
4. `feat(web): /admin/audit-logs 閲覧画面 (GREEN)`

## 受け入れ基準

- 全ゲート緑
- 実サーバ: 蓄積済み audit (設定変更等) が降順表示、操作者 username
  表示 (CLI 起源は「-」)、action フィルタ・before_id カーソルが機能、
  dept_security_admin 403

## 想定リスク

- 特になし (読み取り専用・既存テーブル)
