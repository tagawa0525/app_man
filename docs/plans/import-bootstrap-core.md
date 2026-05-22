# appmgr-import-bootstrap コア機能 + 開発 seed 同梱

## Context

要件書 §9 で MVP 必須と規定された `appmgr-import-bootstrap` (`cmd/import-bootstrap/main.go`) は現在 21 行の placeholder で、ログを 1 行出して終了するだけ。フェーズ 2 でマスタ系 6 テーブルの CRUD 画面 (vendors / products / aliases / departments / users / devices) が揃ったため、画面ベースの動作確認を行いたいが、開発のたびに画面から 1 件ずつ手で入れるのは現実的でない。

本 PR では §9 の **コア機能のみ** (検証 / dry-run / --commit / 1 トランザクション) を実装し、同時に開発用 seed CSV を `data/seeds/` に同梱する。`make dev-seed` で migrate → 6 kind の CSV 投入が一発で済む状態を作る。これで:

- 開発者は seed を流すだけで「画面に何かが映る」状態に到達できる
- 本番初日の Excel 取込みは同じ仕組み (`--commit`) で実施できる (CSV テンプレートは `docs/templates/` ではなく `data/seeds/` を雛形にする運用)

スコープ外 (別 PR):

- `audit_logs` への記録 — テーブルは DDL にあるが Go 側で書き込む初例になる。記録形式 (JSON key / action 名) の規約決めと共に別 PR
- `--kind alias-resolve` — 名寄せキューの一括解決。`raw_installations` テーブルと UI の整備が前提
- `--kind licenses` / `--kind assignments` — `licenses` / `user_assignments` / `device_assignments` テーブルは DDL のみで CRUD 画面が未着手。マスタ整備と同 PR で扱う
- Shift_JIS 入力 — 本 PR は UTF-8 のみ。SKYSEA 取込み PR で対応

## 主要決定

| 項目                                    | 決定                                                                                                                                                       |
| --------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 対象 kind                               | vendors / products / product_aliases / departments / users / devices の **6 種**                                                                              |
| 入力形式                                | **UTF-8 CSV、ヘッダ行あり**。`encoding/csv` の標準パーサを使う                                                                                              |
| dry-run / commit                        | **デフォルト dry-run**、`--commit` で実投入 (要件書 §9 規定)                                                                                                  |
| トランザクション                        | `--commit` 時は **1 トランザクション**で全行投入、検証エラーまたは INSERT エラーで全件ロールバック                                                            |
| 検証フェーズ                            | INSERT に進む前に **全行を 1 周検証**し、エラーがあれば行番号 + 列名 + 内容を標準出力に列挙して exit 1。dry-run / commit のどちらでも検証は必ず実行          |
| FK 参照の解決                           | CSV 内のキー (vendor name / product canonical_name + edition / department code / user employee_code) を **DB 既存レコード + 同 CSV 内で先行登録された行** から引く。同一バッチ内での前方参照は不可 |
| 重複キーの扱い                          | DB 既存と CSV 内の重複は **検証エラー** (要件書 §9「重複キーを行番号付きで列挙」)。同一バッチ内で既に登録済みの行と同じキーが後ろに出てきたらエラー         |
| `clirun` の拡張                         | `RunWithFlags(binaryName, mode, register, handler)` を追加。`register` は `*flag.FlagSet` を受けて追加 flag を登録するコールバック                            |
| 既存バッチへの影響                      | `clirun.Run` は維持 (内部で RunWithFlags を呼ぶ薄いラッパに変える)。他 7 バッチの main は無変更                                                              |
| 標準出力 / ログ                         | 検証結果と最終投入件数は **標準出力**、構造化ログは既存通り `logs/appmgr-import-bootstrap.log` に JSON                                                       |
| make ターゲット                         | `make dev-seed` 追加: `migrate-up` → 6 kind を順に `--commit` で投入                                                                                          |
| seed データ                             | 最小セット (10〜30 件/テーブル) を **CSV ファイル** (`data/seeds/*.csv`) として同梱、ハードコードは作らない                                                  |
| seed の git 管理                        | CSV は **git 管理する** (10〜30 件なので diff が読める)。`data/seeds/.gitkeep` ではなく実体ファイル                                                            |
| 生成された seed.db                      | `data/app.db` 自体は git 管理しない (既存通り)。`make dev-seed` で毎回生成し直す                                                                              |
| FK 参照を CSV でどう書くか              | **業務キー** (vendor name 等) で参照する。bootstrap.csv 内に内部 ID を書かせない (人間が読み書きしやすい形)                                                  |
| 検証エラー時の exit code                | 1 (= `clirun.exitHandlerError` 相当、handler エラーと同じ扱い)                                                                                              |
| dry-run 時の出力                        | 「N 行検証 OK、commit しません」を最後に 1 行                                                                                                                |
| commit 時の出力                         | 「N 行投入」を最後に 1 行                                                                                                                                    |

