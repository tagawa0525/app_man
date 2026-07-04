---
name: verify
description: app_man の変更を実バイナリ/実サーバで end-to-end 検証する手順。CLI バッチと Web 画面 (curl + cookie + CSRF) の両方のレシピ。
---

# app_man 検証レシピ

テストの再実行ではなく、実バイナリを隔離環境で駆動して観察する。

## 隔離環境の構築 (共通)

```sh
V=<scratchpad>/verify-<topic>   # /tmp 直下ではなくセッションの scratchpad を使う
mkdir -p $V/data $V/logs
cat > $V/config.yml <<EOF
server:
  listen: 127.0.0.1:18180
  base_url: http://127.0.0.1:18180
  session_secret: verify-secret-0123456789abcdef
  cookie_secure: false
database:
  path: $V/data/app.db
  wal: true
locks:
  base_dir: $V/data/locks
logging:
  level: info
  base_dir: $V/logs
  format: json
auth:
  session_max_age_hours: 8
EOF
CGO_ENABLED=0 go build -o bin/appmgr-migrate ./cmd/migrate   # make build は差分なしだと no-op なので直 build が確実
bin/appmgr-migrate -config $V/config.yml -direction up
```

マスタ類のシードは sqlite3 で直接 INSERT が速い (departments は
`valid_to` で廃止部署も作れる。users は `deactivated_at` で退職者)。

## CLI バッチの検証

```sh
CGO_ENABLED=0 go build -o bin/appmgr-<name> ./cmd/<name>
bin/appmgr-<name> -config $V/config.yml [-dry-run]; echo exit=$?
tail -1 $V/logs/appmgr-<name>.log       # JSON 構造化ログが証跡
```

- exit code 規約: 0 OK / 1 handler error / 2 lock 競合 / 3 config 不正
- lock 競合は同時 2 プロセス起動で再現できる (片方が exit 2)

## Web 画面の検証 (curl + cookie + CSRF)

```sh
CGO_ENABLED=0 go build -o bin/appmgr-server ./cmd/server
APPMGR_INITIAL_PASSWORD='AdminPass123!' bin/appmgr-create-app-user \
  -config $V/config.yml -username admin -role system_admin
# 部署ロールのユーザは departments を先にシードして -department-code を渡す
(cd $V && /path/to/bin/appmgr-server -config $V/config.yml &)
curl -s http://127.0.0.1:18180/healthz   # "ok"

B=http://127.0.0.1:18180
login() {  # CSRF フィールド名は _csrf (csrf_token ではない)
  local t=$(curl -s -c $3 $B/login | grep -o 'name="_csrf" value="[^"]*"' | sed 's/.*value="//;s/"//')
  curl -s -b $3 -c $3 -d "username=$1" -d "password=$2" -d "_csrf=$t" -o /dev/null $B/login
}
tok() { curl -s -b $1 $B$2 | grep -o 'name="_csrf" value="[^"]*"' | head -1 | sed 's/.*value="//;s/"//'; }
login admin 'AdminPass123!' $V/a.jar
T=$(tok $V/a.jar /licenses/new)          # POST 前に同一セッションでトークン取得
curl -s -b $V/a.jar -d "..." -d "_csrf=$T" -w "%{http_code}\n" $B/<path>
```

- ログイン成功は 303。POST 成功も大抵 303 (PRG パターン)
- ロール別 403 の検証は create-app-user で viewer / license_manager 等を
  作り jar を分ける
- 検証後は `pkill -f "appmgr-server -config $V"` で停止

## 落とし穴

- `make build` は既存バイナリがあると no-op のことがある。検証対象は
  `go build -o bin/...` で明示的に再ビルドする
- サーバ起動ディレクトリに locks/ が作られるため `cd $V` してから起動
- SQLite の DATETIME 検証データは `datetime('now','-400 days')` 等で投入
