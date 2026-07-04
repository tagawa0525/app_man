# appmgr-generate-meta 本実装 (運用基盤の残り 第 1 PR)

## Context

フェーズ 6 (L-1〜L-3) 完了により meta.yml の生成基盤 (internal/metayml、
web 層の regenerateLicenseFS) が存在する。本 PR は仕様 §9「meta.yml
一括再生成 (必要時実行)」の CLI を実装し、L-1〜L-3 期間に発生しうる
「fs_dir_path はあるが物理ディレクトリ / meta.yml が無い」行の backfill
と、手動編集で壊れた meta.yml の復旧手段を提供する。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| 共有ロジックの抽出 | `internal/licensefs` を新設し、web の `regenerateLicenseFS` の本体を `licensefs.Regenerate(ctx, q, basePath, licenseID, now) error` として移す。web は薄いラッパで呼ぶ | 利用者が web の 3 トリガ + CLI で計 4 箇所になり、重複 3 回の抽象化基準を満たす。挙動は移動のみで変えない |
| 起動経路 | 既存 `clirun.Run(binaryName, lockfile.ModeShared, handler)` を維持 | skeleton どおり。backup (ModeGlobal) とは相互排他される |
| 対象 | 全ライセンス (満了含む)。`ListLicenses(ctx, 1)` を流用 | 仕様 §5.2「満了レコードも証書・meta.yml は保持」。満了フォルダも正本 |
| 失敗の扱い | 1 件の失敗で中断せず全件処理し、成功 / 失敗件数をログ。失敗が 1 件以上なら exit 1 | 一括再生成の目的 (できる限り復旧) に合致。exit 1 でスケジューラ / 運用者に異常を通知 |
| dry-run | 対象ライセンス件数と、meta.yml が現存しない (= 新規作成になる) 件数をログに出すのみ。FS には触れない | clirun 共通フラグ。「実行後の姿の予告」思想 (would_create を出す) |
| base_path 必須 | handler 冒頭で `file_store.base_path` 未設定なら error (exit 1) | appmgr-server / backup.output_dir と同じ消費者責務パターン |
| 時刻 | `runGenerateMeta(ctx, deps, now)` に注入。meta.yml の last_updated_by_app に使う | backup / prune-logs と同構成 |
| ログ | `info "generate-meta completed" total succeeded failed would ...`。ライセンスの中身 (キー等) は出さない | 運用可視性。§8.5 |
| README | バックアップ節の周辺に 1 段落 (必要時実行・dry-run) | 運用手順の明記 |

## 対象スコープ

### 範囲内

- `internal/licensefs/licensefs.go` + テスト: `Regenerate` (web からの移動)
  と `MetaExists(basePath, fsDirPath) (bool, error)` (dry-run の would_create 判定。basePath 脱出や ENOENT 以外の stat エラーは error を返し、呼び出し側が failed として予告する)
- `internal/handler/web/documents.go`: regenerateLicenseFS を licensefs
  呼び出しに置換 (挙動不変、既存テストが回帰網)
- `cmd/generate-meta/main.go` + `runner.go` + `runner_test.go`
- `README.md`: 運用 1 段落

### 範囲外 (別 PR)

- appmgr-check-integrity (FS↔DB 整合の検査・警告)
- 孤児ディレクトリ (DB に無いフォルダ) の検出・掃除 (check-integrity の
  警告対象。generate-meta は DB → FS の一方向)
- import-bootstrap の --kind licenses / assignments

## 内部設計

### runGenerateMeta フロー

```text
1. cfg.FileStore.BasePath == "" なら error
2. db.Open → defer close
3. rows := ListLicenses(ctx, 1)   // 満了含む全件
4. dry-run なら: 各行の MetaExists を見て would_create / total / failed を
   ログ (MetaExists が error の行 = 実行しても失敗する行として failed に
   数える) → failed > 0 なら error return
5. 各行: licensefs.Regenerate(...)。失敗は error ログ + failed++ で続行
6. ログ: total / succeeded / failed。failed > 0 なら error return (exit 1)
```

### licensefs.Regenerate

web の実装をそのまま移す (GetLicenseByID + GetProduct +
ListLicenseDocumentsByLicense → MkdirAll → metayml.Write)。
`now` を引数化して LastUpdatedByApp の決定論を CLI テストでも確保する
(web ラッパは time.Now() を渡す)。

## TDD コミット順序

1. `docs(plans): appmgr-generate-meta 本実装の Plan ファイル`
2. `refactor(licensefs): meta 再生成を web から internal/licensefs へ抽出`
   (挙動不変の移動。既存 web テストが緑のままであることが回帰網)
3. `test(generate-meta): 全ライセンスの meta.yml 一括再生成と dry-run (RED)`
4. `feat(generate-meta): runGenerateMeta 実装 (GREEN)`
5. `feat(cmd/generate-meta): main をスケルトンから配線に置換`
6. `docs: README に generate-meta の運用を追記`

## 受け入れ基準

- 全ゲート緑 (build / test / race / lint)
- 実バイナリで:
  - ディレクトリ / meta.yml が無いライセンス行に対して実行 → 物理
    ディレクトリと meta.yml が生成される (backfill)
  - meta.yml を手動で壊して実行 → 正しい内容に復元される
  - 証書ファイルは触られない (バイト列不変)
  - dry-run → FS 無変更、total / would_create がログに出る
  - base_path 未設定 config → exit 1
  - 1 件の失敗 (例: ディレクトリを 読取専用にして書込み不能) があっても
    他の行は処理され、exit 1 で終わる — 検証環境で再現可能なら確認、
    困難なら単体テストで代替
- web 側の既存テスト (documents 系) が変更なしで緑 (抽出の挙動不変)

## 想定リスク

- **抽出時の挙動差**: regenerateLicenseFS は handler のレシーバ依存が
  薄い (q / basePath / now のみ) ため移動は機械的。既存テストで担保
- **大量ライセンス時の実行時間**: 1 件ずつ直列。数百件想定では問題なし。
  必要時実行のバッチなので MVP では並列化しない
