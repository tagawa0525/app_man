package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tagawa0525/app_man/internal/applog"
	"github.com/tagawa0525/app_man/internal/auth"
	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/db"
	"github.com/tagawa0525/app_man/internal/handler"
	"github.com/tagawa0525/app_man/internal/lockfile"
	"github.com/tagawa0525/app_man/internal/session"
	"github.com/tagawa0525/app_man/internal/view/static"
)

const binaryName = "appmgr-server"

// errServerLockHeld はサーバ多重起動を検知して exit 2 する経路の
// マーカーエラー。clirun のバッチ系 exit code 規約 (2 = lock 競合) に揃える。
var errServerLockHeld = errors.New("server lock already held by another process")

func main() {
	configPath := flag.String("config", "config.yml", "path to config.yml")
	flag.Parse()

	if err := run(*configPath); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		if errors.Is(err, errServerLockHeld) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger, closeLog, err := applog.New(cfg.Logging, binaryName)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer func() {
		if cerr := closeLog(); cerr != nil {
			fmt.Fprintf(os.Stderr, "%s: close log: %v\n", binaryName, cerr)
		}
	}()
	slog.SetDefault(logger)

	// lock は DB を開く前に取得する。多重起動時に同じ WAL ファイルを
	// 別プロセスと触ってしまう前段でブロックしたい。要件書 § 8.8 で
	// 「appmgr-server は別 lock で多重起動だけ防ぐ。バッチ系とは別管理」
	// と明記されているため ModeServer を使う。
	lock, err := lockfile.Acquire(cfg.Locks.BaseDir, binaryName, lockfile.ModeServer)
	if err != nil {
		if errors.Is(err, lockfile.ErrAlreadyHeld) {
			logger.Warn("server lock already held by another process; exiting",
				slog.String("error", err.Error()))
			return errServerLockHeld
		}
		return fmt.Errorf("acquire server lock: %w", err)
	}
	defer func() {
		if rerr := lock.Release(); rerr != nil {
			logger.Error("release server lock", slog.Any("error", rerr))
		}
	}()

	sqlDB, closeDB, err := db.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	// defer 登録順は LIFO で実行されるため、closeDB → release → closeLog
	// の順に走る。DB クローズ・lock release のエラーも logger が生きている
	// うちに記録できる。
	defer func() {
		if cerr := closeDB(); cerr != nil {
			logger.Error("close db", slog.Any("error", cerr))
		}
	}()

	if err := db.CheckVersion(sqlDB); err != nil {
		return fmt.Errorf("check schema version: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sessionStore := session.NewSQLiteStore(sqlDB)
	// 起動時に期限切れセッションを掃除する。best-effort なのでエラーで
	// 起動を止めない。定期 GC は需要発生まで cron バイナリを足さない。
	if deleted, err := sessionStore.DeleteExpired(ctx, time.Now()); err != nil {
		logger.Warn("session GC failed", slog.Any("error", err))
	} else if deleted > 0 {
		logger.Info("expired sessions deleted", slog.Int64("count", deleted))
	}

	r := handler.NewRouter(handler.Deps{
		Logger:        logger,
		DB:            sqlDB,
		StaticFS:      static.FS(),
		SessionStore:  sessionStore,
		CookieSecure:  cfg.Server.CookieSecure,
		SessionMaxAge: time.Duration(cfg.Auth.SessionMaxAgeHours) * time.Hour,
		Authenticator: auth.NewLocalAuthenticator(sqlDB),
	})

	srv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server starting", slog.String("listen", cfg.Server.Listen))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", slog.Any("error", err))
		return fmt.Errorf("shutdown: %w", err)
	}
	logger.Info("server stopped")
	return nil
}
