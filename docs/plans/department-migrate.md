# 部署改廃 UI (フェーズ 13 — /admin/departments/migrate)

## Context

仕様 §5.15 / §6.1。廃止部署 (valid_to NOT NULL) の資産を後継部署へ
一括移管する system_admin 画面。AD 同期による自動廃止はフェーズ 4 だが、
部署の手動廃止 (departments CRUD) は既存のため、本画面は今でも価値がある。
これで外部データ待ち以外の実装項目が完了する。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| 画面 | GET: 廃止部署の選択 → 選択後に移管対象プレビュー (所管ライセンス数・アクティブ承認数・移管先候補 = 現役部署)。POST: 一括処理 → 結果を flash で表示 | §5.15 の「画面を開き、後継部署を指定、ボタン操作で一括処理」 |
| licenses 移管 | `owning_department_id` を後継へ UPDATE。UNIQUE(product, dept, slug) 衝突する行は**スキップして報告** (移管されず廃止部署に残る) | 衝突の自動リネームは事故源。運用者が slug を変えて再実行する |
| approvals コピー | 廃止部署のアクティブ承認を後継部署に新規作成 (approval_source='direct'、approved_by = 実行者、note に「部署改廃により <旧部署> から移管」)。後継に同 product のアクティブ承認が既にある行は**スキップして報告** (仕様「重複時は手動マージ」) | 仕様どおり。元の行は revoked にせず残す (廃止部署の履歴として無害。取消理由の捏造を避ける) |
| fs_dir_path | 変更なし (構成は vendor/product/slug で部署を含まない) | FS 移動は不要 — 確認済み |
| トランザクション | 全処理 + audit を 1 tx | 部分移管の中途半端な状態を残さない (スキップは意図的な残置であり別) |
| audit | department.migrate {from, to, licenses_moved, licenses_skipped, approvals_copied, approvals_skipped} を entity=department (廃止部署 id) で記録 | §5.15「audit_logs に記録」 |
| 検証 | 廃止部署のみ移管元に選択可 / 後継は現役のみ / 同一部署は 400 | 誤操作防止 |
| 冪等性 | 再実行しても安全 (移管済みなら対象 0 件、スキップ分だけ再報告) | 衝突解消後の再実行が正規の運用フロー |

## 対象スコープ

- repository: 廃止部署一覧 / 移管対象カウント (プレビュー) /
  licenses の衝突判定付き一括 UPDATE (衝突行はスキップ) /
  アクティブ承認のコピー対象一覧
- web: departments_migrate.go (GET / POST) + view + ルート
  (system_admin) + audit
- handler テスト (移管成功・衝突スキップ・重複承認スキップ・
  廃止/現役の検証・403・audit)

### 範囲外

- AD 同期による自動廃止 (フェーズ 4)
- 割当・証書の部署概念 (ライセンスに従属するため移管で自動的に追従)

## TDD コミット順序

1. `docs(plans): 本 Plan`
2. `feat(repository): 部署移管のクエリ (sqlc)`
3. `test(web): 移管の一括処理/衝突スキップ/検証/認可 (RED)`
4. `feat(web): /admin/departments/migrate 画面 (GREEN)`

## 受け入れ基準

- 全ゲート緑
- 実サーバ: 部署 B を廃止 → 移管画面で B → A を実行 → B のライセンスが
  A 所管になり承認が A にコピーされる。slug 衝突ライセンスと重複承認は
  残置 + 件数報告。audit 1 行。再実行は 0 件で安全。
  dept_security_admin 403

## 想定リスク

- **移管後の部署スコープ認可**: 移管でライセンスが A 所管になると
  B の license_manager は触れなくなる (PR #38 の想定どおりの挙動)
