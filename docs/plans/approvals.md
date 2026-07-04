# 承認管理 (フェーズ 9 — 第 3 グループ)

## Context

マスタ・ライセンス・運用基盤が完了し、AD / SKYSEA 非依存で実装できる
最後の大きな業務機能。仕様 §5.5 (評価ロジック) / §6.1 の 3 画面
(`/approvals` = dept_security_admin 以上、`/approvals/{dept_id}/{product_id}`
= 同、`/admin/global-approvals` = system_admin)。

事前確認: `uniq_dept_product_approvals_active` (000006) が
アクティブ承認の重複を既に防いでいる (migration 追加不要。L-2 の教訓)。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| 評価ロジックの配置 | `internal/approval.Evaluate(defaultStatus, rec *Record, now) Verdict` を純関数で新設。DB lookup は呼び出し側 | 仕様 §5.5 の表をそのまま純関数化。後続のダッシュボード / SKYSEA 突合 / セルフサービスが同じ関数を使う |
| Verdict | allowed / prohibited / unapproved / unreviewed / conditional / expired の列挙 | 仕様の 許可 / 禁止 / 未承認 / 未審査 / 条件付き / 期限切れ (未承認扱い) に対応。expired は unapproved の亜種だが表示で区別したいので別値にし、IsUsable() 等の判定ヘルパで丸める |
| scope_type | MVP は `department_wide` のみ画面から設定可。specific_users / specific_devices は評価ロジックだけ実装 (DDL 済みデータを正しく評価) し、設定 UI は作らない | 仕様「MVP では画面から設定不可 (DDL のみ)」。評価側を作っておけば bootstrap 等で投入されたデータも正しく扱える |
| /approvals 画面 | 部署選択 (dept_security_admin 以上) → 製品 × 承認状態の一覧。各行から登録・編集へ | §6.1。部署は現役のみ選択可 |
| 登録・編集画面 | status (approved / conditional / prohibited)、conditions (conditional 時)、expires_at (任意)、note。既存アクティブ承認がある場合は「取消して再登録」(revoked_at + revoke_reason + 新規行) | UNIQUE(dept, product, active) のため変更 = 取消 + 新規。承認履歴が残る (revoked 行は監査証跡) |
| 取消 | revoked_at = CURRENT_TIMESTAMP + revoked_by + revoke_reason (必須) | 論理削除の日時カラム規約 + 内部統制の理由記録 |
| approved_by / revoked_by | セッションの AppUserID | 監査情報 |
| approval_source | 常に 'direct' | 申請フロー (approval_requests) は MVP 外 (DDL のみ) |
| /admin/global-approvals | 製品一覧 + default_approval_status の変更 (globally_approved / globally_prohibited / unknown / department_discretion) | §6.1、system_admin。products テーブルの既存カラム更新 |
| audit_logs | approval.grant / approval.revoke / product.default_approval_change を記録 (recordAudit 再利用)。diff_json に主要フィールド | 承認は内部統制の中核。audit 網羅 (フェーズ 15) を待たず今入れる |
| 認可 | ロール階層のみ (dept_security_admin 以上 / system_admin)。部署スコープは継続負債 | 既存方針と同じ |
| 期限切れの扱い | Evaluate が expires_at <= now で expired を返す。DB の行は触らない (バッチ不要) | 仕様「期限切れ (未承認扱い)」は評価時判定で足りる |

## 対象スコープ

### 範囲内

- `internal/approval/`: Evaluate + Verdict + テスト (仕様 §5.5 の全分岐)
- repository: dept_product_approvals の一覧 (部署別、製品 JOIN) / 取得 /
  作成 / 取消、products.default_approval_status 更新、scope_users /
  scope_devices の存在確認 (評価用)
- web: 3 画面 + ルート + audit 記録
- handler テスト (ロール別 403 含む)

### 範囲外 (別 PR)

- approval_requests (申請フロー)・specific_* の設定 UI
- ダッシュボード / 突合での Evaluate 消費 (第 4 グループ以降)
- 部署スコープ認可 (継続負債)

## TDD コミット順序

1. `docs(plans): 本 Plan`
2. `test(approval): 仕様 5.5 の評価ロジック全分岐 (RED)`
3. `feat(approval): Evaluate 実装 (GREEN)`
4. `feat(repository): 承認のクエリ (sqlc)`
5. `test(web): 承認一覧/登録/取消/全社設定/認可 (RED)`
6. `feat(web): 承認 3 画面と audit 記録 (GREEN)`

## 受け入れ基準

- 全ゲート緑
- Evaluate: globally_approved/prohibited/unknown → 即決 /
  department_discretion で レコードなし = unapproved、approved +
  department_wide = allowed、approved + specific_users (user_id あり/なし)、
  conditional、prohibited、expires_at 経過 = expired
- 実サーバで: dept_security_admin が承認登録 → 一覧に反映 → 取消
  (理由必須) → 履歴が残り新規登録可能 / viewer は 403 /
  global-approvals は system_admin のみ / audit_logs に grant / revoke /
  default_approval_change が記録される

## 想定リスク

- **評価ロジックの消費者不在**: 本 PR では画面表示のみが消費者。突合・
  ダッシュボードで本格消費するまで Verdict の粒度が仮説だが、純関数
  なので変更コストは小さい
