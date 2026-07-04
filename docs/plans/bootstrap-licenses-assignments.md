# import-bootstrap の licenses / assignments 対応 (運用基盤の残り 第 3 PR)

## Context

フェーズ 6 完了によりライセンス・割当のスキーマと業務ロジックが存在する。
本 PR は `appmgr-import-bootstrap` に `--kind licenses / user_assignments /
device_assignments` を追加し、既存 Excel からの初期データ移行 (仕様 §9、
受け入れ基準 15「製品マスタ・ライセンス・割当が一括登録され、audit_logs
に bootstrap_import として記録される」) を完成させる。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| kind の分割 | `licenses` / `user_assignments` / `device_assignments` の 3 kind | 既存の per-table kind パターン (vendors 等 6 kind) に整合。1 CSV 1 テーブル |
| 参照解決 | 自然キーで解決: vendor は name、product は (vendor_name, canonical_name, edition)、department は code、user は employee_code、device は asset_code、license は (vendor_name, product, edition, department_code, license_slug) | products の UNIQUE(vendor_id, canonical_name, edition) に一致。ID 直書きは移行元 Excel に存在しない |
| licenses CSV 列 | vendor_name, product_name, edition, department_code, license_slug, display_name, total_count, count_unit, contract_type, purchased_at, started_at, expires_at, vendor_order_no, purchaser, unit_price, currency, product_keys, note | スキーマの全入力項目。日付は YYYY-MM-DD、空 = NULL |
| fs_dir_path | web と同じ規則 (`licenses/<v>/<p>/<slug>`、internal/slug) で計算し DB 保存のみ。**物理ディレクトリと meta.yml は作らない** — 投入後に `appmgr-generate-meta` を 1 回実行する運用を README に明記 | 初期移行は数百行になりうるため FS 操作は一括再生成に寄せる (責務の重複を避ける)。fs_dir_path 衝突は検証フェーズでエラー (投入前に Excel 側で解消させる) |
| assignments CSV 列 | user: ライセンス参照 5 列 + employee_code, external_account_id, note / device: 同 + asset_code, note | web の割当フォームと同じ入力項目 |
| 割当の検証 | 退職者 (deactivated_at) / 退役端末 (retired_at) への割当、同一 CSV 内・DB 既存のアクティブ重複はエラー | web の挙動 (400 / 409) と同基準。000006 の部分 UNIQUE が最終防衛 |
| バリデーション方式 | 既存 bootstrap の流儀: 全行検証 → エラーがあれば 1 件も投入しない (1 トランザクション)。行番号付きエラー報告 | 既存 Importer の設計。初期移行は「全部通るまで直す」が正しい |
| audit_logs | commit 成功後に 1 行: action=`bootstrap_import`, entity_type=kind, app_user_id=NULL, diff_json=`{"file":..,"rows":N}`。**既存 6 kind にも同時に適用** (受け入れ基準 15 は製品マスタ含む)。dry-run は記録しない | 受け入れ基準 15。CLI 実行のため app_user は NULL (実行者は OS レベルの監査に委ねる) |
| contract_type / count_unit | web と同じ選択値のみ (perpetual/subscription、device/user) | 入口による表記揺れを防ぐ |

## 対象スコープ

### 範囲内

- `internal/bootstrap/licenses.go` + `_test.go` / `user_assignments.go` +
  `_test.go` / `device_assignments.go` + `_test.go`
- `internal/bootstrap/bootstrap.go`: commit 後の audit_logs 記録 (全 kind)
- `cmd/import-bootstrap/main.go`: importerByKind に 3 kind 追加、
  usage 文字列更新
- 必要な sqlc クエリ (自然キー lookup 等) + 生成物
- `README.md`: 投入順 (マスタ → licenses → assignments) と投入後の
  generate-meta 実行を追記

### 範囲外 (別 PR)

- `--kind alias-resolve` (名寄せ = SKYSEA フェーズ)
- Shift_JIS 入力 / Excel 直読み
- 物理ディレクトリ・meta.yml 生成 (generate-meta の責務)

## TDD コミット順序

1. `docs(plans): 本 Plan`
2. `feat(repository): bootstrap 用の自然キー lookup クエリ (sqlc)`
   (必要な場合のみ。既存クエリで足りるなら省略可)
3. `test(bootstrap): licenses の投入と検証エラー (RED)`
4. `feat(bootstrap): LicensesImporter 実装 (GREEN)`
5. `test(bootstrap): user/device assignments の投入と検証エラー (RED)`
6. `feat(bootstrap): assignments Importer 実装 (GREEN)`
7. `test(bootstrap): commit 後に audit_logs へ bootstrap_import 記録 (RED)`
8. `feat(bootstrap): 全 kind の audit_logs 記録 (GREEN)`
9. `feat(cmd/import-bootstrap): 3 kind を配線し usage 更新`
10. `docs: README に初期移行の投入順と generate-meta 実行を追記`

## 受け入れ基準

- 全ゲート緑
- 実バイナリで: マスタ投入済み環境に licenses.csv 20 行 → dry-run で
  投入 0・検証結果表示 → --commit で 20 行 + fs_dir_path 正 +
  audit_logs 1 行。user/device assignments も同様
- 検証エラー系: 存在しない product / 廃止部署 / 退職者 / 重複割当 /
  不正日付 / 不正 enum が行番号付きで報告され、1 行も投入されない
- 投入後 generate-meta で全契約フォルダが生成される (連携確認)
- 既存 6 kind の投入でも audit_logs が記録される

## 想定リスク

- **既存 kind への audit 追加の影響**: Run の共通処理に足すため全 kind に
  一括適用される。既存テストは audit 行の有無を検証していないので
  非破壊 (新テストで固定)
- **自然キーの曖昧性**: edition NULL と空文字の扱いは products の
  UNIQUE と同じ規則に合わせる (実装時に既存 products importer を確認)
