// appmgr-create-app-user: システム管理用ローカルアカウントの作成 /
// パスワードリセット。要件書 §4.2 / §7.3。
//
// auth_type='local' のアカウントだけが本コマンドの対象。一般社員
// (auth_type='ad') は AD パススルー認証のため app_man 側に認証情報を
// 持たず、AD 同期 (appmgr-sync-directory) が自動作成する。
//
// モード:
//   - create (default): --username --role [--department-code] [--notify-email]
//   - reset:            --reset-password --username
//
// パスワード入力: env APPMGR_INITIAL_PASSWORD を優先、未設定なら
// stdin プロンプト (TTY 必須、エコー抑制で 2 回確認入力)。
package main

import (
	"os"
)

const binaryName = "appmgr-create-app-user"

// exit code は clirun と揃える (要件書 §8.8 / import-bootstrap と同一)。
const (
	exitOK            = 0
	exitHandlerError  = 1
	exitLockConflict  = 2
	exitConfigInvalid = 3
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}
