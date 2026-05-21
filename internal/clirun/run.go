// Package clirun はバッチ CLI バイナリの共通起動ヘルパーを提供する。
// 8 バッチバイナリ（appmgr-sync-directory ほか）の main 実装をここに集約し、
// フラグパース → config 読込 → logger 初期化 → lock 取得 → handler 実行 →
// 後片付けを共通化する。
//
// サーバ・migrate・create-app-user は形が異なるため対象外。
package clirun

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tagawa0525/app_man/internal/applog"
	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/lockfile"
)

// 以下の exit code は要件書 § 8.8 と Plan rustling-discovering-beaver.md PR3 の確定値。
const (
	exitOK            = 0
	exitHandlerError  = 1
	exitLockConflict  = 2 // lock 取得失敗（多重起動 / グローバルロック競合）
	exitConfigInvalid = 3 // フラグパース / config 読込 / logger 初期化の失敗
)

// Deps は handler に渡す依存物。
type Deps struct {
	Cfg    *config.Config
	Logger *slog.Logger
	DryRun bool
}

// Handler はバッチの実処理を表す関数。
// ctx は SIGINT / SIGTERM で cancel される。
type Handler func(ctx context.Context, deps Deps) error

// Run は 8 バッチ共通の main 実装。
// 内部で os.Exit を呼ぶ。返り値での通知が必要なテストは runMain を直接呼ぶこと。
func Run(binaryName string, mode lockfile.Mode, handler Handler) {
	os.Exit(runMain(os.Args[1:], os.Stderr, binaryName, mode, handler))
}

// runMain は Run の本体を exit code 返却にしたもの。
// args は CLI 引数（os.Args[1:] 相当）、stderr は失敗メッセージの出力先。
func runMain(args []string, stderr *os.File, binaryName string, mode lockfile.Mode, handler Handler) int {
	fs := flag.NewFlagSet(binaryName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "config.yml", "path to config.yml")
	dryRun := fs.Bool("dry-run", false, "validate inputs without committing changes")
	if err := fs.Parse(args); err != nil {
		return exitConfigInvalid
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: load config: %v\n", binaryName, err)
		return exitConfigInvalid
	}

	logger, closeLog, err := applog.New(cfg.Logging, binaryName)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: init logger: %v\n", binaryName, err)
		return exitConfigInvalid
	}
	defer func() {
		if cerr := closeLog(); cerr != nil {
			_, _ = fmt.Fprintf(stderr, "%s: close log: %v\n", binaryName, cerr)
		}
	}()

	lock, err := lockfile.Acquire(cfg.Locks.BaseDir, binaryName, mode)
	if err != nil {
		if errors.Is(err, lockfile.ErrAlreadyHeld) {
			logger.Warn("lock already held by another process; exiting",
				slog.String("error", err.Error()))
			return exitLockConflict
		}
		logger.Error("acquire lock", slog.Any("error", err))
		return exitHandlerError
	}
	// LIFO: Release → closeLog の順で実行され、release エラーも logger 経由で記録できる
	defer func() {
		if rerr := lock.Release(); rerr != nil {
			logger.Error("release lock", slog.Any("error", rerr))
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	deps := Deps{Cfg: cfg, Logger: logger, DryRun: *dryRun}
	if err := handler(ctx, deps); err != nil {
		logger.Error("handler", slog.Any("error", err))
		return exitHandlerError
	}
	return exitOK
}
