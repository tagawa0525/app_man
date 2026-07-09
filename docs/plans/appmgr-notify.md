# appmgr-notify 本実装 (第 5 グループ — 通知)

## Context

仕様 §5.9。通知イベント 6 種のうち、現時点でデータ源が存在するのは
「ライセンス満了 N 日前 (日次バッチ)」のみ。本 PR は Notifier 基盤 +
notifications テーブル運用 (送信前記録・再送・gave_up) + 満了通知を
実装する。SKYSEA / AD / 棚卸し起点の 5 イベントは各フェーズで
この基盤に載せる。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| チャネル | 仕様の 4 実装 (SMTP / TeamsWebhook / File / Multi) + off をすべて標準ライブラリで実装 (net/smtp、net/http、os) | 外部依存ゼロ。SMTP は AUTH なし平文 (社内リレー前提、仕様の設定例に認証項目が無い) |
| config | `notifier:` セクション (mode / smtp{host,port,from} / teams{webhook_url(_env)} / file{output_dir} / multi{channels} / expiry_days_before)。mode 既定 off (未設定でバッチが安全に no-op)、expiry_days_before 既定 [30, 90] | 仕様 §10 の設定例。webhook_url_env は既存の _env 機構がそのまま解決 |
| File 出力 | `<output_dir>/<YYYYMMDD-HHMMSS>-<notification_id>.txt` に宛先・件名・本文 | 開発・テスト用 (仕様)。id 付きで突合可能に |
| 満了通知 | 実行日に「満了まで残り N 日 (N ∈ expiry_days_before)」のライセンスを検出 (日付粒度、当日込みセマンティクス)。kind は `license_expiry_<N>` | 重複抑止キー (kind, entity) に N を含め、90 日前通知の後に 30 日前通知が別途出るようにする |
| 宛先解決 | 所管部署に紐づく license_manager ロール保持の有効 app_users → notify_email → linked_user の email → 両方空は warn + skip (仕様どおり)。宛先 1 人 1 レコード | §5.9「宛先解決の優先順位」。部署に license_manager がいなければ system_admin 全員にフォールバック (仕様の「設定された宛先」は未実装のため、握りつぶさない最小策として。Plan 判断) |
| 送信フロー | 通知対象ごとに: 重複チェック ((kind, related_entity_type, related_entity_id) に sent があればスキップ) → notifications へ pending 作成 → Send → 成功: status=sent + sent_at / 失敗: status=failed + last_error | 仕様「送信前に必ずレコード作成」 |
| --retry-failed | status='failed' かつ retry_count < notification_max_retry (app_settings、既定 5) を再送。成功: sent / 失敗: retry_count++ (上限到達で gave_up) | 仕様どおり |
| gave_up サマリ | 通常実行の末尾で、未通知の gave_up があれば system_admin 宛に日次サマリ 1 通 (kind=gave_up_summary、related は NULL、重複抑止は「当日分作成済みか」で判定) | 仕様「system_admin 向けの日次サマリに集約」の最小実装 |
| dry-run | 検出件数・宛先数・would_send をログのみ (notifications へも書かない) | clirun 共通。送信前記録より手前で止める |
| mode=off | 検出もスキップし info ログのみで exit 0 | 通知未設定の環境でスケジューラ登録だけ先行しても無害 |
| lock | ModeShared (skeleton どおり) | 通常の DB 読み書き |
| audit_logs | 記録しない | 通知履歴は notifications テーブル自体が持つ (二重管理を避ける) |

## 対象スコープ

- config: NotifierConfig 一式 + validate (mode 列挙、multi の channels 検証、smtp/teams/file の必須項目は「そのモードが選ばれたときだけ」検査)
- `internal/notify`: Notifier IF + 4 実装 + FromConfig(cfg) ファクトリ。
  単体テスト (File は実ファイル、Teams は httptest、SMTP は最小 fake
  server か接続失敗系、Multi は fake)
- repository: notifications の作成 / 送信結果更新 / 再送対象一覧 /
  重複チェック / gave_up 未サマリ集計、満了 N 日ちょうどのライセンス
  一覧 (宛先解決 JOIN 込み)
- cmd/notify: runner (通常 / --retry-failed / dry-run) + main 配線
- config.example.yml / README 追記

### 範囲外 (別 PR)

- SKYSEA / AD / 棚卸し起点の 5 イベント (各フェーズでこの基盤に載せる)
- 通知テンプレートの装飾・HTML メール・SMTP AUTH / TLS (必要になったら)
- 「設定された宛先」(ライセンス単位の宛先指定) — スキーマに列が無く仕様も詳細未定義

## TDD コミット順序

1. `docs(plans): 本 Plan`
2. `feat(config): NotifierConfig を追加 (mode 検証 + example)`
3. `test(notify): File/Teams/Multi チャネルと off (RED)`
4. `feat(notify): Notifier 実装 (GREEN)`
5. `feat(repository): notifications と満了検出のクエリ (sqlc)`
6. `test(notify-cmd): 満了通知の検出/記録/重複抑止/再送/gave_up (RED)`
7. `feat(notify-cmd): runner 実装 (GREEN)`
8. `feat(cmd/notify): main 配線 + --retry-failed フラグ`
9. `docs: README と config.example.yml に notifier を追記`

## 受け入れ基準

- 全ゲート緑
- 実バイナリ (mode=file):
  - 満了 30 日前のライセンス → 実行で mail-out にファイル生成 +
    notifications に sent。再実行で重複送信されない
  - 宛先なし部署 → warn + skip (レコードも作らない)
  - file の output_dir を書込不能にして実行 → failed + last_error →
    --retry-failed で再送 (復旧後) → sent。上限到達で gave_up →
    次回通常実行で system_admin にサマリ
  - dry-run → notifications 無変化 + would_send ログ
  - mode=off → no-op exit 0
- Teams: httptest への POST ボディ (テキスト) を単体テストで固定

## 実装中の変更

- **満了検出クエリの差し替え** (`ListLicensesExpiringInDays` →
  `ListLicensesExpiringOn`): 当初の
  `julianday(date(expires_at)) - julianday(date('now')) = N` は実データで
  常に 0 件になる欠陥があった。modernc.org/sqlite ドライバは time.Time を
  Go の `time.Time.String()` 形式 (`2026-08-08 00:00:00 +0000 UTC`) で保存
  し、SQLite の `date()` がこの形式を解釈できず NULL を返すため
  (cmd/notify の runner テスト作成時に発見)。対象日付 (`YYYY-MM-DD`) を
  Go 側で now から計算してクエリに渡し、
  `substr(CAST(expires_at AS TEXT), 1, 10)` との文字列比較で判定する方式に
  変更。`date('now')` への依存が消え、now 注入で検出そのものを決定論的に
  テストできる副次効果もある

## 想定リスク

- **SMTP の実環境検証不能**: fake server での送信成功系 + 接続失敗系の
  テストに留め、実サーバ検証は本番導入時の受け入れに委ねる (README に
  file モードでの事前確認手順を記載)
- **満了検出の「ちょうど N 日」**: 日次実行が 1 日飛ぶと取りこぼす。
  MVP は仕様どおり N 日ちょうどで実装し、取りこぼし対策 (N 日以内で
  未通知) は運用で問題になってから