## 対象スコープ

### 範囲内

- `internal/bootstrap/` パッケージ新設 (CSV reader、Importer インタフェース、各 kind の Importer 実装)
- `cmd/import-bootstrap/main.go` を本実装に置換 (`--kind` / `--file` / `--commit` フラグ)
- `internal/clirun.RunWithFlags` 追加
- `data/seeds/*.csv` 6 ファイル (10〜30 件)
- `Makefile` に `dev-seed` ターゲット
- 各 Importer の単体テスト + dispatch の統合テスト

### 範囲外

- audit_logs 書き込み
- alias-resolve
- licenses / assignments の kind
- Shift_JIS 入力
- ヘッダ行なしの CSV / 区切り文字選択
- 並列投入 (1 件ずつ順次で十分)
- ロールアップ統計 (kind 別の件数しか出さない)

## CSV フォーマット

各 kind の CSV ヘッダ列と必須/任意の対応表。FK は **業務キーで参照** する。

### `vendors.csv`

```csv
name,url,note
Microsoft,https://www.microsoft.com/,
Adobe,https://www.adobe.com/,
```

- `name` 必須・UNIQUE。`url` / `note` 任意

### `products.csv`

```csv
vendor_name,canonical_name,edition,software_type,license_required,default_approval_status,note
Microsoft,Microsoft Office,Standard,installed,true,approved,
Adobe,Photoshop,,installed,true,approved,
```

- `vendor_name` で `vendors.name` を引いて FK 解決
- `(vendor_id, canonical_name, edition)` で UNIQUE
- `software_type` / `default_approval_status` は省略時 DDL デフォルト (`installed` / `unknown`)
- `license_required` は `true` / `false` / 空欄
- 同 CSV 内で先に登録された vendor の name は参照可能 (vendors.csv → products.csv の順で投入)

### `product_aliases.csv`

```csv
product_vendor_name,product_canonical_name,product_edition,alias_name
Microsoft,Microsoft Office,Standard,MS Office Standard 2021
Adobe,Photoshop,,Adobe Photoshop CC 2024
```

- `(product_vendor_name, product_canonical_name, product_edition)` で products を引く (edition は空欄も許容)
- `alias_name` は UNIQUE

### `departments.csv`

```csv
code,name,parent_code,valid_from,valid_to,source_ou
DEPT001,本社,,2020-04-01,,
DEPT010,営業部,DEPT001,2020-04-01,,
```

- `code` 必須・UNIQUE
- `parent_code` 任意。空欄 or **CSV 内で前に登録された code** を参照
- `valid_from` / `valid_to` は `YYYY-MM-DD` 形式 (空欄可)
- `successor_department_id` は seed 範囲外 (本 PR では未対応)
- `source` は `'manual'` 固定 (DDL デフォルト)

### `users.csv`

```csv
employee_code,username,name,email,department_code
E001,tagawa,田川太郎,tagawa@example.com,DEPT010
E002,,山田花子,,DEPT010
```

- `employee_code` 必須・UNIQUE
- `department_code` で departments を引く (空欄可)
- 退職者 (`deactivated_at`) は本 PR では対象外

### `devices.csv`

```csv
asset_code,hostname,primary_user_code,department_code
PC-001,tagawa-pc,E001,DEPT010
PC-002,,,DEPT010
```

