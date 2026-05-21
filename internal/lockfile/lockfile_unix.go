//go:build unix

package lockfile

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Acquire は baseDir/<name>.lock を排他取得する。
// mode の意味は Mode 定数の comment 参照。
// 取得済み（他プロセスが flock を保持中）なら ErrAlreadyHeld を返す。
// baseDir が存在しなければ作成する。
func Acquire(baseDir, name string, mode Mode) (*Lock, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir %s: %w", baseDir, err)
	}
	// ModeGlobal の実装は後続コミットで追加する。
	// 現段階では全モードを自身の lock 取得のみで処理する。
	_ = mode
	return acquireOne(baseDir, name)
}

// acquireOne は単一 lock ファイルに対する flock + metadata 書込を行う。
// Linux/macOS の flock(2) は同一プロセス内でも別 fd 同士で排他されるため、
// 同一プロセス内の二重 Acquire も EWOULDBLOCK で検出できる。
func acquireOne(baseDir, name string) (*Lock, error) {
	path := lockPath(baseDir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%s: %w", path, ErrAlreadyHeld)
		}
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}
	if err := writeMetadata(f, name); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, err
	}
	return &Lock{baseDir: baseDir, name: name, file: f}, nil
}

// Release は保持中の lock fd を解放し、lock ファイルを削除する。
// 既に Release 済みなら nil を返す。
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	fd := int(l.file.Fd())
	flockErr := syscall.Flock(fd, syscall.LOCK_UN)
	closeErr := l.file.Close()
	removeErr := os.Remove(lockPath(l.baseDir, l.name))
	l.file = nil

	switch {
	case flockErr != nil:
		return fmt.Errorf("unlock: %w", flockErr)
	case closeErr != nil:
		return fmt.Errorf("close lock file: %w", closeErr)
	case removeErr != nil && !errors.Is(removeErr, os.ErrNotExist):
		return fmt.Errorf("remove lock file: %w", removeErr)
	}
	return nil
}
