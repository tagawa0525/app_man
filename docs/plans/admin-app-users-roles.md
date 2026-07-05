# /admin/app-users・/admin/roles (第 4 グループ 第 4 PR)

## Context

仕様 §6.1 のアプリユーザ管理・ロール管理 (いずれも system_admin)。仕様は
画面名のみのため、詳細は既存規約 (論理削除の日時カラム / audit / CLI との
役割分担) から最小設計する。作成は CLI (`appmgr-create-app-user`) の責務の
まま、UI は状態管理 (無効化・ロール) に限定する。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| app-users 一覧 | username / auth_type / 連携ユーザ / notify_email / 状態 (disabled_at) / アクティブロール数 | 運用把握に必要な最小 |
| 無効化・再有効化 | `disabled_at = CURRENT_TIMESTAMP` / NULL 化。**自分自身の無効化は 400** | 論理削除規約。全 system_admin ロックアウトの最短経路を塞ぐ |
| notify_email 編集 | 行内フォーム。空 = NULL。形式検証は「@ を含む」程度の軽量 (本格検証は通知フェーズで) | ローカル admin の通知先 (§7.3)。過剰検証しない |
| 新規作成 UI | なし | パスワード入力を伴う作成は CLI (TTY / env) の責務。AD ユーザは AD フェーズで自動作成 |
| roles 画面 | app_user 選択 → アクティブロール一覧 (role / 部署 / granted_at) + 付与フォーム + 剥奪ボタン | user_department_roles の直接操作 |
| 付与の検証 | role は AllRoles のみ。system_admin は department NULL 固定、他ロールは現役部署必須 (廃止部署 400)。アクティブ重複は 409 (000006 の 2 本立て部分 UNIQUE が backstop) | create-app-user CLI と同じ規則。DB 制約はレース時の最終防衛 |
| 剥奪 | `revoked_at = CURRENT_TIMESTAMP`。**自分の system_admin ロールと、最後のアクティブ system_admin ロールの剥奪は 400** | ロックアウト防止 2 層 (自分 + 全体) |
| 無効化とロール | 無効化してもロール行は触らない (認証時に disabled を弾く既存挙動) | 再有効化で元の権限に戻る方が運用事故が少ない。無効化ユーザはアクティブ system_admin 数の勘定から除外する |
| audit | app_user.disable / app_user.enable / app_user.notify_email_change {old,new} / role.grant {role,department_id} / role.revoke {role,department_id} を同一 tx で記録 | 権限変更は内部統制の中核 |
| 認可 | systemAdmins 束 | §6.1 |

## 対象スコープ

- repository: app_users 一覧 (ロール数付き) / disable / enable /
  notify_email 更新、user_department_roles のアクティブ一覧 (部署 JOIN) /
  付与 (既存 CreateUserDepartmentRole 流用可否を確認) / 剥奪 /
  アクティブ system_admin 数 (無効化ユーザ除外)
- web: admin_app_users.go + admin_roles.go + view + ルート + audit
- handler テスト (ロックアウト防止 2 種・重複 409・廃止部署 400 含む)

### 範囲外

- 作成 / パスワードリセット UI (CLI の責務)
- AD 由来ユーザの自動管理 (AD フェーズ)
- 部署スコープ認可 (継続負債)

## TDD コミット順序

1. `docs(plans): 本 Plan`
2. `feat(repository): アプリユーザとロール管理のクエリ (sqlc)`
3. `test(web): app-users 一覧/無効化/メール編集と自己無効化防止 (RED)`
4. `feat(web): /admin/app-users 画面 (GREEN)`
5. `test(web): ロール付与/剥奪とロックアウト防止 (RED)`
6. `feat(web): /admin/roles 画面 (GREEN)`

## 受け入れ基準

- 全ゲート緑
- 実サーバ: 一覧表示 → viewer ユーザを無効化 (ログイン不能になる) →
  再有効化 (ログイン可)。notify_email 変更。自分の無効化 400。
  ロール付与 (viewer + 部署) → 重複 409 → 剥奪 → 再付与可。
  自分の system_admin 剥奪 400。唯一の system_admin の剥奪 400
  (別 admin を足せば剥奪可)。audit_logs に 5 種の action。
  dept_security_admin は両画面 403

## 想定リスク

- **無効化と既存セッション**: 無効化してもセッションが残ると操作継続
  できる可能性。AuthMiddleware が毎リクエストで disabled を見ているか
  実装時に確認し、見ていなければ本 PR で該当ユーザのセッション削除を
  無効化処理に含める (発見事項としてコミットに記録)