- `asset_code` 必須・UNIQUE
- `primary_user_code` で users の `employee_code` を引く (空欄可)
- `department_code` で departments を引く (空欄可)

## 内部 bootstrap パッケージ設計

```go
// internal/bootstrap/bootstrap.go
package bootstrap

// Importer は kind ごとの実装を抽象化する。Validate と Insert の 2 段階
// で、Validate は読み取り専用、Insert は tx 上で副作用を起こす。
type Importer interface {
    // Kind はこの importer が扱う --kind 文字列 (例: "vendors")。
    Kind() string
    // Validate は CSV 1 ファイル分を解析し、行ごとの検証エラーを返す。
    // 空スライスならエラーなし。DB は既存値の参照のみ (SELECT) で読む。
    Validate(ctx context.Context, q *repository.Queries, rows [][]string) []ValidationError
    // Insert は Validate を通った rows をトランザクション上で投入する。
    // 件数を返す。Insert 中のエラーは即座に return し、呼び出し側で
    // tx.Rollback() される。
    Insert(ctx context.Context, q *repository.Queries, rows [][]string) (int, error)
}

type ValidationError struct {
    Line    int    // 1 始まり (ヘッダ行は除いて 1 行目から)
    Column  string // 列名 (例: "vendor_name")
    Message string
}

// Run は dispatch + dry-run/commit 制御を行う共通関数。
//   - csvPath を開き、ヘッダ行を確認、データ行を [][]string にする
//   - importer.Validate を呼ぶ
//   - エラーがあれば標準出力に列挙して exit 1 相当を返す
//   - エラーなし & dryRun=true なら「N 行検証 OK」と出力して return
//   - エラーなし & dryRun=false なら BeginTx → importer.Insert → tx.Commit
func Run(ctx context.Context, db *sql.DB, csvPath string, importer Importer, dryRun bool, out io.Writer) error
```

### 各 kind の Importer

- `internal/bootstrap/vendors.go`: `vendorsImporter` 構造体
- `internal/bootstrap/products.go`: vendor name → vendor_id 解決ロジックを内包
- `internal/bootstrap/product_aliases.go`: products 引き
- `internal/bootstrap/departments.go`: parent_code は CSV 内前方参照可能 (Insert 中に都度引く)
- `internal/bootstrap/users.go`: department_code 引き
- `internal/bootstrap/devices.go`: user / department の両方を引く

