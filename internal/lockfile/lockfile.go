// Package lockfile はバッチ CLI バイナリの多重起動を防ぐ排他制御を提供する。
// 要件書 § 8.8「バッチの排他制御」に対応する。
//
// 通常バッチは ModeShared で自身の lock のみを取得する。appmgr-backup だけは
// ModeGlobal で他全バッチの lock も相互排他取得する（VACUUM INTO 中の書込み
// 衝突を避けるため）。appmgr-server は ModeServer（バッチ系の Global 対象外）。
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
	// ModeGlobal は appmgr-backup 用。自身 + BatchBinaries の全 lock を排他取得する。
	ModeGlobal
	// ModeServer は appmgr-server 用。自身の lock のみ取得し、バッチ系の Global 対象外。
	ModeServer
)

// ErrAlreadyHeld は別プロセスが既に lock を保持していることを示す。
var ErrAlreadyHeld = errors.New("lock already held by another process")

// Lock は取得済みの lock を表す。
type Lock struct {
	baseDir string
	name    string
	file    *os.File
}

// lockMetadata は lock ファイルに 1 行 JSON で書き込む内容。
// PID は stale 判定に用いる。
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
