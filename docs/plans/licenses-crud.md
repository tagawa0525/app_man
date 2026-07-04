# licenses CRUD (フェーズ 6 第 1 PR — L-1)

## Context

運用基盤 (backup / prune-logs) 完了後の再立案 (2026-07-04) に基づくフェーズ 6
(ライセンス・証書) の第 1 PR。licenses テーブル (migration 000002 で定義済み)
を駆動する CRUD 画面と DB 層を実装する。仕様 §5.2 / §6.1。

フェーズ 6 は 3 PR に分割する：

- **L-1 (本 PR)**: licenses CRUD。DB と画面のみ。**FS には一切触れない**
- L-2: 割当 (user_assignments / device_assignments) と v_license_usage 表示
- L-3: 証書ファイル (file_store 設定・物理ディレクトリ作成・アップロード /
  ダウンロード・マジックバイト検証・meta.yml 生成・ライセンスキー閲覧の
  audit_logs 記録)

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| FS 操作の範囲 | L-1 は `fs_dir_path` (相対パス文字列) を計算して DB に保存するのみ。物理ディレクトリは作らない | file_store 設定も物理配置も L-3 の責務に集約。L-1 期間中に作成された行の物理ディレクトリは appmgr-generate-meta (運用基盤の残り PR) が一括生成できるため、先行して文字列だけ確定させても破綻しない |
| fs_dir_path の形式 | `licenses/<vendor_slug>/<product_slug>/<license_slug>` (base_path からの相対) | base_path の移設に耐える。仕様 §3.2 のレイアウトそのまま |
| slug 生成 | 新規パッケージ `internal/slug`。仕様 §3.2 の規則: `/ \ : * ? " < > \|` と制御文字とスペースを `_` に置換、日本語はそのまま | vendor 名・product 名から都度導出する共通処理で、L-3 / generate-meta / check-integrity も使う (3 利用者が確定しているので抽象化は早すぎない) |
| slug 衝突 | license_slug は UNIQUE(product_id, owning_department_id, license_slug) で DB が防ぐ。fs_dir_path の衝突 (別 vendor/product が同じ slug に正規化される等) は L-1 では UNIQUE 制約なしのため検出のみ後送り | 衝突サフィックス `_2` の付与は物理ディレクトリを作る L-3 で実装 (FS の実在チェックと不可分のため) |
| 削除 UI | 作らない | 仕様 §5.2「レコード自体は削除せず、過去の契約履歴として残す」。満了 = expires_at (論理削除の日時カラム規約) |
| 満了フィルタ | 一覧はデフォルト現役のみ (`expires_at IS NULL OR expires_at >= date('now')`)。`?expired=1` で満了も表示 | 仕様 §5.2「デフォルトは現役のみ」 |
| 期限接近 | 一覧を期限昇順 (NULL = 無期限は最後) で並べ、90 日以内の行に警告表示 | 仕様「期限が近いライセンスの一覧表示」。専用画面ではなく一覧のソート + 強調で満たす (最小実装) |
| product_keys | **write-only**。登録・変更はフォームから可、表示は一覧・詳細とも「登録あり/なし」のみ。編集フォームは空欄 =変更なし、入力あり = 上書き | 仕様「閲覧時は audit_logs 記録」を満たす閲覧機能 (reveal + 監査記録) は L-3。それまで平文表示経路を作らない (パスワード欄の慣行と同じ) |
| 認可 | 一覧・詳細閲覧 = viewer 以上、新規・編集 = license_manager 以上 (`RequireRole`) | 仕様 §6.1 の必要ロール。**license_manager の部署スコープ制限 (自部署のみ操作可) は範囲外** — 現状の認可基盤はロール階層のみで部署照合を持たず、AD 未連携で利用者が管理者のみの現段階では実害がない。§7.2 完全対応は認可強化 PR として別送り (Plan に明記して負債を可視化) |
| 契約更新 | 特別な UI は作らない。新規作成で別レコードを作り、note に旧契約への言及を書く運用 | 仕様 §5.2「新ライセンスを別レコードで作成」「DB レベルの親子関係は持たせない」 |
| バリデーション | product_id / owning_department_id 実在 (廃止部署は新規選択不可)、license_slug・display_name・count_unit・contract_type 必須、total_count は NULL (無制限) or 正整数、日付は YYYY-MM-DD、unit_price は NULL or 非負整数 | スキーマの NOT NULL と業務整合。count_unit は device / user、contract_type は perpetual / subscription の選択式 (meta.yml 例・v_license_usage の前提) |
| updated_at | UPDATE 時に `CURRENT_TIMESTAMP` で更新 | スキーマ規約 |
| クエリのコメント | db/queries/licenses.sql 内は ASCII 英文限定 | sqlc v1.31.1 の非 ASCII コメントバグ (docs/plans/appmgr-prune-logs.md に記録) |

## 対象スコープ

### 範囲内

