//go:build windows

package lockfile

import "errors"

// errNotImplemented は windows 用 lock 実装が未投入であることを示す。
// 本番投入前に LockFileEx ベースの実装を別 PR で追加する。
var errNotImplemented = errors.New("lockfile: not implemented on windows (LockFileEx 実装は本番投入前の別 PR を予定)")

// Acquire は windows ではまだ実装されていない。クロスコンパイルを通すための
// スタブで、呼び出すと errNotImplemented を返す。
func Acquire(_, _ string, _ Mode) (*Lock, error) {
	return nil, errNotImplemented
}

// Release は windows ではまだ実装されていない。
func (l *Lock) Release() error {
	return errNotImplemented
}
