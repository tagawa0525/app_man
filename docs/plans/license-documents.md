# 証書ファイルと meta.yml (フェーズ 6 第 3 PR — L-3)

## Context

L-1 (CRUD, PR #24)・L-2 (割当, PR #25) がマージ済み。フェーズ 6 の最終 PR
として、ライセンス証書ファイルの物理配置・アップロード / ダウンロード・
meta.yml 自動生成・ライセンスキー閲覧の audit_logs 記録を実装する。
仕様 §3.2 (FS レイアウト) / §5.2 / §8.3 (アップロード検証・配信) / §8.6
(meta.yml は JST)。

これで「FS が正本、DB は検索インデックス」の FS 側が初めて実体を持つ。
L-1 で確定した `licenses.fs_dir_path` (相対パス) が物理ディレクトリになる。

## 主要決定

| 項目 | 決定 | 判断 |
|------|------|------|
| config | `FileStoreConfig{ BasePath string; UploadMaxBytes int64; AllowedMimeTypes []string }` (`file_store:`)。validate は UploadMaxBytes >= 0 のみ。**BasePath 必須チェックは appmgr-server 起動時** (backup.output_dir と同じ配置。バッチ系は file_store 不要のため validate で全バイナリを落とさない) | 既存の「バイナリ固有の前提条件は消費者側で検査」パターン |
| config 既定値 | UploadMaxBytes 未指定 (0) → 20971520 (20 MiB)、AllowedMimeTypes 未指定 → [application/pdf, image/png, image/jpeg] | 仕様 §10 のサンプル値。ゼロ値で動く安全な既定 |
| 物理ディレクトリ作成 | ライセンス新規作成時に `MkdirAll(base/fs_dir_path)` + meta.yml 生成。L-3 以前に作られた行はディレクトリ無しのままでよい (次回編集 or 証書アップロード時に MkdirAll、全量 backfill は appmgr-generate-meta = 次 PR) | 起動時一括処理を持たない。オンデマンド + バッチ再生成の 2 経路 |
| fs_dir_path 衝突 | 作成・変更時に「他ライセンスが同じ fs_dir_path を持つ (DB)」または「物理ディレクトリが既存かつ空でない」なら `_2`, `_3`... サフィックス (仕様 §3.2) | L-1 から先送りした衝突解決。DB と FS の両方を見る |
| スラッグ変更時のディレクトリ | 編集で fs_dir_path が変わる場合、旧ディレクトリが存在すれば `os.Rename` で移動 (移動先衝突はサフィックス)。旧が無ければ新パスで MkdirAll | FS 正本の追随。失敗時は 500 (DB 更新前に実施し、途中失敗で DB と FS がズレない順序にする) |
| アップロード検証 | (1) `http.MaxBytesReader` でサイズ上限 (2) 拡張子 (.pdf/.png/.jpg/.jpeg) (3) **マジックバイト実判定** (%PDF- / PNG シグネチャ / FF D8 FF) (4) 判定 MIME が AllowedMimeTypes に含まれる。Content-Type ヘッダは信用しない | 仕様 §8.3 の 4 点をそのまま |
| 保存ファイル名 | `filepath.Base(元名)` を slug.Slugify で正規化 (拡張子は保持)。同名衝突は `_2` サフィックス | パス区切り・禁止文字の混入防止。元名は license_documents.original_filename に保持 |
| sha256 | 保存時に計算し license_documents.sha256 に記録。meta.yml の documents にも出す | 仕様の meta.yml 形式 |
| doc_type | select: certificate / order / other | スキーマ NOT NULL。仕様は列挙を定めないため最小の 3 値 (証書 / 注文書 / その他) |
| ダウンロード | GET、認可後 `http.ServeContent` 相当のストリーミング。Content-Disposition は original_filename (RFC 5987 エンコード) | 仕様 §8.3「認可チェック後にストリーミング配信」 |
| 証書の削除 UI | 作らない | 仕様は アップロード / ダウンロードのみ要求。FS 正本の削除は運用 (手動) + check-integrity の警告で扱う |
| meta.yml | 新規パッケージ `internal/metayml`。仕様 §5.2 の形式で tmp + rename 書込み。日時は JST ISO8601 (§8.6)。生成トリガ = ライセンス作成 / 更新 / 証書アップロード | 自動生成ヘッダコメント含め仕様の例に忠実に |
| meta.yml 書込み失敗 | 証書ファイル保存・DB 登録は成立させ、meta 失敗は error ログ + flash 警告のみ (ブロックしない) | FS/DB 整合は「警告のみでブロックしない」思想 (CLAUDE.md)。appmgr-generate-meta で回復可能 |
| キー閲覧 | ライセンス詳細に「キーを表示」ボタン (POST /licenses/{id}/keys/reveal、license_manager 以上)。POST 応答として詳細画面にキーを 1 回だけ埋めて描画 (redirect しない)。表示前に audit_logs へ INSERT | 仕様 §5.2「閲覧時は audit_logs 記録」。GET で見えると記録を回避できるため POST + CSRF 必須。Cache-Control: no-store を付与 |
| audit_logs | 初の書込み経路。`CreateAuditLog` クエリ + web 層ヘルパ `recordAudit(ctx, q, r, action, entityType, entityID)` (app_user_id はセッションから)。今回の action は `license_keys.view` のみ | 監査記録の網羅 (仕様フェーズ 15) は別 PR。ここでは器 + 最初の 1 経路 |
| uploaded_by_app_user_id | セッションの AppUserID を記録 | 監査情報 |
| 認可 | 証書一覧・ダウンロード = viewer 以上 (L-1 の詳細画面と同基準)、アップロード・キー閲覧 = license_manager 以上 | §6.1。詳細画面の閲覧基準は L-1 の判断 (viewer = 詳細データの閲覧、§7.1) を踏襲 |
| Windows パス | FS 操作時は `filepath.FromSlash(fs_dir_path)` で変換して base と Join | fs_dir_path は `/` 区切りで保存済み (L-1) |
| クエリコメント | ASCII 英文限定 | sqlc v1.31.1 バグ |

## 対象スコープ

### 範囲内

- config: `FileStoreConfig` + 既定値解決 + validate。config.example.yml に
  `file_store:` 追記。cmd/server: BasePath 必須チェック + handler への配線
- `internal/filestore`: 保存 (検証 + sha256 + 衝突回避)・オープン・
  マジックバイト判定。単体テスト
- `internal/metayml`: meta.yml の構造体と Write (tmp + rename)。単体テスト
- repository: `ListLicenseDocumentsByLicense` / `CreateLicenseDocument` /
  `GetLicenseDocumentByID` / `CreateAuditLog`
- web: 証書セクション (一覧 + アップロードフォーム + ダウンロードリンク)、
  keys reveal、licenses create / update への FS 処理組込み
- handler テスト + filestore / metayml 単体テスト

### 範囲外 (別 PR)

- appmgr-generate-meta (全ライセンスの meta.yml / ディレクトリ backfill)
- appmgr-check-integrity (FS↔DB 整合チェック)
- 証書削除 UI / 監査記録の網羅 (フェーズ 15) / 部署スコープ認可 (継続負債)
- ZIP 一括ダウンロード (/admin/export)

## 内部設計

### internal/filestore

```go
type Store struct { base string; maxBytes int64; allowed map[string]bool }

// Save は upload を検証して dir (base からの相対) に保存し、
// 保存名・sha256・サイズ・判定 MIME を返す。
func (s *Store) Save(dir, originalName string, r io.Reader) (SavedFile, error)
// Open はダウンロード用に相対 stored_path を開く (パス脱出防止付き)。
func (s *Store) Open(storedPath string) (*os.File, error)
// SniffMIME は先頭バイトのマジックで pdf/png/jpeg を判定する。
```

Open は `filepath.Clean` 後に base 配下であることを検証 (stored_path は
DB 由来だが多層防御)。

### internal/metayml

仕様 §5.2 の例と同キー順の構造体を yaml.v3 で Marshal し、自動生成
ヘッダコメント 2 行を先頭に付けて tmp + rename で書く。日時は
`time.FixedZone("JST", 9*3600)` で ISO8601。

### キー閲覧フロー

```text
POST /licenses/{id}/keys/reveal (license_manager 以上, CSRF)
 → GetLicenseByID (product_keys 生値)
 → CreateAuditLog(action="license_keys.view", entity_type="license", entity_id=id)
 → renderShow に RevealedKeys を渡して 200 (Cache-Control: no-store)
```

audit INSERT が失敗したらキーを表示せず 500 (記録なしの閲覧を作らない)。

## TDD コミット順序

1. `docs(plans): 証書ファイルと meta.yml (L-3) の Plan ファイル`
2. `feat(config): FileStoreConfig を追加 (既定値 + validate + example)`
3. `test(filestore): 検証・保存・衝突回避・パス脱出防止 (RED)`
4. `feat(filestore): Store 実装 (GREEN)`
5. `test(metayml): 仕様形式の meta.yml 生成 (RED)`
6. `feat(metayml): Write 実装 (GREEN)`
7. `feat(repository): license_documents と audit_logs のクエリ (sqlc)`
8. `test(web): 証書アップロード/ダウンロード/キー閲覧 audit (RED)`
9. `feat(web): 証書セクションとキー閲覧、FS 処理の組込み (GREEN)`
10. `chore(config): config.example.yml に file_store セクション追記`
    (2 に含められれば統合可)

## 受け入れ基準

- 全ゲート緑 (build / test / race / lint)
- 実サーバで:
  - ライセンス新規作成で物理ディレクトリ + meta.yml が生成される
  - PDF アップロード → `<base>/<fs_dir_path>/` に保存、DB 行、meta.yml の
    documents に filename / sha256 が載る
  - 拡張子偽装 (中身 txt の .pdf) → 400。サイズ超過 → 413 相当のエラー
  - ダウンロードで元ファイル名・同一バイト列が返る
  - viewer: ダウンロード可、アップロード / キー閲覧 403
  - キー閲覧 POST でキーが表示され、audit_logs に app_user_id 付きで
    1 行入る。GET でキーが出る経路が無い
  - スラッグ変更で物理ディレクトリが追随 (os.Rename)
  - 衝突: 同名 slug の別ライセンス作成で fs_dir_path に `_2`
- filestore 単体: パス脱出 (../ を含む stored_path) が拒否される

## 想定リスク

- **meta.yml のキー順**: yaml.v3 の Marshal は struct 順なので仕様例の
  順に struct を定義すれば一致する。コメント付きヘッダは手前置き文字列
- **multipart のメモリ**: 20 MiB 上限なので ParseMultipartForm(32<<20)
  で十分。巨大化したらストリーミング保存に変える (今はしない)
- **既存 L-1/L-2 データ**: fs_dir_path はあるが物理ディレクトリ無しの行が
  検証環境に存在しうる。編集 / アップロード時の MkdirAll で自然回復する
  ことをテストで確認