- `internal/slug/slug.go` + `slug_test.go`: `Slugify(s string) string` (仕様 §3.2 規則)
- `db/queries/licenses.sql` + `make generate` 生成物:
  - `ListLicenses` (現役のみ / 全件、products・vendors・departments を JOIN
    した表示用行、期限昇順 NULL 最後)
  - `GetLicenseByID` (JOIN 付き詳細)
  - `CreateLicense` / `UpdateLicense` (`updated_at = CURRENT_TIMESTAMP`)
- `internal/handler/web/licenses.go`: list / show / newForm / create /
  editForm / update (vendors.go の構成に合わせる)
- `internal/view/licenses/`: list / show / form の templ (+ 生成物コミット)
- `internal/handler/web/web.go`: ルート登録
  (GET /licenses = viewers、GET /licenses/{id} = viewers、
  new/create/edit/update = editors 相当の license_manager 以上)
- handler テスト (既存の session ベーステスト流儀)

### 範囲外 (別 PR)

- 割当 (L-2)、証書・meta.yml・file_store 設定・キー閲覧 (L-3)
- fs_dir_path 衝突時のサフィックス付与 (L-3)
- license_manager の部署スコープ認可 (認可強化 PR)
- 部署改廃時のライセンス移管 UI (フェーズ 13)
- `/my/licenses`・ダッシュボードのライセンス表示 (後続グループ)
- import-bootstrap の --kind licenses (運用基盤の残り PR)

## 内部設計

### internal/slug

```go
// Slugify は仕様 §3.2 の規則で文字列をファイルシステム安全な slug に
// 正規化する。日本語等の非 ASCII はそのまま保持し、Windows で使えない
// 文字 (/ \ : * ? " < > |)・制御文字・スペースを _ に置換する。
func Slugify(s string) string
```

先頭末尾の空白 trim 後に置換。空文字になった場合は "_" を返す (パス成分の
欠落防止)。衝突解決 (サフィックス) は持たない (L-3 の責務)。

### fs_dir_path の組み立て (handler 内)

```go
fsDirPath := path.Join("licenses",
    slug.Slugify(vendorName),
    slug.Slugify(productCanonicalName),
    slug.Slugify(licenseSlug))
```

区切りは `path.Join` (常に `/`)。Windows 実行時の物理パス変換は L-3 で
`filepath.FromSlash` を使う側の責務。

### クエリ設計の要点

- ListLicenses は 1 本で `?expired` の有無に対応するため
  `WHERE (?1 = 1 OR expires_at IS NULL OR expires_at >= date('now'))` の
  フラグ引数方式にする (sqlite で bool は int)
- 期限昇順 NULL 最後: `ORDER BY expires_at IS NULL, expires_at, id`
- 表示用に vendor_name / product_name / department_name を JOIN で取る

## TDD コミット順序

1. `docs(plans): licenses CRUD (L-1) の Plan ファイル`
2. `test(slug): 仕様 3.2 の slug 正規化規則 (RED)`
3. `feat(slug): Slugify 実装 (GREEN)`
4. `feat(repository): licenses の一覧・取得・作成・更新クエリ (sqlc)`
5. `test(web): licenses ハンドラの一覧/詳細/作成/編集 (RED)`
6. `feat(web): licenses ハンドラと templ 画面 (GREEN)`
7. `feat(web): licenses ルートを登録`

GREEN ごとに `make test` / `make lint` 緑。templ 追加時は `make generate`。

## 受け入れ基準

- `make build` / `make test` / `go test -race ./...` / `make lint` 全緑
- ブラウザ (実サーバ) で:
  - viewer 相当でライセンス一覧・詳細が見える。license_manager 以上で
    新規・編集フォームが使える。権限不足は 403
  - 新規作成すると fs_dir_path が `licenses/<v>/<p>/<slug>` 形式で保存される
    (日本語 slug が保持され、禁止文字が `_` になる)
  - デフォルト一覧に満了ライセンスが出ない。`?expired=1` で出る
  - 期限 90 日以内の行が視覚的に区別できる
  - product_keys はどの画面にも平文表示されない。編集フォーム空欄提出で
    既存キーが保持される
  - 廃止部署 (valid_to NOT NULL) は新規作成の選択肢に出ない
- Slugify: `Adobe Acrobat Pro` → `Adobe_Acrobat_Pro`、`A/B:C` → `A_B_C`、
  `契約 2024` → `契約_2024`、空文字 → `_`

## 想定リスク

- **fs_dir_path と実 FS の乖離期間**: L-1〜L-3 の間、DB にパスはあるが
  物理ディレクトリが無い。check-integrity (未実装) が警告する状態を
  経由するが、仕様どおり警告のみでブロックしないため運用は継続できる
- **部署スコープ認可の欠如**: 上記決定のとおり明示的な負債。AD 連携で
  一般ロール利用者が入る前に認可強化 PR を入れる
