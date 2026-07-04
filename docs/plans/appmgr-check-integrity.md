# appmgr-check-integrity 本実装 (運用基盤の残り 第 2 PR)

## Context

generate-meta (PR #27) まで完了し、FS 側の実体と `internal/licensefs` が
存在する。本 PR は仕様 §5.12 の整合性チェック CLI を実装する。

仕様 §5.12 の 5 パターンのうち、**対話的な対応 (「登録 or 無視を選択」
「新ハッシュ承認 or 復元」) は `/admin/integrity` 画面 (system_admin、
別 PR) の責務**とし、本 PR は CLI による検出・警告ログ・meta.yml の
自動生成のみを実装する。検出ロジックは `internal/integrity` に置き、
画面 PR が同じ Scan を再利用できる形にする。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| 検出ロジックの配置 | `internal/integrity.Scan(ctx, q, basePath) (Report, error)` を新設。CLI はログ出力の器 | `/admin/integrity` 画面 (別 PR) が同じ Scan を使う予定。先に共有形にしておく |
| 検査パターン | (1) stored_path が FS に無い (2) FS の licenses/ 配下にあるが DB に無いファイル (meta.yml は除外) (3) sha256 不一致 (4) meta.yml 欠落 (5) ディレクトリ不一致 = fs_dir_path のディレクトリが無い / どのライセンスにも属さない孤児ディレクトリ | 仕様 §5.12 の表をそのまま |
| meta.yml 欠落の扱い | 唯一の自動修復。`licensefs.Regenerate` で生成し、結果は "auto-generated" として報告 | 仕様「自動生成」。他パターンは警告のみ |
| exit code | 検出があっても **exit 0** (警告はブロックしない思想)。exit 1 は動作エラー (DB 不能・base_path 未設定・walk 失敗) のみ | 仕様「ブロックしない (FS が正本の思想)」。所見は正常な出力 |
| ログ | 所見 1 件ごとに warn (kind / license_id / path / detail)、最後に info でサマリ (件数を kind 別に) | §8.5。/admin/integrity 実装までの運用者インターフェイス |
| dry-run | meta.yml 自動生成を行わず would_generate_meta として報告。検査自体は読取専用なのでそのまま実行 | 破壊的操作は meta 生成のみ |
| sha256 検査 | license_documents 全行についてファイルを読み再計算 | 台数規模 (数百ファイル・20MiB 上限) なら日次で問題ない。遅くなったらサイズ・mtime の事前フィルタを検討 (今はしない) |
| 孤児ディレクトリの判定 | licenses/ 直下 3 階層 (vendor/product/license) を走査し、末端ディレクトリが全ライセンスの fs_dir_path 集合に無ければ孤児。途中階層は末端が全て孤児のときのみ報告しない (ノイズ削減のため末端のみ報告) | §3.2 の構造前提。深さ不定の走査より誤検知が少ない |
| 未登録ファイルの判定 | 各ライセンスの契約フォルダ内で、meta.yml と license_documents.stored_path 群に無いファイル | 「人が直接置く」運用 (仕様 §3.2) があるため正常系でも起こる。警告でなく info 寄りだが、仕様は「表示」対象なので所見として数える |
| 汚染 fs_dir_path | licensefs.DirAbs のガードに任せ、該当行は所見 (kind=invalid_path) として報告し続行 | generate-meta と同じ隔離方針 |
| 起動経路 | `clirun.Run(binaryName, lockfile.ModeShared, handler)` 維持。runCheckIntegrity(ctx, deps) → scan は下位関数分離 | skeleton どおり。時刻依存が無いため now 注入は不要 |
| README | 運用 1 段落 | 明記 |

## 対象スコープ

### 範囲内

- `internal/integrity/integrity.go` + テスト: `Finding{Kind, LicenseID, Path, Detail}` /
  `Report{Findings, MetaGenerated, WouldGenerateMeta}` / `Scan(ctx, q, basePath, dryRun, now)`
- `cmd/check-integrity/main.go` + `runner.go` + `runner_test.go`
- `README.md`: 運用 1 段落

### 範囲外 (別 PR)

- `/admin/integrity` 画面と対話的対応 (登録 / 無視 / 承認 / 復元)
- 所見の永続化 (テーブル追加)。CLI はログ、画面 PR はオンデマンド Scan
  で開始し、無視状態の記憶が必要になった時点でテーブルを検討
- 通知連携 (フェーズ 11)

## 内部設計

### Scan フロー

```text
1. rows := ListLicenses(ctx, 1)  // 満了含む全件
2. 各ライセンス:
   a. DirAbs 失敗 → finding(invalid_path)、この行の以降をスキップ
   b. ディレクトリ無し → finding(dir_missing)。meta / 文書検査はスキップ
   c. meta.yml 無し → dry-run: would_generate++ / 実行: Regenerate して
      MetaGenerated++ (失敗は finding(meta_generate_failed))
   d. ListLicenseDocumentsByLicense: stored_path 不在 → finding(file_missing)、
      存在すれば sha256 再計算 → 不一致 finding(sha256_mismatch)
   e. フォルダ内の実ファイル走査: meta.yml でも stored_path 群でもない
      → finding(unregistered_file)
3. licenses/ ツリー走査: 末端 (深さ 3) ディレクトリが fs_dir_path 集合に
   無ければ finding(orphan_dir)
4. Report を返す。CLI が warn ログ + サマリ info
```

### stored_path の対応付け

license_documents.stored_path は fs_dir_path からの相対ではなく
base からの相対 (L-3 実装を確認して追随する)。突合はパス文字列の
正規化 (filepath.Clean / ToSlash) を通して行う。

## TDD コミット順序

1. `docs(plans): appmgr-check-integrity 本実装の Plan ファイル`
2. `test(integrity): 5 パターンの検出と meta 自動生成 (RED)`
3. `feat(integrity): Scan 実装 (GREEN)`
4. `feat(cmd/check-integrity): main をスケルトンから配線に置換`
5. `docs: README に check-integrity の運用を追記`

## 受け入れ基準

- 全ゲート緑
- 実バイナリで (verify-doc 環境ベース):
  - 整合した状態 → 所見 0 件、exit 0
  - 証書ファイルを削除 → file_missing 警告 / 中身を書き換え →
    sha256_mismatch 警告 / 未登録ファイルを置く → unregistered_file /
    孤児ディレクトリを作る → orphan_dir / fs_dir_path のディレクトリを
    消す → dir_missing。いずれも exit 0
  - meta.yml を消して実行 → 自動生成される。dry-run では生成されず
    would_generate_meta として報告
  - base_path 未設定 → exit 1
- Scan がファイルの内容を変更しない (meta.yml 生成を除く)

## 想定リスク

- **大規模 FS の走査時間**: 全 sha256 再計算が支配的。想定規模では
  日次に収まる。閾値超過時の増分化は必要になってから
- **画面 PR との重複回避**: Scan の戻り値 (Report) を画面がそのまま
  表示できる構造にしておく。所見の文言は view 層で日本語化し、
  integrity パッケージは Kind 定数 + 生データに留める