FK 解決のために `q.GetXxxByYyy` のクエリが必要になる可能性。`vendors` テーブルには `name` UNIQUE があるが `GetVendorByName` が無い場合は追加する。**db/queries/*.sql に必要なら 1〜2 本追加する。sqlc 再生成**。

## clirun の拡張

```go
// FlagRegistrar は flag.FlagSet に追加 flag を登録するコールバック。
// 戻り値は Handler 側で読む parsed values へのアクセサ。
type FlagRegistrar func(fs *flag.FlagSet) (read func() any)

// RunWithFlags は Run の拡張で、追加 flag を登録できる。
func RunWithFlags(binaryName string, mode lockfile.Mode, registrar FlagRegistrar, handler Handler) {
    ...
}

// 既存 Run は内部で RunWithFlags を nil registrar で呼ぶ薄いラッパに。
func Run(binaryName string, mode lockfile.Mode, handler Handler) {
    RunWithFlags(binaryName, mode, nil, handler)
}
```

ただし `any` 返しは型安全でない。代替案として `Deps` に `Extra any` フィールドを追加し、import-bootstrap 側で型アサートする形にする。または **シンプルに `cmd/import-bootstrap/main.go` だけ clirun を経由しない実装にして、必要なヘルパは関数として export する** という選択もある。

実装中に最終形を決めるが、Plan としては「clirun を最小拡張する方向」とだけ確定させ、詳細は実装で詰める (アサート不要な単純な形に収まる見込み)。

## ファイル構成

| パス                                                  | 概要                                                                      | 区分        |
| ----------------------------------------------------- | ------------------------------------------------------------------------- | ----------- |
| `docs/plans/import-bootstrap-core.md`                 | 本 Plan                                                                   | 新規        |
| `internal/bootstrap/bootstrap.go`                     | Importer インタフェース、ValidationError、Run dispatch                    | 新規        |
| `internal/bootstrap/bootstrap_test.go`                | Run dispatch のテスト (dry-run、commit、ロールバック等)                    | 新規        |
| `internal/bootstrap/vendors.go`                       | vendors Importer                                                          | 新規        |
| `internal/bootstrap/vendors_test.go`                  | RED → GREEN                                                               | 新規        |
| `internal/bootstrap/products.go`                      | products Importer (vendor_id 解決)                                        | 新規        |
| `internal/bootstrap/products_test.go`                 | RED → GREEN                                                               | 新規        |
| `internal/bootstrap/product_aliases.go`               | aliases Importer                                                          | 新規        |
| `internal/bootstrap/product_aliases_test.go`          |                                                                          | 新規        |
| `internal/bootstrap/departments.go`                   | departments Importer (parent_code 解決、CSV 内前方参照)                    | 新規        |
| `internal/bootstrap/departments_test.go`              |                                                                          | 新規        |
| `internal/bootstrap/users.go`                         | users Importer                                                            | 新規        |
| `internal/bootstrap/users_test.go`                    |                                                                          | 新規        |
| `internal/bootstrap/devices.go`                       | devices Importer                                                          | 新規        |
| `internal/bootstrap/devices_test.go`                  |                                                                          | 新規        |
| `cmd/import-bootstrap/main.go`                        | placeholder を本実装に置換 (flag dispatch)                                | 編集        |
| `internal/clirun/run.go`                              | RunWithFlags 追加 (本 PR で必要なら)                                       | 編集 (条件付き) |
| `db/queries/*.sql`                                    | FK 解決用の `GetVendorByName` 等を必要に応じて追加                          | 編集        |
| `internal/repository/*.sql.go`                        | sqlc 再生成                                                               | 編集 (生成) |
| `data/seeds/vendors.csv`                              | seed (10〜30 件)                                                          | 新規        |
| `data/seeds/products.csv`                             | seed                                                                      | 新規        |
| `data/seeds/product_aliases.csv`                      | seed                                                                      | 新規        |
| `data/seeds/departments.csv`                          | seed                                                                      | 新規        |
| `data/seeds/users.csv`                                | seed                                                                      | 新規        |
| `data/seeds/devices.csv`                              | seed                                                                      | 新規        |
| `Makefile`                                            | `dev-seed` ターゲット追加                                                  | 編集        |

## seed データ (規模感)

| テーブル          | 件数 | 内容の方向性                                                                                              |
| ----------------- | ---- | --------------------------------------------------------------------------------------------------------- |
| vendors           | 5    | Microsoft / Adobe / Autodesk / JetBrains / Slack                                                          |
| products          | 12   | Office Standard / Office Pro / Photoshop / Illustrator / AutoCAD / Inventor / GoLand / IntelliJ / Slack 等 |
| product_aliases   | 8    | 代表的な alias (`MS Office Standard 2021` → Office Standard 等)                                              |
| departments       | 6    | 本社 / 営業部 / 製造部 / 情シス / 経理 / 廃止済み 1 件                                                       |
| users             | 20   | 各部署 3〜4 名、退職者 0 名 (退職表示は別途手動 delete で確認)                                              |
| devices           | 20   | PC-001 〜 PC-020、primary_user / department は users と紐付け                                              |

実在の企業名と日本人名を使うが、データはすべて架空。

## コミット列 (TDD サイクル)

ブランチ: `feat/import-bootstrap`。CLAUDE.md「最初のコミットを Plan ファイル」規約に従う。

| #  | コミット件名                                                                                          |
| -- | ----------------------------------------------------------------------------------------------------- |
| 1  | `docs(plans): import-bootstrap コア機能 + 開発 seed 同梱の実装プラン`                                  |
| 2  | `feat(bootstrap): Importer インタフェース + ValidationError + Run dispatch`                            |
| 3  | `test(bootstrap): Run dispatch の dry-run / commit / 検証エラーで abort (RED)` (※2 と統合の可能性)      |
| 4  | `feat(db/queries): bootstrap 用 GetVendorByName 等の FK 解決クエリ`                                    |
| 5  | `test(bootstrap): vendors Importer (RED)`                                                              |
| 6  | `feat(bootstrap): vendors Importer (GREEN)`                                                            |
| 7  | `test+feat(bootstrap): products Importer + vendor_id 解決`                                             |
| 8  | `test+feat(bootstrap): product_aliases Importer + product_id 解決`                                     |
| 9  | `test+feat(bootstrap): departments Importer + parent_code 前方参照`                                    |
| 10 | `test+feat(bootstrap): users Importer + department_code 解決`                                          |
| 11 | `test+feat(bootstrap): devices Importer + primary_user / department 解決`                              |
| 12 | `feat(cmd/import-bootstrap): main を本実装に置換 (--kind / --file / --commit)`                          |
| 13 | `feat(data/seeds): 6 kind の seed CSV を同梱`                                                          |
| 14 | `feat(make): dev-seed ターゲットを追加`                                                                 |

コミット 3 は 2 に統合してもよい (依存が密)。各 Importer は RED→GREEN を 1 コミットにまとめる (細分化すると 14 コミットでは収まらないため、kind ごとに `test+feat` の混合コミットにする)。FK 解決クエリ (#4) は依存する Importer のコミット直前にまとめる方が読みやすいので、必要なら kind ごとに分散させる。

## 受け入れ基準

- [ ] `make generate` 後 `git status` クリーン
- [ ] `make build` / `make test` / `go test -race ./...` / `make lint` 全緑
- [ ] `make dev-seed` を実行すると `data/app.db` が初期化 → 6 kind が順次投入されエラーなく完了
- [ ] `make run` (DevMode=1) でサーバを起動し、`/vendors`・`/products`・`/departments`・`/users`・`/devices` に seed データが表示される
- [ ] `./bin/appmgr-import-bootstrap --kind vendors --file data/seeds/vendors.csv` (dry-run) が「N 行検証 OK、commit しません」と出力
- [ ] 同コマンドに `--commit` を付けると INSERT され「N 行投入」と出力
- [ ] 検証エラー (FK 参照先なし、UNIQUE 重複、必須欠落) は行番号 + 列名 + 内容で標準出力に列挙され exit 1
- [ ] 検証エラーがあると `--commit` 付きでも INSERT されない (件数 0)
- [ ] INSERT 中の DB エラーで全件ロールバック (テストで人為的に再現)
- [ ] PR 本文に「audit_logs / alias-resolve / licenses / assignments / Shift_JIS は別 PR」と明記

## 動作検証手順

```sh
nix develop
make generate
make build
make dev-seed                 # = migrate-up → 6 kind を順次 --commit
APP_MAN_DEV_MODE=1 ./bin/appmgr-server --config config.yml &
sleep 1

# 一覧に seed が出ること
curl -sS -H 'X-User-Role: viewer' http://localhost:8180/vendors    | grep -c "<tr"
curl -sS -H 'X-User-Role: viewer' http://localhost:8180/products   | grep -c "<tr"
curl -sS -H 'X-User-Role: viewer' http://localhost:8180/users      | grep -c "<tr"
curl -sS -H 'X-User-Role: viewer' http://localhost:8180/devices    | grep -c "<tr"

# dry-run は既存 DB を変更しない
./bin/appmgr-import-bootstrap --config config.yml --kind vendors --file data/seeds/vendors.csv
# → "5 行検証 OK、commit しません"

# 検証エラー再現 (重複)
cp data/seeds/vendors.csv /tmp/dup.csv
echo "Microsoft,,," >> /tmp/dup.csv
./bin/appmgr-import-bootstrap --config config.yml --kind vendors --file /tmp/dup.csv --commit
# → "line 6, column name: 'Microsoft' は既に登録されています" のような出力で exit 1
```

## 後続 PR の準備

- audit_logs PR: 本 PR の Run dispatch に `auditLogger AuditLogger` 引数を追加して書き込ませる構造にできるよう、`Run` の interface を「将来拡張可能」な形で残す
- alias-resolve PR: 別 kind として追加 (raw_installations PR と同時)
- licenses / assignments PR: 該当テーブルの CRUD 画面 (フェーズ 3) と同 PR で
