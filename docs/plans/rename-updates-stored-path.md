# slug 変更時に license_documents.stored_path を追随させる (fix)

## Context

appmgr-check-integrity (PR #28) の実データ検証で発見した L-3 (PR #26) の
バグ修正。ライセンス更新で fs_dir_path が変わると `renameLicenseDir` が
物理ディレクトリを移動するが、その配下の証書を指す
`license_documents.stored_path` (base 相対) が旧パスのまま残る。

結果: 証書ダウンロードが 404 相当になり、check-integrity では
file_missing (DB が旧パスを指す) + unregistered_file (新パスの実体が
未登録扱い) の対として現れる。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| 修正位置 | licenses update ハンドラの fs_dir_path 変更フロー。rename 成功後、該当ライセンスの documents を列挙し、旧 fs_dir_path 接頭辞を新に付け替えて 1 行ずつ UPDATE | 影響範囲が最小で、rename と同じトリガ内で完結する |
| 付け替えの実装 | Go 側で `strings.HasPrefix(stored, oldDir+"/")` を確認して組み立て、新クエリ `UpdateLicenseDocumentStoredPath :execrows` (id, stored_path) で更新 | SQL の文字列演算 (substr/`\|\|`) より読みやすく、接頭辞不一致の行 (手動修正済み等) を壊さない |
| 失敗時 | stored_path 付け替えと licenses UPDATE は 1 トランザクション。失敗 (UNIQUE 衝突・commit 失敗・affected 0) 時は FS rename も best-effort で復元し、復元失敗のみ check-integrity の警告に委ねる | レビューで方針変更: 当初は「警告のみ思想の範囲内」としたが、UNIQUE 衝突 409 のような通常系の失敗で毎回残骸が出るのは警告思想の濫用。tx + rename 復元で部分更新を構造的に防ぐ |
| meta.yml | 既存の update フロー末尾の再生成がそのまま新 stored_path を反映する | 追加作業なし |

## TDD コミット順序

1. `docs(plans): 本 Plan`
2. `test(web): slug 変更後も証書の stored_path とダウンロードが追随 (RED)` —
   アップロード → slug 変更 → (a) DB の stored_path が新 fs_dir_path 接頭辞
   (b) ダウンロード 200 で同一バイト列 (c) integrity.Scan が所見 0
3. `fix(web): rename 時に stored_path を付け替える (GREEN)` — 新クエリ
   (sqlc, ASCII コメント) + ハンドラ修正

## 受け入れ基準

- 上記テスト + 既存全テスト緑。実サーバでアップロード → slug 変更 →
  ダウンロード成功と check-integrity 所見 0 を確認
- fs_dir_path が変わらない通常更新では documents に触れない
