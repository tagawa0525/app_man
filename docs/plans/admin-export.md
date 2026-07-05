# /admin/export — エクスポート (第 4 グループ 最終 PR)

## Context

仕様 §5.10。全データ Excel (複数シート)・ZIP 一括 (DB スナップショット +
全証書 + meta.yml)。操作は audit_logs 記録。system_admin のみ (§6.1)。
これで第 4 グループ (可視化・管理画面) の画面群が /admin/integrity
(SKYSEA 後で価値が出る) を除き完結する。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| Excel ライブラリ | `github.com/xuri/excelize/v2` を新規依存として追加 | 仕様が Excel (複数シート) を明示し標準ライブラリでは実現不能。pure Go / CGO 不要で単一バイナリ制約に適合。依存追加はこの仕様要求を根拠とする |
| シート構成 | vendors / products / departments / users / devices / licenses / user_assignments / device_assignments / approvals (department_product_approvals) / app_settings の 10 シート | 「全データ」= 業務データの正本。sessions / audit_logs は除外 (機微・肥大。audit は画面と prune で管理) |
| ライセンスキー | チェックボックス opt-in (デフォルト含めない)。含めない場合は licenses シートに product_keys 列自体を出さない。**含めた場合は audit の diff_json に include_keys: true を記録** | 仕様どおり + write-only 方針との整合 (エクスポートは正当な閲覧経路で、audit 必須) |
| Excel 生成 | `internal/export` パッケージに `WriteExcel(ctx, q, w io.Writer, includeKeys bool) error`。シートごとに repository の List 系を流用し、無ければ最小限の全件クエリを追加 | web ハンドラから分離してテスト可能に |
| ZIP 内容 | (1) `db-snapshot.db` — `VACUUM INTO` で一時ファイルに書き出して格納 (backup と同方式、完成品保証) (2) `licenses/` ツリー全量 (証書 + meta.yml) | 仕様「DB スナップショット + 全証書 + meta.yml」。VACUUM INTO は稼働中 DB の整合スナップショットを取る唯一の安全手段 |
| ZIP の一時ファイル | `os.MkdirTemp` の専用ディレクトリ配下に VACUUM INTO → zip へコピー → defer RemoveAll。パスは応答に出さない (CreateTemp は空ファイルを先に作るため VACUUM INTO が失敗する — backup PR の知見) | サーバの作業領域を汚さない |
| ストリーミング | ZIP は `zip.NewWriter(w)` で応答へ直接書く。Excel は excelize が全量メモリ構築 (想定規模で問題なし) | 数百 MB 級になったら見直し (今はしない) |
| ファイル名 | `appmgr-export-<YYYYMMDD-HHMMSS>.xlsx` / `.zip` (JST) | 運用者向け。backup の命名と整合 |
| audit | export.excel {include_keys} / export.zip を**ダウンロード開始前に記録** (記録失敗は 500 で配信しない) | キー閲覧と同方針: 記録なしの機微データ持ち出しを作らない |
| 画面 | GET /admin/export: 説明 + Excel フォーム (キー checkbox) + ZIP ボタン。ダウンロードは POST (CSRF 必須) | GET ダウンロードだと audit 迂回リンクが作れてしまう |
| 認可 | systemAdmins 束 | §6.1 |

## 対象スコープ

- go.mod: excelize/v2 追加
- `internal/export/`: WriteExcel + WriteZip (+ 単体テスト: シート存在・
  行数・キー列の有無、zip エントリ構成)
- repository: 不足する全件 List クエリ (必要分のみ)
- web: export.go (GET 画面 / POST excel / POST zip) + view + ルート + audit
- handler テスト (audit 記録・キー opt-in・403)

### 範囲外

- CSV 形式・ページング・非同期生成 (要件外)
- /admin/integrity 画面 (SKYSEA 後)

## TDD コミット順序

1. `docs(plans): 本 Plan`
2. `chore(deps): excelize/v2 を追加 (仕様 5.10 の Excel エクスポート)`
3. `test(export): Excel シート構成とキー opt-in / ZIP 構成 (RED)`
4. `feat(export): WriteExcel / WriteZip 実装 (GREEN)`
5. `test(web): /admin/export の audit と認可 (RED)`
6. `feat(web): /admin/export 画面と配信 (GREEN)`

## 受け入れ基準

- 全ゲート緑
- 実サーバ: Excel ダウンロード → 10 シート・データ行あり・デフォルトで
  product_keys 列なし → キー含める → 列あり + audit に include_keys。
  ZIP → db-snapshot.db (開いて SELECT 可能) + licenses/ の証書と
  meta.yml が入っている。audit 記録前のエラーでは配信されない。
  dept_security_admin 403
- ZIP 内 db-snapshot.db にセッションテーブルも含まれる (DB 全量) が、
  ZIP 自体の取得が system_admin + audit 記録済みのため許容 (README
  等での注意書きは不要 — バックアップと同等の取扱い)

## 想定リスク

- **excelize の依存サイズ**: ビルド時間・バイナリサイズ増。仕様要求の
  ため受容。CGO 不要は確認済み (pure Go)
- **VACUUM INTO と ModeGlobal**: appmgr-server は バッチのグローバル
  ロック対象外のため、backup 実行中に ZIP エクスポートすると VACUUM
  同士が競合しうる → 失敗は 500 + error ログで返す (当初 503 相当と
  したが、BUSY 判定分岐はテスト不能な狭経路で、運用対応はどちらも
  「再実行」のため区別の利得が薄いと判断し 500 に統一)。発生条件が
  狭いため MVP はこれで足りる
