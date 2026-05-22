# flake から go-migrate を撤去する

## 背景

`nix develop` で開く dev shell の起動時、`shellHook` の `migrate -version` 実行で以下の panic が表示される：

```text
panic: failed to parse CA certificate.

goroutine 1 [running]:
github.com/snowflakedb/gosnowflake.readCACerts()
    github.com/snowflakedb/gosnowflake@v1.6.19/ocsp.go:881 +0x1fe
github.com/snowflakedb/gosnowflake.init.3()
    github.com/snowflakedb/gosnowflake@v1.6.19/ocsp.go:928 +0xf
```

これは nixpkgs の `go-migrate` 4.19.1 が同梱している `gosnowflake@v1.6.19` の既知バグ（`init()` 内で CA 証明書のパースに失敗）。`init()` で panic するため `migrate` バイナリは `-version` や `-help` を含め全ての呼び出しが exit 2 で落ちる。

`docs/plans/synthetic-mapping-treasure.md:48` の通り、本プロジェクトは既にこの panic を回避するため自前の `appmgr-migrate`（`cmd/migrate/`）で `internal/db.MigrateUp/Down` を呼ぶ構成に移行済み。`flake.nix` の `go-migrate` パッケージは shellHook のバージョン表示でしか使われておらず、整理漏れになっている。

## 方針

不要かつ壊れたパッケージを残しておくと「ある日 nixpkgs 側が直って動くようになる」までの間ノイズが出続けるので、参照を完全に削る。

- `flake.nix` の `packages` から `go-migrate` を削除
- `flake.nix` の `shellHook` から `migrate -version` 行を削除
- `README.md` / `CLAUDE.md` の「dev shell が提供するツール」一覧から `go-migrate`（CLI 名 `migrate`）への言及を削除

Go ライブラリとしての `golang-migrate/migrate` は `go.mod` 経由で取得し続けるため影響なし。`README.md:47` の「`go-migrate` ランナ」記述はライブラリ層の話なので残す。

## 影響範囲

- 開発者が `nix develop` で得るバイナリ集合から `migrate` CLI が消える。ただしどのドキュメント・スクリプトも CLI を使っていないため、操作上の差分は「panic が消える」のみ。
- マイグレーション実行は従来通り `make migrate-up` / `make migrate-down`（`appmgr-migrate` 経由）。

## 検証

`nix develop` で再ログインして panic が出ないこと、`make build` / `make test` が通ることを確認。
