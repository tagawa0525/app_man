# ライセンス割当 (フェーズ 6 第 2 PR — L-2)

## Context

L-1 (licenses CRUD, PR #24) がマージ済み。第 2 PR として user_assignments /
device_assignments (migration 000002 で定義済み) を駆動する割当機能を実装
する。仕様 §5.2 (license_manager の「自部署のライセンス・割当・証書操作」)、
v_license_usage (migration 000006) による過不足の可視化。

割当専用画面は仕様 §6.1 に存在しない。割当の操作・表示は**ライセンス詳細
画面 (`/licenses/{id}`) の割当セクション**として実装する。

## 事前確認 (2026-07-04)

- `UNIQUE(license_id, user_id, revoked_at)` は SQLite では **NULL 同士を
  別値として扱うため、アクティブ割当 (revoked_at IS NULL) の重複を防げない**。
  重複チェックはアプリ側で行う必要がある (INSERT 前に既存アクティブ割当を
  確認)。スキーマ変更 (部分 UNIQUE インデックス) はマイグレーション追加に
  なるため本 PR で対応する (下記)
- **訂正 (2026-07-04, PR #25 Copilot レビューで発覚)**: 上記の NULL 問題
  自体は正しいが、**migration 000006 が既に部分 UNIQUE インデックス
  `uniq_user_assignments_active` / `uniq_device_assignments_active` で
  対処済み**だった。本 PR で追加した migration 000008 は完全に冗長のため
  撤去し、既存インデックスを前提にする。見落としの経緯: 000002 のテーブル
  定義だけを見て 000006 のインデックス定義を確認しなかった
- v_license_usage は product 単位の集計 (total_owned は期限内のみ /
  installed_count / user_assigned_count / device_assigned_count)

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| UI の場所 | ライセンス詳細に「ユーザ割当」「端末割当」セクション。追加フォーム + 各行の解除ボタン | §6.1 に専用画面なし。割当はライセンスに従属する情報 |
| アクティブ重複の防止 | 既存の部分 UNIQUE インデックス `uniq_user_assignments_active` / `uniq_device_assignments_active` (migration 000006) を前提にし、handler でも事前チェックして 409 + フォームエラー | 既存のテーブル UNIQUE は NULL で効かないが、000006 の部分 UNIQUE が根本を塞いでいる。handler チェックでユーザ向けエラーメッセージを出す 2 層。解除→再割当は revoked_at 埋めで部分インデックス対象外になり可能 |
| 解除 | `revoked_at = CURRENT_TIMESTAMP` の論理解除 (:execrows、`WHERE id = ? AND revoked_at IS NULL`)。物理 DELETE なし | 論理削除の日時カラム規約。監査情報 |
| 超過割当 | ブロックしない。license 詳細に「割当数 / 保有数」を表示し、count_unit 側の合計が total_count を超えたら警告表示 | 本システムの思想は可視化 (整合性チェックも警告のみ)。total_count NULL = 無制限は警告なし |
| user/device 両割当 | count_unit に関わらず両方許可。警告判定は count_unit に一致する側の割当数で行う | 契約実態は混在しうる。v_license_usage も両方を別々に数える設計 |
| 割当対象の選択肢 | ユーザ = 在職者のみ (deactivated_at IS NULL)、端末 = 現役のみ (retired_at IS NULL)。既存割当の表示は退職者・退役端末も出す (状態注記付き) | 退職者への新規割当は事故。既存割当の可視化は「退職者の未解除割当」検出 (AD フェーズ) の前提 |
| user_assignments の付帯項目 | フォームは note と external_account_id (任意) のみ。provisioned_at / deprovisioned_at は本 PR では常に NULL | SaaS アカウント棚卸し (§5.4 以降) で使う項目。今は入力経路だけ用意しない (最小実装) |
| v_license_usage の表示先 | products 詳細ページに「ライセンス利用状況」サマリ (total_owned / user_assigned / device_assigned / installed) を追加 | 過不足の product 単位可視化。installed_count は SKYSEA 未取込のため 0 表示になるが、取込後に自然に埋まる |
| 認可 | 割当セクションの閲覧 = ライセンス詳細と同じ viewer 以上。追加・解除 = license_manager 以上 | §6.1 / §7.1。部署スコープは L-1 と同じく認可強化 PR へ (負債継続) |
| クエリのコメント | ASCII 英文限定 | sqlc v1.31.1 の非 ASCII コメントバグ |

## 対象スコープ

### 範囲内

- アクティブ割当の重複防止は既存 `uniq_*_assignments_active` (000006)
  を前提にする (migration 追加なし。当初の 000008 は冗長だったため撤去)
- `db/queries/assignments.sql` + 生成物:
  - `ListActiveUserAssignmentsByLicense` / `ListActiveDeviceAssignmentsByLicense`
    (users / devices を JOIN、状態カラム含む)
  - `CreateUserAssignment` / `CreateDeviceAssignment`
  - `RevokeUserAssignment` / `RevokeDeviceAssignment` (:execrows)
  - `CountActiveUserAssignment` / `CountActiveDeviceAssignment` (重複チェック)
  - `GetLicenseUsageByProduct` (v_license_usage から 1 行)
- `internal/handler/web/assignments.go`:
  - POST `/licenses/{id}/assignments/users` (追加)
  - POST `/licenses/{id}/assignments/users/{aid}/revoke`
  - POST `/licenses/{id}/assignments/devices` (追加)
  - POST `/licenses/{id}/assignments/devices/{aid}/revoke`
- `internal/handler/web/licenses.go` show: 割当一覧・選択肢・超過警告を
  詳細画面 props に追加
- `internal/view/licenses/show.templ`: 割当セクション追加
- `internal/handler/web/products.go` show + `internal/view/products/show.templ`:
  ライセンス利用状況サマリ
- handler テスト (割当追加 / 重複 409 / 解除 / 再割当 / 退職者選択不可 /
  超過警告 / ロール 403)

### 範囲外 (別 PR)

- 部署スコープ認可 (継続負債)
- 退職者の未解除割当アラート画面 (AD フェーズ)
- provisioned_at / deprovisioned_at の入力・SaaS 棚卸し
- `/my/licenses` (セルフサービス、AD 後)
- ダッシュボードの過不足ウィジェット (第 4 グループ)
- installations との突合 (SKYSEA 後)
- 割当選択肢の検索・ページング UI (選択肢クエリは LIMIT なしの全件。
  対象が数千件規模になったら検討する継続負債)

## 内部設計

### アクティブ重複の DB 制約 (migration 000006 に既存)

```sql
CREATE UNIQUE INDEX uniq_user_assignments_active
  ON user_assignments(license_id, user_id)
  WHERE revoked_at IS NULL;
CREATE UNIQUE INDEX uniq_device_assignments_active
  ON device_assignments(license_id, device_id)
  WHERE revoked_at IS NULL;
```

migration 追加は不要 (必須スキーマ版数は 7 のまま)。当初 migration
000008 として同一の部分 UNIQUE を追加したが冗長のため撤去した。
handler の事前チェック後のレースはこの既存インデックスの UNIQUE 違反
として現れるので 409 に変換する。

### 超過警告の判定 (handler)

```text
capacity  = license.total_count (NULL なら無制限 → 警告なし)
assigned  = count_unit == "user" ? activeUserAssignments : activeDeviceAssignments
over      = capacity != NULL && assigned > capacity
```

### 解除の :execrows

`WHERE id = ? AND license_id = ? AND revoked_at IS NULL` で 0 行なら
404 相当 (既に解除済み / 他ライセンスの割当 ID)。二重 POST に安全。

## TDD コミット順序

1. `docs(plans): ライセンス割当 (L-2) の Plan ファイル`
2. `feat(db): アクティブ割当の部分 UNIQUE インデックス (migration 000008)`
   — **後に冗長と判明し 8 で撤去**
3. `feat(repository): 割当と利用状況のクエリ (sqlc)`
4. `test(web): 割当の追加/重複/解除/再割当/警告 (RED)`
5. `feat(web): 割当ハンドラと詳細画面の割当セクション (GREEN)`
6. `feat(web): products 詳細にライセンス利用状況サマリ`
7. `docs(plans): 000006 に既存の部分 UNIQUE を見落としていた記録 (000008 撤去)`
8. `revert(db): 冗長な migration 000008 を撤去 (000006 に既存)`
9. `fix: 割当まわりのコメントを実挙動と既存インデックスに合わせる (Copilot 指摘)`

GREEN ごとに `make test` / `make lint` 緑。migration 追加後は
`make migrate-up` 相当のテスト DB 更新 (handlertest が embed 適用なら自動)。

## 受け入れ基準

- `make build` / `make test` / `go test -race ./...` / `make lint` 全緑
- 実サーバで:
  - license_manager がライセンス詳細からユーザ/端末を割当できる
  - 同一ユーザの二重割当は 409 (フォームエラー)。解除後の再割当は成功
  - 解除で行が消えず revoked 扱いになる (詳細画面から消える)
  - total_count=1 で 2 件目 (解除→別ユーザ割当は 1 件のまま) を超えると
    警告表示。total_count NULL は警告なし
  - 退職者 (deactivated_at NOT NULL) が割当選択肢に出ない
  - viewer は割当 POST が 403
  - products 詳細に total_owned / 割当数 / installed (0) が表示される
- DB 制約: 直接 SQL で同一 (license, user) のアクティブ割当を 2 行
  INSERT すると `uniq_user_assignments_active` (000006) の UNIQUE 違反
  になる (device 側は `uniq_device_assignments_active`)

## 想定リスク

- **既存データとの整合**: 部分 UNIQUE インデックス追加時に既存の重複
  アクティブ割当があると migration が失敗するが、割当機能自体が本 PR
  初出のため既存データは存在しない (bootstrap の --kind assignments も
  未実装)。リスクなし
- **スキーマ版数チェック**: appmgr-server は起動時に必須版数を検査する
  設計。版数定数の更新漏れは `make test` の migrate テストで検出される
  想定 (実装時に internal/db/migrate.go を確認)
