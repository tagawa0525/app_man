// Package lockfile はバッチ CLI バイナリの多重起動を防ぐ排他制御を提供する。
// 要件書 § 8.8「バッチの排他制御」に対応する。
//
// 通常バッチは ModeShared で自身の lock のみを取得する。appmgr-backup だけは
// ModeGlobal で他全バッチの lock も相互排他取得する（VACUUM INTO 中の書込み
// 衝突を避けるため）。appmgr-server は ModeServer（バッチ系の Global 対象外）。
//
// flock(2) はプロセス終了で fd と共にカーネルが自動解放するため、PID 生存
// 確認による stale 判定ロジックは入れない。lock ファイル残骸の上から再
// Acquire できる（flock が free なら成功、メタデータは上書き）。
package lockfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Mode は lock の取得モード。
type Mode int

const (
	// ModeShared は通常バッチ用。自身の lock のみ取得する。
	ModeShared Mode = iota
	// ModeGlobal は appmgr-backup 用。自身 + batchBinaries の全 lock を排他取得する。
	ModeGlobal
	// ModeServer は appmgr-server 用。自身の lock のみ取得し、バッチ系の Global 対象外。
	ModeServer
)

// batchBinaries は要件書 § 8.8 の排他制御対象バッチ。
// ModeGlobal で取得を試みる対象。順序は固定（取得・解放の決定性のため）。
//
// unexport にすることで、外部パッケージからスライス内容を書き換えられて
// ModeGlobal の対象が意図せず変わる事故を防ぐ。外部で参照したくなったら
// 配列コピーを返す getter を別途公開する。
var batchBinaries = []string{
	"appmgr-sync-directory",
	"appmgr-import-skysea",
	"appmgr-check-integrity",
	"appmgr-notify",
	"appmgr-backup",
	"appmgr-prune-logs",
	"appmgr-generate-meta",
	"appmgr-import-bootstrap",
}

// ErrAlreadyHeld は別プロセスが既に lock を保持していることを示す。
var ErrAlreadyHeld = errors.New("lock already held by another process")

// Lock は取得済みの lock を表す。
// primary は呼び出し側が指定した name の lock、extras は ModeGlobal で
// 追加取得した他バッチの lock（Release で逆順解放）。
type Lock struct {
	primary acquiredLock
	extras  []acquiredLock
}

// acquiredLock は単一 lock ファイルに対する取得結果。
type acquiredLock struct {
	path string
	file *os.File
}

// lockMetadata は lock ファイルに 1 行 JSON で書き込む内容。
// PID は運用時の「誰が hold 中か」確認用で、Acquire ロジックは参照しない。
type lockMetadata struct {
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
	Binary    string `json:"binary"`
}

func lockPath(baseDir, name string) string {
	return filepath.Join(baseDir, name+".lock")
}

// writeMetadata は取得直後の lock fd に PID 等の JSON を書き込む。
// flock 取得済み fd 前提のため、truncate と書込みを安全に実施できる。
func writeMetadata(f *os.File, name string) error {
	meta := lockMetadata{
		PID:       os.Getpid(),
		StartedAt: time.Now().Format(time.RFC3339),
		Binary:    name,
	}
	body, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("truncate lock file: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek lock file: %w", err)
	}
	if _, err := f.Write(body); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}
	return nil
}
