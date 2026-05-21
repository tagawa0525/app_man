//go:build unix

package lockfile

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Acquire は baseDir/<name>.lock を排他取得する。
//   - ModeShared / ModeServer: 自身の lock のみ
//   - ModeGlobal: 自身 + BatchBinaries の他バッチ全 lock を順次取得
//
// 取得済み（他プロセスが flock を保持中）なら ErrAlreadyHeld を返す。
// baseDir が存在しなければ作成する。
// ModeGlobal で途中で取得失敗した場合、それまでに取得した lock は逆順に release する。
func Acquire(baseDir, name string, mode Mode) (*Lock, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir %s: %w", baseDir, err)
	}

	primary, err := acquireOne(baseDir, name)
	if err != nil {
		return nil, err
	}
	lock := &Lock{primary: primary}

	if mode == ModeGlobal {
		for _, other := range BatchBinaries {
			if other == name {
				continue // 自分自身は primary で取得済み
			}
			extra, err := acquireOne(baseDir, other)
			if err != nil {
				// 部分取得した lock を Release で逆順解放する
				_ = lock.Release()
				return nil, err
			}
			lock.extras = append(lock.extras, extra)
		}
	}

	return lock, nil
}

// acquireOne は単一 lock ファイルに対する flock + metadata 書込を行う。
// Linux/macOS の flock(2) は同一プロセス内でも別 fd 同士で排他されるため、
// 同一プロセス内の二重 Acquire も EWOULDBLOCK で検出できる。
func acquireOne(baseDir, name string) (acquiredLock, error) {
	path := lockPath(baseDir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return acquiredLock{}, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return acquiredLock{}, fmt.Errorf("%s: %w", path, ErrAlreadyHeld)
		}
		return acquiredLock{}, fmt.Errorf("flock %s: %w", path, err)
	}
	if err := writeMetadata(f, name); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return acquiredLock{}, err
	}
	return acquiredLock{path: path, file: f}, nil
}

// Release は保持中の全 lock を解放し、lock ファイルを削除する。
// ModeGlobal で複数取得していた場合は extras を逆順、最後に primary の順で解放する。
// 解放中の最初のエラーを返し、残りの解放は継続する（fd リークを防ぐため）。
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	var firstErr error
	for i := len(l.extras) - 1; i >= 0; i-- {
		if err := releaseOne(&l.extras[i]); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	l.extras = nil
	if err := releaseOne(&l.primary); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// releaseOne は単一 acquiredLock を解放する。既に解放済みなら no-op。
// unlock と close の両方が成功した場合のみ lock ファイルを削除する。
// close が失敗して fd が残っている状態でファイルだけ消すと、次の Acquire が
// 新しい inode で flock 通過してしまい排他が破綻する。close 失敗時は
// ファイルを残し、後続の Acquire が flock 競合で正しく弾けるようにする。
func releaseOne(a *acquiredLock) error {
	if a == nil || a.file == nil {
		return nil
	}
	fd := int(a.file.Fd())
	flockErr := syscall.Flock(fd, syscall.LOCK_UN)
	closeErr := a.file.Close()
	a.file = nil

	if flockErr != nil {
		return fmt.Errorf("unlock %s: %w", a.path, flockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close lock file %s: %w", a.path, closeErr)
	}
	if err := os.Remove(a.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove lock file %s: %w", a.path, err)
	}
	return nil
}
