# appmgr-prune-logs 本実装 (フェーズ 12 — 運用基盤 第 2 PR)

## Context

運用基盤第 1 PR (`appmgr-backup`, PR #22) がマージ済み。第 2 PR として
`appmgr-prune-logs` を placeholder スケルトンから本実装に置き換える。

現状 `cmd/prune-logs/main.go` は「not implemented」をログ出力するだけの
スケルトン。仕様書の要求：

- §5.11: `app_settings` の保持期間キーに従い、超過レコードを物理削除
- §9 受け入れ基準 17: 「`appmgr-prune-logs` を実行すると、`app_settings` の
  保持期間キーに従って `audit_logs` / `raw_installations` / `import_logs` /
  `notifications`（送信済み）の古いレコードが削除される。`--dry-run` で
  対象件数のみ確認できる」

対象テーブルと保持期間キー (§5.11、既定値は仕様書の値)：

| テーブル | キー | 既定値 (日) | 判定カラム |
|---------|------|------------|-----------|
| `audit_logs` | `retention_days_audit_logs` | 1825 | `occurred_at` |
| `raw_installations` | `retention_days_raw_installations` | 365 | `created_at` |
| `import_logs` | `retention_days_import_logs` | 1095 | `imported_at` |
| `notifications` (送信済みのみ) | `retention_days_notifications_sent` | 365 | `sent_at` |

4 テーブルとも既存スキーマ (migration 000003 / 000005) に存在する。
`app_settings` も存在するが、初期データの seed は無い (設定 UI は §5.11 の
後続フェーズ)。つまり本番でもキー不在が通常状態であり、**キー不在 =
既定値**のフォールバックが本体仕様になる。

## 事前検証 (2026-07-03 実施)

DATETIME カラムの日時比較は、書込み経路の混在 (SQLite の
`DEFAULT CURRENT_TIMESTAMP` とドライバの time.Time バインド) で
壊れうるため、modernc.org/sqlite で実証した：

| 比較方法 | 結果 |
|---------|------|
| A: `WHERE at < ?` に `time.Time` を直接バインド | **正しく動作** (CURRENT_TIMESTAMP 書込み行に対して期待件数) |
| B: UTC を `"2006-01-02 15:04:05"` 文字列でバインド | 正しく動作 |
| C: `WHERE at < datetime(?)` でラップ | **誤動作** (0 件になる)。採用禁止 |

既存コード (`db/queries/sessions.sql` の `DeleteExpiredSessions` →
`internal/session/store.go`) はパターン A なので、**A に統一**する。
cutoff は `now.UTC().AddDate(0, 0, -days)` で計算する (§8.6: 保存は UTC)。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| 起動経路 | 既存 `clirun.Run(binaryName, lockfile.ModeShared, handler)` を維持 | DELETE は通常の書込みなので §8.8 のグローバルロック対象外 (ModeGlobal は backup のみ) |
| 本体ロジック分離 | `cmd/prune-logs/runner.go` に `runPrune(ctx, deps, now) error` | backup と同構成。時刻注入でテスト決定論 |
| 保持期間の取得元 | `app_settings` テーブル (config.yml ではない) | 仕様書 §5.11 の明示。設定 UI (後続フェーズ) から運用中に変更される値のため DB 持ち |
| キー不在時 | コード内の既定値定数 (1825 / 365 / 1095 / 365) にフォールバック | seed が無く「不在が通常」。既定値は仕様書 §5.11 の表と一致させる |
| 値が不正 (非整数 / 0 以下 / NULL) | error で即失敗 (exit 1)。該当テーブルだけスキップせず全体を中断 | 保持期間の解釈ミスで大量削除する事故の防止。0 日 = 「全部消す」は仕様に無い解釈なので拒否 |
| 日時比較 | sqlc クエリに `time.Time` を直接バインド (パターン A) | 事前検証と既存 sessions の流儀。`datetime(?)` ラップは誤動作するため禁止 |
| notifications の対象 | `sent_at IS NOT NULL AND sent_at < ?` | 「送信済みのみ」。pending / failed は再送管理の対象なので消さない。判定は status 文字列でなく日時カラム (論理状態は日時カラムで表現する規約と同族) |
| import_logs の FK 保護 | `DELETE ... WHERE imported_at < ? AND NOT EXISTS (SELECT 1 FROM raw_installations WHERE import_log_id = import_logs.id)` | `raw_installations.import_log_id` が FK 参照 (foreign_keys=1)。既定値では raw (365 日) が先に消えるので通常は影響しないが、管理者が import_logs の保持を raw より短く設定したときに FK 違反で全体失敗せず、子が残る親はスキップする構造的解決 |
| 削除順序 | raw_installations → import_logs → audit_logs → notifications | FK の子から先に削除。後 2 者は独立 |
| トランザクション | 使わない (テーブルごとに独立した DELETE) | 日次で再実行される冪等な処理。途中失敗で一部だけ消えても次回実行で収束する。巨大 tx の保持時間を避ける |
| dry-run | テーブルごとの対象件数を SELECT COUNT でログに出すのみ。import_logs の COUNT だけは DELETE と同一条件ではなく「同一実行で消えない子 (= raw の保持期間内の子) が残る親のみ除外」する | 受け入れ基準 17「対象件数のみ確認できる」。実削除は raw → import_logs の順で子が先に消えるため、「現時点で子が残る親」を除外する COUNT では超過 raw だけを子に持つ超過親が過少報告される (実バイナリ検証で発見。下記「解消済みリスク」)。backup PR の dry-run と同じ「実行後の姿を予告する」思想に揃える |
| クエリ | sqlc (`db/queries/app_settings.sql` + `db/queries/prune.sql`)。生成物はコミット | ORM 禁止 / sqlc 直書きの規約。`make generate` 後にコミット |
| ログ | 成功: `info "prune completed"` に各テーブルの deleted_count と total。dry-run: `info "prune dry-run"` に would_delete。レコードの中身は出さない | 監査・運用可視性。§8.5 |
| config.yml | 変更なし | 保持期間は app_settings 持ちのため。backup PR と異なり config 拡張は無い |
| exit code | clirun 既定 (0 OK / 1 handler error / 2 lock 競合 / 3 config 不正) | 既存規約 |

## 対象スコープ

### 範囲内

- `db/queries/app_settings.sql`: `GetAppSetting :one` (key で 1 行取得)
- `db/queries/prune.sql`: 4 テーブル × (Count :one / Delete :execrows) の 8 クエリ
- `internal/repository/*.sql.go`: `make generate` の生成物 (コミット対象)
- `cmd/prune-logs/runner.go`: `runPrune(ctx, deps, now) error` +
  保持期間解決ヘルパ (`resolveRetentionDays(ctx, q, key, defaultDays) (int, error)`)
- `cmd/prune-logs/runner_test.go`: 後述の受け入れ基準を網羅
- `cmd/prune-logs/main.go`: スケルトンを `runPrune` 配線に置換
- `README.md`: prune-logs の運用 1 段落 (バックアップ節の周辺に追記。
  保持期間キーと既定値、dry-run での事前確認)

### 範囲外 (別 PR)

- `app_settings` 編集画面 (§5.11 の UI。system_admin のみ)
- `notification_max_retry` 等、保持期間以外の app_settings キーの利用
- prune 実行自体の audit_logs 記録 (仕様に要求なし。バッチの実行記録は
  アプリログで足りる)
- 削除後の `VACUUM` / ファイルサイズ回収 (SQLite は自動再利用する。
  必要なら appmgr-backup の VACUUM INTO が実質的な最適化を兼ねる)
- sessions の期限切れ削除 (SessionMiddleware / store 側の既存責務)

## 内部設計

### クエリ (db/queries/prune.sql)

4 テーブルとも同型。import_logs のみ FK 保護付き：

```sql
-- name: CountPrunableAuditLogs :one
SELECT count(*) FROM audit_logs WHERE occurred_at < ?;

-- name: PruneAuditLogs :execrows
DELETE FROM audit_logs WHERE occurred_at < ?;

-- CountPrunableImportLogs は DELETE と同一条件ではない: 実削除は raw →
-- import_logs の順で同一実行内に子が先に消えるため、「この実行では消えない子
-- (= raw の cutoff 以降の子) が残る親のみ除外」して実行後の姿を予告する。
-- name: CountPrunableImportLogs :one
SELECT count(*) FROM import_logs
WHERE imported_at < sqlc.arg(imported_at)
  AND NOT EXISTS (
    SELECT 1 FROM raw_installations r
    WHERE r.import_log_id = import_logs.id
      AND r.created_at >= sqlc.arg(created_at)
  );

-- name: PruneImportLogs :execrows
DELETE FROM import_logs
WHERE imported_at < ?
  AND NOT EXISTS (SELECT 1 FROM raw_installations r WHERE r.import_log_id = import_logs.id);

-- name: CountPrunableSentNotifications :one
SELECT count(*) FROM notifications WHERE sent_at IS NOT NULL AND sent_at < ?;

-- name: PruneSentNotifications :execrows
DELETE FROM notifications WHERE sent_at IS NOT NULL AND sent_at < ?;

-- (raw_installations は audit_logs と同型: created_at < ?)
```

### runPrune フロー

```text
1. db.Open(deps.Cfg.Database) → defer close
2. 4 キーそれぞれ resolveRetentionDays(ctx, q, key, default):
     GetAppSetting → ErrNoRows なら default、
     値ありなら strconv.Atoi → 失敗 or <= 0 なら error (全体中断)
3. cutoff[table] = now.UTC().AddDate(0, 0, -days)
4. dry-run なら: 4 テーブルの Count を実行し would_delete をログ → return
5. 実削除 (順序: raw_installations → import_logs → audit_logs → notifications)
   各 :execrows の戻り値を記録
6. ログ: deleted_audit_logs / deleted_raw_installations / deleted_import_logs /
   deleted_notifications / total
```

### 保持期間キーの定数

```go
// 仕様書 §5.11 の既定値。app_settings にキーが無い場合に使う。
const (
    keyRetentionAuditLogs         = "retention_days_audit_logs"         // 1825
    keyRetentionRawInstallations  = "retention_days_raw_installations"  // 365
    keyRetentionImportLogs        = "retention_days_import_logs"        // 1095
    keyRetentionNotificationsSent = "retention_days_notifications_sent" // 365
)
```

### テスト方針

- `handlertest.NewTestDB` (in-memory + migrate 適用済み) を使う
  (cmd/create-app-user/runner_test.go と同流儀)
- 古い行の投入は raw SQL の INSERT で明示日時
  (`datetime('now', '-400 days')` 等) を書き込む
- deps は `clirun.Deps{Cfg: ..., Logger: slog.New(slog.DiscardHandler)}` を
  直接組み立てる。ただし runPrune は DB を自分で開くため、テストは
  `handlertest.NewTestDB` の DB を使う下位関数
  `pruneAll(ctx, q, logger, now, dryRun) error` を直接呼ぶ形に分離する
  (runPrune = db.Open + pruneAll の薄い合成。backup の runBackup が
  ファイルパス前提だったのと違い、in-memory DB を注入できる継ぎ目が要る)

## TDD コミット順序

1. `docs(plans): appmgr-prune-logs 本実装の Plan ファイル`
2. `feat(repository): app_settings 取得と prune 系クエリを追加 (sqlc)`
3. `test(prune-logs): 既定保持期間で 4 テーブルの超過行を削除 (RED)`
4. `feat(prune-logs): pruneAll で既定値ベースの物理削除を実装 (GREEN)`
5. `test(prune-logs): app_settings 上書き / 不正値エラー / FK 保護 / dry-run (RED)`
6. `feat(prune-logs): 設定解決・dry-run・import_logs FK 保護 (GREEN)`
7. `feat(cmd/prune-logs): main をスケルトンから runPrune 配線に置換`
8. `docs: README に prune-logs の運用を追記`

GREEN コミットごとに `make test` / `make lint` 緑を確認する
(RED コミットはテスト失敗のままで良い)。

## 受け入れ基準

- `make build` / `make test` / `go test -race ./cmd/prune-logs/` / `make lint` 全緑
- `pruneAll`:
  - 既定値 (キー不在): 各テーブルで保持期間超過行だけが消え、期間内の行は残る
  - `notifications`: `sent_at IS NULL` (pending / failed) は古くても消えない
  - `app_settings` にキーがあればその値が優先される
  - 値が非整数・0・負値・NULL → error (どのテーブルも削除されない)
  - `import_logs`: `raw_installations` の子が残っている行は削除されない
    (FK 違反にならず、他の行の削除は成功する)
  - dry-run: 件数ログのみで 1 行も削除されない
- 実バイナリ: 受け入れ基準 17 のとおり `--dry-run` で対象件数のみ確認できる
- 構造化ログにレコードの中身 (diff_json / body 等) が出ない (件数のみ)
- 多重起動 (lock 競合) → exit 2 (clirun 既存挙動、追加テスト不要)

## 想定リスク

- **大量削除の所要時間**: 初回運用時に数十万行の raw_installations を
  消す可能性。SQLite の DELETE は速く、ModeShared なので他バッチも
  ブロックしない。日次実行が定常化すれば増分は小さい。MVP では
  分割削除 (LIMIT 付きループ) は入れない — 必要になってから
- **audit_logs への削除記録の連鎖**: prune の実行を audit_logs に書くと
  「削除の記録がまた削除対象になる」再帰があるが、範囲外の決定
  (アプリログで足りる) により発生しない
- **時計ずれ**: cutoff はアプリサーバの時計基準。DB の CURRENT_TIMESTAMP
  も同一ホストなので実運用上の乖離は無視できる

## 解消済みリスク (記録)

- ~~dry-run の COUNT は 4 テーブルとも DELETE と同一条件で正確~~ → 誤り。
  実削除は raw_installations → import_logs の順で同一実行内に子が先に
  消えるため、「現時点で子が残っている親」を除外する COUNT は
  `would_delete_import_logs` を過少報告する (保持期間超過の import_logs の
  子が超過 raw のみのとき、dry-run は 0 件と報告するが実削除では 1 件消える。
  溜まったデータを最初に prune する初回運用時に典型的に起きる)。
  2026-07-03 の実バイナリ end-to-end 検証で発見。`CountPrunableImportLogs` に
  raw の cutoff を第 2 引数として追加し、「同一実行で削除されない子 (= raw の
  保持期間内の子) が残る親のみ除外」に変更して解消。`PruneImportLogs`
  (実削除) は削除順序が正しさを保証するため変更しない
